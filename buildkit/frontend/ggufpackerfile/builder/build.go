package builder

import (
	"context"
	"encoding/json"
	"fmt"
	"path"

	"github.com/containerd/platforms"
	ggufparser "github.com/gpustack/gguf-parser-go"
	"github.com/moby/buildkit/client/llb"
	"github.com/moby/buildkit/frontend"
	"github.com/moby/buildkit/frontend/gateway/client"
	"github.com/moby/buildkit/frontend/subrequests/lint"
	"github.com/moby/buildkit/frontend/subrequests/outline"
	"github.com/moby/buildkit/frontend/subrequests/targets"
	"github.com/moby/buildkit/solver/errdefs"
	"github.com/moby/buildkit/solver/pb"
	"github.com/pkg/errors"

	"github.com/gpustack/gguf-packer-go/buildkit/frontend/ggufpackerfile/ggufpackerfile2llb"
	"github.com/gpustack/gguf-packer-go/buildkit/frontend/ggufpackerfile/instructions"
	"github.com/gpustack/gguf-packer-go/buildkit/frontend/ggufpackerfile/linter"
	"github.com/gpustack/gguf-packer-go/buildkit/frontend/ggufpackerfile/parser"
	"github.com/gpustack/gguf-packer-go/buildkit/frontend/ggufpackerui"
	specs "github.com/gpustack/gguf-packer-go/buildkit/frontend/specs/v1"
)

func Build(ctx context.Context, c client.Client) (_ *client.Result, err error) {
	c = &withResolveCache{Client: c}
	bc, err := ggufpackerui.NewClient(c)
	if err != nil {
		return nil, err
	}

	src, err := bc.ReadEntrypoint(ctx, "GGUFPackerfile")
	if err != nil {
		return nil, err
	}

	convertOpt := ggufpackerfile2llb.ConvertOpt{
		Config:       bc.Config,
		Client:       bc,
		SourceMap:    src.SourceMap,
		MetaResolver: c,
		Warn: func(rulename, description, url, msg string, location []parser.Range) {
			startLine := 0
			if len(location) > 0 {
				startLine = location[0].Start.Line
			}
			msg = linter.LintFormatShort(rulename, msg, startLine)
			src.Warn(ctx, msg, warnOpts(location, [][]byte{[]byte(description)}, url))
		},
	}

	if res, ok, err := bc.HandleSubrequest(ctx, ggufpackerui.RequestHandler{
		Outline: func(ctx context.Context) (*outline.Outline, error) {
			return ggufpackerfile2llb.Outline(ctx, src.Data, convertOpt)
		},
		ListTargets: func(ctx context.Context) (*targets.List, error) {
			return ggufpackerfile2llb.ListTargets(ctx, src.Data)
		},
		Lint: func(ctx context.Context) (*lint.LintResults, error) {
			return ggufpackerfile2llb.Lint(ctx, src.Data, convertOpt)
		},
	}); err != nil {
		return nil, err
	} else if ok {
		return res, nil
	}

	defer func() {
		var el *parser.ErrorLocation
		if errors.As(err, &el) {
			for _, l := range el.Locations {
				err = wrapSource(err, src.SourceMap, l)
			}
		}
	}()

	rb, err := bc.Build(ctx, func(ctx context.Context, platform *specs.Platform, idx int) (client.Reference, *specs.Image, *specs.Image, error) {
		opt := convertOpt
		opt.TargetPlatform = platform
		if idx != 0 {
			opt.Warn = nil
		}

		st, img, baseImg, pt, err := ggufpackerfile2llb.ToLLB(ctx, src.Data, opt)
		if err != nil {
			return nil, nil, nil, err
		}

		def, err := st.Marshal(ctx)
		if err != nil {
			return nil, nil, nil, errors.Wrapf(err, "failed to marshal LLB definition")
		}

		r, err := c.Solve(ctx, client.SolveRequest{
			Definition:   def.ToPB(),
			CacheImports: bc.CacheImports,
		})
		if err != nil {
			return nil, nil, nil, errors.Wrapf(err, "failed to solve LLB definition")
		}

		ref, err := r.SingleRef()
		if err != nil {
			return nil, nil, nil, errors.Wrapf(err, "failed to get single ref")
		}

		if pt == nil {
			return ref, img, baseImg, nil
		}

		p := platforms.DefaultSpec()
		if platform != nil {
			p = *platform
		}
		id := platforms.Format(platforms.Normalize(p))

		ps := []*instructions.CmdParameter{
			pt.Cmd.Model,
			pt.Cmd.Drafter,
			pt.Cmd.Projector,
		}
		for i := range pt.Cmd.Adapters {
			ps = append(ps, &pt.Cmd.Adapters[i])
		}

		img.Config.Size = 0
		for i := range ps {
			if ps[i] == nil {
				continue
			}

			runArgs := []string{
				"gguf-parser",
				"--path",
				path.Join("/run/src", ps[i].Value),
				"--raw",
				"--raw-output",
				path.Join("/run/dest", ps[i].Type+".json"),
			}

			runOpt := []llb.RunOption{
				llb.WithCustomName(fmt.Sprintf("[%s] parsing %s GGUF file", id, ps[i].Type)),
				ggufpackerfile2llb.Location(src.SourceMap, pt.Cmd.Location()),
				llb.Args(runArgs),
				llb.AddMount("/run/src", pt.State, llb.Readonly),
				llb.AddMount("/tmp", llb.Scratch(), llb.Tmpfs()),
			}
			if pt.IgnoreCache {
				runOpt = append(runOpt, llb.IgnoreCache)
			}
			run := llb.Image(bc.ParseImage).Run(runOpt...)

			pst := run.AddMount("/run/dest", llb.Scratch())
			pdef, err := pst.Marshal(ctx)
			if err != nil {
				return nil, nil, nil, errors.Wrapf(err, "failed to marshal parsing LLB definition")
			}
			pr, err := c.Solve(ctx, frontend.SolveRequest{
				Definition: pdef.ToPB(),
			})
			if err != nil {
				return nil, nil, nil, errors.Wrapf(err, "failed to solve parsing LLB definition")
			}
			prr, err := pr.SingleRef()
			if err != nil {
				return nil, nil, nil, errors.Wrapf(err, "failed to get single parsing ref")
			}
			bs, err := prr.ReadFile(ctx, client.ReadRequest{
				Filename: ps[i].Type + ".json",
			})
			if err != nil {
				return nil, nil, nil, errors.Wrapf(err, "failed to read parsing result")
			}

			var gf ggufparser.GGUFFile
			if err = json.Unmarshal(bs, &gf); err != nil {
				return nil, nil, nil, errors.Wrapf(err, "failed to unmarshal parsing result")
			}
			m := gf.Model()
			mgf := specs.GGUFFile{
				GGUFFile:          gf,
				Architecture:      m.Architecture,
				Parameters:        m.Parameters,
				BitsPerWeight:     m.BitsPerWeight,
				FileType:          m.FileType,
				CmdParameterValue: ps[i].Value,
				CmdParameterIndex: ps[i].Index,
			}
			img.Config.Size += gf.Size
			switch ps[i].Type {
			case "model":
				// Labels.
				{
					if img.Config.Labels == nil {
						img.Config.Labels = map[string]string{}
					}
					lbs := img.Config.Labels
					setLabel(lbs, "gguf-packer", "org.opencontainers.image.vendor")
					setLabel(lbs, "text", "gguf.model.usage")
					setLabel(lbs, m.Architecture, "gguf.model.architecture")
					setLabel(lbs, m.Parameters.String(), "gguf.model.parameters")
					setLabel(lbs, m.BitsPerWeight.String(), "gguf.model.bpw")
					setLabel(lbs, m.FileType.String(), "gguf.model.filetype")
					if v := m.Name; v != "" {
						setLabel(lbs, v, "gguf.model.name", "org.opencontainers.image.title")
					}
					if v := m.Author; v != "" {
						setLabel(lbs, v, "gguf.model.authors", "org.opencontainers.image.authors")
					}
					if v := m.URL; v != "" {
						setLabel(lbs, v, "gguf.model.url", "org.opencontainers.image.url")
					}
					if v := m.Description; v != "" {
						setLabel(lbs, v, "gguf.model.description", "org.opencontainers.image.description")
					}
					if v := m.License; v != "" {
						setLabel(lbs, v, "gguf.model.licenses", "org.opencontainers.image.licenses")
					}
				}
				img.Config.Model = &mgf
			case "drafter":
				img.Config.Drafter = &mgf
			case "projector":
				img.Config.Projector = &mgf
			case "adapter":
				img.Config.Adapters = append(img.Config.Adapters, &mgf)
			}
		}

		return ref, img, baseImg, nil
	})
	if err != nil {
		return nil, err
	}

	return rb.Finalize()
}

func warnOpts(r []parser.Range, detail [][]byte, url string) client.WarnOpts {
	opts := client.WarnOpts{Level: 1, Detail: detail, URL: url}
	if r == nil {
		return opts
	}
	opts.Range = []*pb.Range{}
	for _, r := range r {
		opts.Range = append(opts.Range, &pb.Range{
			Start: pb.Position{
				Line:      int32(r.Start.Line),
				Character: int32(r.Start.Character),
			},
			End: pb.Position{
				Line:      int32(r.End.Line),
				Character: int32(r.End.Character),
			},
		})
	}
	return opts
}

func wrapSource(err error, sm *llb.SourceMap, ranges []parser.Range) error {
	if sm == nil {
		return err
	}
	s := errdefs.Source{
		Info: &pb.SourceInfo{
			Data:       sm.Data,
			Filename:   sm.Filename,
			Language:   sm.Language,
			Definition: sm.Definition.ToPB(),
		},
		Ranges: make([]*pb.Range, 0, len(ranges)),
	}
	for _, r := range ranges {
		s.Ranges = append(s.Ranges, &pb.Range{
			Start: pb.Position{
				Line:      int32(r.Start.Line),
				Character: int32(r.Start.Character),
			},
			End: pb.Position{
				Line:      int32(r.End.Line),
				Character: int32(r.End.Character),
			},
		})
	}
	return errdefs.WithSource(err, s)
}

func setLabel(lbs map[string]string, v string, k string, ks ...string) {
	if _, ok := lbs[k]; !ok {
		lbs[k] = v
	}
	for i := range ks {
		if _, ok := lbs[ks[i]]; !ok {
			lbs[ks[i]] = v
		}
	}
}
