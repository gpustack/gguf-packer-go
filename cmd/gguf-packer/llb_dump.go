package main

import (
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/distribution/reference"
	ggufpacker "github.com/gpustack/gguf-packer-go"
	"github.com/gpustack/gguf-packer-go/buildkit/frontend/ggufpackerfile/parser"
	"github.com/gpustack/gguf-packer-go/buildkit/frontend/ggufpackerui"
	"github.com/gpustack/gguf-packer-go/util/osx"
	"github.com/moby/buildkit/client/llb"
	"github.com/moby/buildkit/frontend/dockerui"
	"github.com/moby/buildkit/solver/pb"
	"github.com/opencontainers/go-digest"
	"github.com/spf13/cobra"
)

func llbDump(app string) *cobra.Command {
	var (
		browser  bool
		json     bool
		protobuf bool
	)

	c := &cobra.Command{
		Use:   "llb-dump [PATH]",
		Short: "Dump the BuildKit LLB of the GGUFPackerfile.",
		Example: sprintf(`  # Dump the BuildKit LLB of the current directory
  %s llb-dump

  # Dump the BuildKit LLB of the GGUFPackerfile
  %[1]s llb-dump /path/to/file

  # Dump the BuildKit LLB of a specific directory
  %[1]s llb-dump /path/to/dir

  # Dump the BuildKit LLB of the current directory as json format
  %[1]s llb-dump --json

  # Dump the BuildKit LLB of the current directory as protobuf format
  %[1]s llb-dump --protobuf | buildctl debug dump-llb`, app),
		Args: cobra.MaximumNArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			// Inspired by https://github.com/moby/buildkit/blob/bc92b63b98aa0968614240082997483f6bf68cbe/cmd/buildctl/debug/dumpllb.go#L30.
			var path string
			{
				if len(args) == 0 {
					pwd, err := os.Getwd()
					if err != nil {
						return fmt.Errorf("get working directory: %w", err)
					}
					path = pwd
				} else {
					path = osx.InlineTilde(args[0])
				}
			}
			switch {
			case osx.ExistsDir(path):
				f := filepath.Join(path, ggufpackerui.DefaultGGUFPackerfileName)
				if !osx.ExistsFile(f) {
					f = filepath.Join(path, dockerui.DefaultDockerfileName)
					if !osx.ExistsFile(f) {
						return fmt.Errorf("cannot find target file under %s", path)
					}
				}
				path = f
			case !osx.ExistsFile(path):
				return errors.New("cannot find target file")
			}

			var st *llb.State
			{
				bs, err := os.ReadFile(path)
				if err != nil {
					return fmt.Errorf("read %s: %w", path, err)
				}
				if filepath.Base(path) == dockerui.DefaultDockerfileName {
					ref, _, _, ok := parser.DetectSyntax(bs)
					if !ok {
						return errors.New("cannot detect syntax")
					}
					nd, err := reference.ParseNormalizedNamed(ref)
					if err != nil {
						return fmt.Errorf("parse reference from detect syntax: %w", err)
					}
					if !strings.HasSuffix(nd.String(), "/gguf-packer") {
						return errors.New("invalid syntax")
					}
				}
				st, err = ggufpacker.ToLLB(c.Context(), bs)
				if err != nil {
					return fmt.Errorf("parse GGUFPackerfile to LLB: %w", err)
				}
			}

			def, err := st.Marshal(c.Context())
			if err != nil {
				return fmt.Errorf("marshal LLB: %w", err)
			}

			w := c.OutOrStdout()
			if protobuf {
				return llb.WriteTo(def, w)
			}

			ops := make([]struct {
				Op         pb.Op         `json:"op"`
				Digest     digest.Digest `json:"digest"`
				OpMetadata pb.OpMetadata `json:"opMetadata"`
			}, len(def.Def))
			for i := range def.Def {
				if err = (&ops[i].Op).Unmarshal(def.Def[i]); err != nil {
					return fmt.Errorf("unmarshal op: %w", err)
				}
				ops[i].Digest = digest.FromBytes(def.Def[i])
				ops[i].OpMetadata = def.Metadata[ops[i].Digest]
			}

			if json {
				jprint(w, ops)
				return nil
			}

			sb := &strings.Builder{}
			if browser {
				w = io.MultiWriter(w, sb)
			}
			fprintf(w, "digraph {\n")
			for _, op := range ops {
				name, shape := dotAttr(op.Digest, op.Op)
				fprintf(w, "  %q [label=%q shape=%q];\n", op.Digest, name, shape)
			}
			for _, op := range ops {
				for i, inp := range op.Op.Inputs {
					label := ""
					if eo, ok := op.Op.Op.(*pb.Op_Exec); ok {
						for _, m := range eo.Exec.Mounts {
							if int(m.Input) == i && m.Dest != "/" {
								label = m.Dest
							}
						}
					}
					fprintf(w, "  %q -> %q [label=%q];\n", inp.Digest, op.Digest, label)
				}
			}
			fprintf(w, "}\n")
			if !browser {
				return nil
			}
			err = osx.OpenBrowser("https://dreampuf.github.io/GraphvizOnline/#" + url.PathEscape(sb.String()))
			if err == nil {
				fprint(w, "\n!!!View the graph at your default browser!!!\n")
			}
			return nil
		},
	}
	c.Flags().BoolVar(&browser, "browser", browser, "Output as dot format and try to display it on the default browser.")
	c.Flags().BoolVar(&json, "json", json, "Output as json format.")
	c.Flags().BoolVar(&protobuf, "protobuf", protobuf, "Output as protobuf format.")
	return c
}

func dotAttr(dgst digest.Digest, op pb.Op) (string, string) {
	switch op := op.Op.(type) {
	case *pb.Op_Source:
		return op.Source.Identifier, "ellipse"
	case *pb.Op_Exec:
		return strings.Join(op.Exec.Meta.Args, " "), "box"
	case *pb.Op_Build:
		return "build", "box3d"
	case *pb.Op_Merge:
		return "merge", "invtriangle"
	case *pb.Op_Diff:
		return "diff", "doublecircle"
	case *pb.Op_File:
		var names []string
		for _, action := range op.File.Actions {
			var name string
			switch act := action.Action.(type) {
			case *pb.FileAction_Copy:
				name = fmt.Sprintf("copy{src=%s, dest=%s}", act.Copy.Src, act.Copy.Dest)
			case *pb.FileAction_Mkfile:
				name = fmt.Sprintf("mkfile{path=%s}", act.Mkfile.Path)
			case *pb.FileAction_Mkdir:
				name = fmt.Sprintf("mkdir{path=%s}", act.Mkdir.Path)
			case *pb.FileAction_Rm:
				name = fmt.Sprintf("rm{path=%s}", act.Rm.Path)
			}
			names = append(names, name)
		}
		return strings.Join(names, ","), "note"
	default:
		return dgst.String(), "plaintext"
	}
}
