package ggufpackerfile2llb

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"math"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/containerd/platforms"
	"github.com/distribution/reference"
	"github.com/moby/buildkit/client/llb"
	"github.com/moby/buildkit/client/llb/imagemetaresolver"
	"github.com/moby/buildkit/client/llb/sourceresolver"
	"github.com/moby/buildkit/frontend/dockerfile/shell"
	"github.com/moby/buildkit/frontend/subrequests/lint"
	"github.com/moby/buildkit/frontend/subrequests/outline"
	"github.com/moby/buildkit/frontend/subrequests/targets"
	"github.com/moby/buildkit/identity"
	"github.com/moby/buildkit/solver/pb"
	"github.com/moby/buildkit/util/apicaps"
	"github.com/moby/buildkit/util/gitutil"
	"github.com/moby/buildkit/util/suggest"
	"github.com/moby/buildkit/util/system"
	"github.com/moby/patternmatcher"
	"github.com/opencontainers/go-digest"
	"github.com/pkg/errors"
	"golang.org/x/sync/errgroup"

	"github.com/gpustack/gguf-packer-go/buildkit/frontend/ggufpackerfile/instructions"
	"github.com/gpustack/gguf-packer-go/buildkit/frontend/ggufpackerfile/linter"
	"github.com/gpustack/gguf-packer-go/buildkit/frontend/ggufpackerfile/parser"
	"github.com/gpustack/gguf-packer-go/buildkit/frontend/ggufpackerui"
	specs "github.com/gpustack/gguf-packer-go/buildkit/frontend/specs/v1"
)

const (
	emptyImageName = "scratch"
	historyComment = "buildkit.ggufpackerfile.v0"
)

var (
	secretsRegexpOnce sync.Once
	secretsRegexp     *regexp.Regexp
)

type ConvertOpt struct {
	ggufpackerui.Config
	Client         *ggufpackerui.Client
	MainContext    *llb.State
	SourceMap      *llb.SourceMap
	TargetPlatform *specs.Platform
	MetaResolver   llb.ImageMetaResolver
	LLBCaps        *apicaps.CapSet
	Warn           linter.LintWarnFunc
	AllStages      bool
}

type ParseTarget struct {
	State llb.State
	Cmd   *instructions.CmdCommand

	IgnoreCache bool
}

func ToLLB(ctx context.Context, dt []byte, opt ConvertOpt) (*llb.State, *specs.Image, *specs.Image, *ParseTarget, error) {
	ds, err := toDispatchState(ctx, dt, opt)
	if err != nil {
		return nil, nil, nil, nil, err
	}

	// Labels.
	{
		if ds.image.Config.Labels == nil {
			ds.image.Config.Labels = make(map[string]string)
		}
		lbs := ds.image.Config.Labels
		ct := time.Now()
		if ds.image.Created != nil {
			ct = *ds.image.Created
		}
		setLabel(lbs, ct.Format(time.RFC3339), "org.opencontainers.image.created")
		if ds.stage.BaseName != emptyImageName {
			setLabel(lbs, ds.stage.BaseName, "org.opencontainers.image.base.name")
			if ds.stage.BaseDigest != "" {
				setLabel(lbs, ds.stage.BaseDigest, "org.opencontainers.image.base.digest")
			}
		}
	}

	var pt *ParseTarget
	switch {
	case ds.stage.CmdCommand != nil:
		ds.image.Config.Cmd = ds.stage.CmdCommand.Args
		pt = &ParseTarget{
			State:       ds.state,
			Cmd:         ds.stage.CmdCommand,
			IgnoreCache: ds.ignoreCache,
		}
	case ds.baseImg != nil:
		pt = &ParseTarget{
			State:       ds.state,
			Cmd:         &instructions.CmdCommand{Args: ds.baseImg.Config.Cmd},
			IgnoreCache: ds.ignoreCache,
		}
		if m := ds.baseImg.Config.Model; m != nil {
			pt.Cmd.Model = &instructions.CmdParameter{
				Type:  "model",
				Value: m.CmdParameterValue,
				Index: m.CmdParameterIndex,
			}
		}
		if d := ds.baseImg.Config.Drafter; d != nil {
			pt.Cmd.Drafter = &instructions.CmdParameter{
				Type:  "drafter",
				Value: d.CmdParameterValue,
				Index: d.CmdParameterIndex,
			}
		}
		if p := ds.baseImg.Config.Projector; p != nil {
			pt.Cmd.Projector = &instructions.CmdParameter{
				Type:  "projector",
				Value: p.CmdParameterValue,
				Index: p.CmdParameterIndex,
			}
		}
		for _, a := range ds.baseImg.Config.Adapters {
			if a == nil {
				continue
			}
			pt.Cmd.Adapters = append(pt.Cmd.Adapters, instructions.CmdParameter{
				Type:  "adapter",
				Value: a.CmdParameterValue,
				Index: a.CmdParameterIndex,
			})
		}
	}

	return &ds.state, &ds.image, ds.baseImg, pt, nil
}

func Outline(ctx context.Context, dt []byte, opt ConvertOpt) (*outline.Outline, error) {
	ds, err := toDispatchState(ctx, dt, opt)
	if err != nil {
		return nil, err
	}
	o := ds.Outline(dt)
	return &o, nil
}

func Lint(ctx context.Context, dt []byte, opt ConvertOpt) (*lint.LintResults, error) {
	results := &lint.LintResults{}
	sourceIndex := results.AddSource(opt.SourceMap)
	opt.Warn = func(rulename, description, url, fmtmsg string, location []parser.Range) {
		results.AddWarning(rulename, description, url, fmtmsg, sourceIndex, location)
	}
	// for lint, no target means all targets
	if opt.Target == "" {
		opt.AllStages = true
	}

	_, err := toDispatchState(ctx, dt, opt)

	var errLoc *parser.ErrorLocation
	if err != nil {
		buildErr := &lint.BuildError{
			Message: err.Error(),
		}
		if errors.As(err, &errLoc) {
			ranges := mergeLocations(errLoc.Locations...)
			buildErr.Location = toPBLocation(sourceIndex, ranges)
		}
		results.Error = buildErr
	}
	return results, nil
}

func ListTargets(ctx context.Context, dt []byte) (*targets.List, error) {
	ggufpackerfile, err := parser.Parse(bytes.NewReader(dt))
	if err != nil {
		return nil, err
	}

	stages, _, err := instructions.Parse(ggufpackerfile.AST, nil)
	if err != nil {
		return nil, err
	}

	l := &targets.List{
		Sources: [][]byte{dt},
	}

	for i, s := range stages {
		t := targets.Target{
			Name:        s.Name,
			Description: s.Comment,
			Default:     i == len(stages)-1,
			Base:        s.BaseName,
			Platform:    s.Platform,
			Location:    toSourceLocation(s.Location),
		}
		l.Targets = append(l.Targets, t)
	}
	return l, nil
}

func newRuleLinter(dt []byte, opt *ConvertOpt) (*linter.Linter, error) {
	var lintConfig *linter.Config
	if opt.Client != nil && opt.Client.LinterConfig != nil {
		lintConfig = opt.Client.LinterConfig
	} else {
		var err error
		lintOptionStr, _, _, _ := parser.ParseDirective("check", dt)
		lintConfig, err = linter.ParseLintOptions(lintOptionStr)
		if err != nil {
			return nil, errors.Wrapf(err, "failed to parse check options")
		}
	}
	lintConfig.Warn = opt.Warn
	return linter.New(lintConfig), nil
}

func toDispatchState(ctx context.Context, dt []byte, opt ConvertOpt) (*dispatchState, error) {
	if len(dt) == 0 {
		return nil, errors.Errorf("the GGUFPackerfile cannot be empty")
	}

	if opt.Client != nil && opt.MainContext != nil {
		return nil, errors.Errorf("Client and MainContext cannot both be provided")
	}

	namedContext := func(ctx context.Context, name string, copt ggufpackerui.ContextOpt) (*llb.State, *specs.Image, error) {
		if opt.Client == nil {
			return nil, nil, nil
		}
		if !strings.EqualFold(name, "scratch") && !strings.EqualFold(name, "context") {
			if copt.Platform == nil {
				copt.Platform = opt.TargetPlatform
			}
			return opt.Client.NamedContext(ctx, name, copt)
		}
		return nil, nil, nil
	}

	lint, err := newRuleLinter(dt, &opt)
	if err != nil {
		return nil, err
	}

	if opt.Client != nil && opt.LLBCaps == nil {
		caps := opt.Client.BuildOpts().LLBCaps
		opt.LLBCaps = &caps
	}

	platformOpts := buildPlatformOpt(&opt)

	optMetaArgs := getPlatformArgs(platformOpts)
	for i, arg := range optMetaArgs {
		optMetaArgs[i] = setKVValue(arg, opt.BuildArgs)
	}

	ggufpackerfile, err := parser.Parse(bytes.NewReader(dt))
	if err != nil {
		return nil, err
	}

	// Moby still uses the `ggufpackerfile.PrintWarnings` method to print non-empty
	// continuation line warnings. We iterate over those warnings here.
	for _, warning := range ggufpackerfile.Warnings {
		// The `ggufpackerfile.Warnings` *should* only contain warnings about empty continuation
		// lines, but we'll check the warning message to be sure, so that we don't accidentally
		// process warnings that are not related to empty continuation lines twice.
		if strings.HasPrefix(warning.Short, "Empty continuation line found in: ") {
			location := []parser.Range{*warning.Location}
			msg := linter.RuleNoEmptyContinuation.Format()
			lint.Run(&linter.RuleNoEmptyContinuation, location, msg)
		}
	}

	validateCommandCasing(ggufpackerfile, lint)

	proxyEnv := proxyEnvFromBuildArgs(opt.BuildArgs)

	stages, metaArgs, err := instructions.Parse(ggufpackerfile.AST, lint)
	if err != nil {
		return nil, err
	}
	if len(stages) == 0 {
		return nil, errors.New("ggufpackerfile contains no stages to build")
	}
	validateStageNames(stages, lint)

	shlex := shell.NewLex(ggufpackerfile.EscapeToken)
	outline := newOutlineCapture()

	// Validate that base images continue to be valid even
	// when no build arguments are used.
	validateBaseImagesWithDefaultArgs(stages, shlex, metaArgs, optMetaArgs, lint)

	// Rebuild the arguments using the provided build arguments
	// for the remainder of the build.
	optMetaArgs, outline.allArgs, err = buildMetaArgs(optMetaArgs, shlex, metaArgs, opt.BuildArgs)
	if err != nil {
		return nil, err
	}

	metaResolver := opt.MetaResolver
	if metaResolver == nil {
		metaResolver = imagemetaresolver.Default()
	}

	allDispatchStates := newDispatchStates()

	// set base state for every image
	for i, st := range stages {
		env := metaArgsToEnvs(optMetaArgs)
		nameMatch, err := shlex.ProcessWordWithMatches(st.BaseName, env)
		reportUnusedFromArgs(metaArgsKeys(optMetaArgs), nameMatch.Unmatched, st.Location, lint)
		used := nameMatch.Matched
		if used == nil {
			used = map[string]struct{}{}
		}

		if err != nil {
			return nil, parser.WithLocation(err, st.Location)
		}
		if nameMatch.Result == "" {
			return nil, parser.WithLocation(errors.Errorf("base name (%s) should not be blank", st.BaseName), st.Location)
		}
		st.BaseName = nameMatch.Result

		ds := &dispatchState{
			stage:          st,
			deps:           make(map[*dispatchState]instructions.Command),
			ctxPaths:       make(map[string]struct{}),
			paths:          make(map[string]struct{}),
			stageName:      st.Name,
			prefixPlatform: opt.MultiPlatformRequested,
			outline:        outline.clone(),
			epoch:          opt.Epoch,
		}

		if st.Name != "" {
			s, img, err := namedContext(ctx, st.Name, ggufpackerui.ContextOpt{
				Platform:       ds.platform,
				ResolveMode:    opt.ImageResolveMode.String(),
				AsyncLocalOpts: ds.asyncLocalOpts,
			})
			if err != nil {
				return nil, err
			}
			if s != nil {
				ds.noinit = true
				ds.state = *s
				if img != nil {
					// timestamps are inherited as-is, regardless to SOURCE_DATE_EPOCH
					// https://github.com/moby/buildkit/issues/4614
					ds.image = *img
					if img.Architecture != "" && img.OS != "" {
						ds.platform = &specs.Platform{
							OS:           img.OS,
							Architecture: img.Architecture,
							Variant:      img.Variant,
							OSVersion:    img.OSVersion,
						}
						if img.OSFeatures != nil {
							ds.platform.OSFeatures = append([]string{}, img.OSFeatures...)
						}
					}
				}
				allDispatchStates.addState(ds)
				continue
			}
		}

		if st.Name == "" {
			ds.stageName = fmt.Sprintf("stage-%d", i)
		}

		allDispatchStates.addState(ds)

		for k := range used {
			ds.outline.usedArgs[k] = struct{}{}
		}

		total := 0
		if ds.stage.BaseName != emptyImageName && ds.base == nil {
			total = 1
		}
		for _, cmd := range ds.stage.Commands {
			switch cmd.(type) {
			case *instructions.AddCommand, *instructions.CopyCommand:
				total++
			case *instructions.ConvertCommand, *instructions.QuantizeCommand, *instructions.CatCommand:
				total++
			}
		}
		ds.cmdTotal = total
		if opt.Client != nil {
			ds.ignoreCache = opt.Client.IsNoCache(st.Name)
		}
	}

	var target *dispatchState
	if opt.Target == "" {
		target = allDispatchStates.lastTarget()
	} else {
		var ok bool
		target, ok = allDispatchStates.findStateByName(opt.Target)
		if !ok {
			return nil, errors.Errorf("target stage %q could not be found", opt.Target)
		}
	}

	// fill dependencies to stages so unreachable ones can avoid loading image configs
	for _, d := range allDispatchStates.states {
		d.commands = make([]command, len(d.stage.Commands))
		for i, cmd := range d.stage.Commands {
			newCmd, err := toCommand(cmd, allDispatchStates, opt)
			if err != nil {
				return nil, err
			}
			d.commands[i] = newCmd
			for _, src := range newCmd.sources {
				if src != nil {
					d.deps[src] = cmd
					if src.unregistered {
						allDispatchStates.addState(src)
					}
				}
			}
		}
	}

	if err = validateCircularDependency(allDispatchStates.states); err != nil {
		return nil, err
	}

	if len(allDispatchStates.states) == 1 {
		allDispatchStates.states[0].stageName = ""
	}

	allStageNames := make([]string, 0, len(allDispatchStates.states))
	for _, s := range allDispatchStates.states {
		if s.stageName != "" {
			allStageNames = append(allStageNames, s.stageName)
		}
	}
	allReachable := allReachableStages(target)

	baseCtx := ctx
	eg, ctx := errgroup.WithContext(ctx)
	for i, d := range allDispatchStates.states {
		_, reachable := allReachable[d]
		if opt.AllStages {
			reachable = true
		}
		// resolve image config for every stage
		if d.base == nil && !d.noinit {
			if d.stage.BaseName == emptyImageName {
				d.state = llb.Scratch()
				d.image = emptyImage(platformOpts.targetPlatform)
				d.platform = &platformOpts.targetPlatform
				if d.unregistered {
					d.noinit = true
				}
				continue
			}
			func(i int, d *dispatchState) {
				eg.Go(func() (err error) {
					defer func() {
						if err != nil {
							err = parser.WithLocation(err, d.stage.Location)
						}
						if d.unregistered {
							// implicit stages don't need further dispatch
							d.noinit = true
						}
					}()
					origName := d.stage.BaseName
					ref, err := reference.ParseNormalizedNamed(d.stage.BaseName)
					if err != nil {
						return errors.Wrapf(err, "failed to parse stage name %q", d.stage.BaseName)
					}
					platform := d.platform
					if platform == nil {
						platform = &platformOpts.targetPlatform
					}
					d.stage.BaseName = reference.TagNameOnly(ref).String()

					var isScratch bool
					st, img, err := namedContext(ctx, d.stage.BaseName, ggufpackerui.ContextOpt{
						ResolveMode:    opt.ImageResolveMode.String(),
						Platform:       platform,
						AsyncLocalOpts: d.asyncLocalOpts,
					})
					if err != nil {
						return err
					}
					if st != nil {
						if img != nil {
							d.image = *img
						} else {
							d.image = emptyImage(platformOpts.targetPlatform)
						}
						d.state = st.Platform(*platform)
						d.platform = platform
						return nil
					}
					if reachable {
						prefix := "["
						if opt.MultiPlatformRequested && platform != nil {
							prefix += platforms.Format(*platform) + " "
						}
						prefix += "internal]"
						mutRef, dgst, dt, err := metaResolver.ResolveImageConfig(ctx, d.stage.BaseName, sourceresolver.Opt{
							LogName:  fmt.Sprintf("%s load metadata for %s", prefix, d.stage.BaseName),
							Platform: platform,
							ImageOpt: &sourceresolver.ResolveImageOpt{
								ResolveMode: opt.ImageResolveMode.String(),
							},
						})
						if err != nil {
							return suggest.WrapError(errors.Wrap(err, origName), origName, allStageNames, true)
						}

						if ref.String() != mutRef {
							ref, err = reference.ParseNormalizedNamed(mutRef)
							if err != nil {
								return errors.Wrapf(err, "failed to parse ref %q", mutRef)
							}
						}
						var img specs.Image
						if err := json.Unmarshal(dt, &img); err != nil {
							return errors.Wrap(err, "failed to parse image config")
						}
						d.baseImg = cloneX(&img) // immutable
						img.Created = nil
						// if there is no explicit target platform, try to match based on image config
						if d.platform == nil && platformOpts.implicitTarget {
							p := autoDetectPlatform(img, *platform, platformOpts.buildPlatforms)
							platform = &p
						}
						if dgst != "" {
							ref, err = reference.WithDigest(ref, dgst)
							if err != nil {
								return err
							}
						}
						d.stage.BaseDigest = dgst.String()
						d.stage.BaseName = ref.String()
						if len(img.RootFS.DiffIDs) == 0 {
							isScratch = true
							// schema1 images can't return diffIDs so double check :(
							for _, h := range img.History {
								if !h.EmptyLayer {
									isScratch = false
									break
								}
							}
						}
						d.image = img
					}
					if isScratch {
						d.state = llb.Scratch()
					} else {
						d.state = llb.Image(d.stage.BaseName,
							dfCmd(d.stage.SourceCode),
							llb.Platform(*platform),
							opt.ImageResolveMode,
							llb.WithCustomName(prefixCommand(d, "FROM "+d.stage.BaseName, opt.MultiPlatformRequested, platform, emptyEnvs{})),
							Location(opt.SourceMap, d.stage.Location),
						)
						if reachable {
							validateBaseImagePlatform(origName, *platform, d.image.Platform, d.stage.Location, lint)
						}
					}
					d.platform = platform
					return nil
				})
			}(i, d)
		}
	}
	if err := eg.Wait(); err != nil {
		return nil, err
	}

	ctx = baseCtx
	buildContext := &mutableOutput{}
	ctxPaths := map[string]struct{}{}

	var ggufpackerIgnoreMatcher *patternmatcher.PatternMatcher
	if opt.Client != nil {
		dockerIgnorePatterns, err := opt.Client.GGUFPackerIgnorePatterns(ctx)
		if err != nil {
			return nil, err
		}
		if len(dockerIgnorePatterns) > 0 {
			ggufpackerIgnoreMatcher, err = patternmatcher.New(dockerIgnorePatterns)
			if err != nil {
				return nil, err
			}
		}
	}

	for _, d := range allDispatchStates.states {
		if !opt.AllStages {
			if _, ok := allReachable[d]; !ok || d.noinit {
				continue
			}
		}
		d.init()

		// Ensure platform is set.
		if d.platform == nil {
			d.platform = &d.opt.targetPlatform
		}

		d.state = d.state.Network(opt.NetworkMode)
		for _, arg := range optMetaArgs {
			d.state = d.state.AddEnv(arg.Key, *arg.Value)
		}

		d.opt = dispatchOpt{
			allDispatchStates:       allDispatchStates,
			metaArgs:                optMetaArgs,
			buildArgValues:          opt.BuildArgs,
			shlex:                   shlex,
			buildContext:            llb.NewState(buildContext),
			proxyEnv:                proxyEnv,
			cacheIDNamespace:        opt.CacheIDNamespace,
			buildPlatforms:          platformOpts.buildPlatforms,
			targetPlatform:          platformOpts.targetPlatform,
			extraHosts:              opt.ExtraHosts,
			shmSize:                 opt.ShmSize,
			ulimit:                  opt.Ulimits,
			cgroupParent:            opt.CgroupParent,
			llbCaps:                 opt.LLBCaps,
			sourceMap:               opt.SourceMap,
			lint:                    lint,
			ggufpackerIgnoreMatcher: ggufpackerIgnoreMatcher,
		}

		for _, cmd := range d.commands {
			if err = dispatch(d, cmd, d.opt); err != nil {
				return nil, parser.WithLocation(err, cmd.Location())
			}
		}

		for p := range d.ctxPaths {
			ctxPaths[p] = struct{}{}
		}
	}

	// Ensure the entirety of the target state is marked as used.
	// This is done after we've already evaluated every stage to ensure
	// the paths attribute is set correctly.
	target.paths["/"] = struct{}{}

	if len(opt.Labels) != 0 && target.image.Config.Labels == nil {
		target.image.Config.Labels = make(map[string]string, len(opt.Labels))
	}
	maps.Copy(target.image.Config.Labels, opt.Labels)

	// If lint.Error() returns an error, it means that
	// there were warnings, and that our linter has been
	// configured to return an error on warnings,
	// so we appropriately return that error here.
	if err := lint.Error(); err != nil {
		return nil, err
	}

	opts := filterPaths(ctxPaths)
	bctx := opt.MainContext
	if opt.Client != nil {
		bctx, err = opt.Client.MainContext(ctx, opts...)
		if err != nil {
			return nil, err
		}
	} else if bctx == nil {
		bctx = ggufpackerui.DefaultMainContext(opts...)
	}
	buildContext.Output = bctx.Output()

	defaults := []llb.ConstraintsOpt{
		llb.Platform(platformOpts.targetPlatform),
	}
	if opt.LLBCaps != nil {
		defaults = append(defaults, llb.WithCaps(*opt.LLBCaps))
	}
	target.state = target.state.SetMarshalDefaults(defaults...)

	if !platformOpts.implicitTarget {
		target.image.OS = platformOpts.targetPlatform.OS
		target.image.Architecture = platformOpts.targetPlatform.Architecture
		target.image.Variant = platformOpts.targetPlatform.Variant
		target.image.OSVersion = platformOpts.targetPlatform.OSVersion
		if platformOpts.targetPlatform.OSFeatures != nil {
			target.image.OSFeatures = append([]string{}, platformOpts.targetPlatform.OSFeatures...)
		}
	}

	return target, nil
}

func metaArgsToEnvs(metaArgs []instructions.KeyValuePairOptional) shell.EnvGetter {
	return &envsFromKeyValuePairs{in: metaArgs}
}

type envsFromKeyValuePairs struct {
	in   []instructions.KeyValuePairOptional
	once sync.Once
	m    map[string]string
}

func (e *envsFromKeyValuePairs) init() {
	if len(e.in) == 0 {
		return
	}
	e.m = make(map[string]string, len(e.in))
	for _, kv := range e.in {
		e.m[kv.Key] = kv.ValueString()
	}
}

func (e *envsFromKeyValuePairs) Get(key string) (string, bool) {
	e.once.Do(e.init)
	v, ok := e.m[key] // windows: case-insensitive
	return v, ok
}

func (e *envsFromKeyValuePairs) Keys() []string {
	keys := make([]string, len(e.in))
	for i, kp := range e.in {
		keys[i] = kp.Key
	}
	return keys
}

func metaArgsKeys(metaArgs []instructions.KeyValuePairOptional) []string {
	s := make([]string, 0, len(metaArgs))
	for _, arg := range metaArgs {
		s = append(s, arg.Key)
	}
	return s
}

func toCommand(ic instructions.Command, allDispatchStates *dispatchStates, opt ConvertOpt) (command, error) {
	cmd := command{Command: ic}

	if c, ok := ic.(instructions.FromGetter); ok {
		if from := c.GetFrom(); from != "" {
			var stn *dispatchState
			index, err := strconv.Atoi(from)
			if err != nil {
				stn, ok = allDispatchStates.findStateByName(from)
				if !ok {
					if from == "context" {
						stn = &dispatchState{
							stage:  instructions.Stage{Name: "context", Location: ic.Location()},
							deps:   make(map[*dispatchState]instructions.Command),
							paths:  make(map[string]struct{}),
							noinit: true,
						}
					} else {
						stn = &dispatchState{
							stage:        instructions.Stage{BaseName: from, Location: ic.Location()},
							deps:         make(map[*dispatchState]instructions.Command),
							paths:        make(map[string]struct{}),
							unregistered: true,
						}
					}
				}
			} else {
				stn, err = allDispatchStates.findStateByIndex(index)
				if err != nil {
					return command{}, err
				}
			}
			cmd.sources = []*dispatchState{stn}
		}
	}

	var img string
	switch ic.(type) {
	default:
		return cmd, nil
	case *instructions.ConvertCommand:
		img = opt.ConvertImage
	case *instructions.QuantizeCommand:
		img = opt.QuantizeImage
	}
	if img == "" {
		img = ggufpackerui.DefaultImage
	}
	cmd.sources = append(cmd.sources, &dispatchState{
		stage:        instructions.Stage{BaseName: img, Location: ic.Location()},
		deps:         make(map[*dispatchState]instructions.Command),
		paths:        make(map[string]struct{}),
		unregistered: true,
	})

	return cmd, nil
}

type dispatchOpt struct {
	allDispatchStates       *dispatchStates
	metaArgs                []instructions.KeyValuePairOptional
	buildArgValues          map[string]string
	shlex                   *shell.Lex
	buildContext            llb.State
	proxyEnv                *llb.ProxyEnv
	cacheIDNamespace        string
	targetPlatform          specs.Platform
	buildPlatforms          []specs.Platform
	extraHosts              []llb.HostIP
	shmSize                 int64
	ulimit                  []pb.Ulimit
	cgroupParent            string
	llbCaps                 *apicaps.CapSet
	sourceMap               *llb.SourceMap
	lint                    *linter.Linter
	ggufpackerIgnoreMatcher *patternmatcher.PatternMatcher
}

func getEnv(state llb.State) shell.EnvGetter {
	return &envsFromState{state: &state}
}

type envsFromState struct {
	state *llb.State
	once  sync.Once
	env   shell.EnvGetter
}

func (e *envsFromState) init() {
	env, err := e.state.Env(context.TODO())
	if err != nil {
		return
	}
	e.env = env
}

func (e *envsFromState) Get(key string) (string, bool) {
	e.once.Do(e.init)
	return e.env.Get(key)
}

func (e *envsFromState) Keys() []string {
	e.once.Do(e.init)
	return e.env.Keys()
}

func dispatch(d *dispatchState, cmd command, opt dispatchOpt) (err error) {
	// ARG command value could be ignored, so defer handling the expansion error
	_, isArg := cmd.Command.(*instructions.ArgCommand)
	if ex, ok := cmd.Command.(instructions.SupportsSingleWordExpansion); ok && !isArg {
		err := ex.Expand(func(word string) (string, error) {
			env := getEnv(d.state)
			newword, unmatched, err := opt.shlex.ProcessWord(word, env)
			reportUnmatchedVariables(cmd, d.buildArgs, env, unmatched, &opt)
			return newword, err
		})
		if err != nil {
			return err
		}
	}
	if ex, ok := cmd.Command.(instructions.SupportsSingleWordExpansionRaw); ok {
		err := ex.ExpandRaw(func(word string) (string, error) {
			lex := shell.NewLex('\\')
			lex.SkipProcessQuotes = true
			env := getEnv(d.state)
			newword, unmatched, err := lex.ProcessWord(word, env)
			reportUnmatchedVariables(cmd, d.buildArgs, env, unmatched, &opt)
			return newword, err
		})
		if err != nil {
			return err
		}
	}

	switch c := cmd.Command.(type) {
	case *instructions.AddCommand:
		var checksum digest.Digest
		if c.Checksum != "" {
			checksum, err = digest.Parse(c.Checksum)
		}
		if err == nil {
			err = dispatchCopy(d, copyConfig{
				params:          c.SourcesAndDest,
				excludePatterns: c.ExcludePatterns,
				source:          opt.buildContext,
				isAddCommand:    true,
				cmdToPrint:      c,
				chown:           c.Chown,
				chmod:           c.Chmod,
				link:            c.Link,
				keepGitDir:      c.KeepGitDir,
				checksum:        checksum,
				location:        c.Location(),
				ignoreMatcher:   opt.ggufpackerIgnoreMatcher,
				opt:             opt,
			})
		}
		if err == nil {
			for _, src := range c.SourcePaths {
				if !strings.HasPrefix(src, "http://") && !strings.HasPrefix(src, "https://") {
					d.ctxPaths[path.Join("/", filepath.ToSlash(src))] = struct{}{}
				}
			}
		}
	case *instructions.ArgCommand:
		err = dispatchArg(d, c, &opt)
	case *instructions.CatCommand:
		err = dispatchCat(d, c, &opt)
	case *instructions.CmdCommand:
		err = dispatchCmd(d, c, opt.lint)
	case *instructions.CopyCommand:
		l := opt.buildContext
		var ignoreMatcher *patternmatcher.PatternMatcher
		if len(cmd.sources) != 0 {
			src := cmd.sources[0]
			if !src.noinit {
				return errors.Errorf("cannot copy from stage %q, it needs to be defined before current stage %q", c.From, d.stageName)
			}
			l = src.state
		} else {
			ignoreMatcher = opt.ggufpackerIgnoreMatcher
		}
		err = dispatchCopy(d, copyConfig{
			params:          c.SourcesAndDest,
			excludePatterns: c.ExcludePatterns,
			source:          l,
			isAddCommand:    false,
			cmdToPrint:      c,
			chown:           c.Chown,
			chmod:           c.Chmod,
			link:            c.Link,
			parents:         c.Parents,
			location:        c.Location(),
			ignoreMatcher:   ignoreMatcher,
			opt:             opt,
		})
		if err == nil {
			if len(cmd.sources) != 0 {
				source := cmd.sources[0]
				if source.paths == nil {
					source.paths = make(map[string]struct{})
				}
				for _, src := range c.SourcePaths {
					source.paths[path.Join("/", filepath.ToSlash(src))] = struct{}{}
				}
			}
		}
	case *instructions.ConvertCommand:
		sts := make([]llb.State, len(cmd.sources))
		for i := range cmd.sources {
			st := cmd.sources[i].state
			if i == 0 && cmd.sources[0].stage.Name == "context" {
				st = opt.buildContext
			}
			sts[i] = st
		}
		err = dispatchConvert(d, c, &opt, sts)
	case *instructions.LabelCommand:
		err = dispatchLabel(d, c, opt.lint)
	case *instructions.QuantizeCommand:
		sts := make([]llb.State, len(cmd.sources))
		for i := range cmd.sources {
			st := cmd.sources[i].state
			if i == 0 && cmd.sources[0].stage.Name == "context" {
				st = opt.buildContext
			}
			sts[i] = st
		}
		err = dispatchQuantize(d, c, &opt, sts)
	default:
	}
	return err
}

type dispatchState struct {
	opt       dispatchOpt
	state     llb.State
	image     specs.Image
	platform  *specs.Platform
	stage     instructions.Stage
	base      *dispatchState
	baseImg   *specs.Image // immutable, unlike image
	noinit    bool
	deps      map[*dispatchState]instructions.Command
	buildArgs []instructions.KeyValuePairOptional
	commands  []command
	// ctxPaths marks the paths this dispatchState uses from the build context.
	ctxPaths map[string]struct{}
	// paths marks the paths that are used by this dispatchState.
	paths          map[string]struct{}
	ignoreCache    bool
	unregistered   bool
	stageName      string
	cmdIndex       int
	cmdTotal       int
	prefixPlatform bool
	outline        outlineCapture
	epoch          *time.Time

	cmd instructionTracker
}

func (ds *dispatchState) asyncLocalOpts() []llb.LocalOption {
	return filterPaths(ds.paths)
}

// init is invoked when the dispatch state inherits its attributes
// from the base image.
func (ds *dispatchState) init() {
	// mark as initialized, used to determine states that have not been dispatched yet
	ds.noinit = true

	if ds.base == nil {
		return
	}

	ds.state = ds.base.state
	ds.platform = ds.base.platform
	ds.image = clone(ds.base.image)
	ds.baseImg = cloneX(ds.base.baseImg)
	// Utilize the same path index as our base image so we propagate
	// the paths we use back to the base image.
	ds.paths = ds.base.paths
	ds.buildArgs = append(ds.buildArgs, ds.base.buildArgs...)
}

type dispatchStates struct {
	states       []*dispatchState
	statesByName map[string]*dispatchState
}

func newDispatchStates() *dispatchStates {
	return &dispatchStates{statesByName: map[string]*dispatchState{}}
}

func (dss *dispatchStates) addState(ds *dispatchState) {
	dss.states = append(dss.states, ds)

	if d, ok := dss.statesByName[ds.stage.BaseName]; ok {
		ds.base = d
		ds.outline = d.outline.clone()
	}
	if ds.stage.Name != "" {
		dss.statesByName[strings.ToLower(ds.stage.Name)] = ds
	}
}

func (dss *dispatchStates) findStateByName(name string) (*dispatchState, bool) {
	ds, ok := dss.statesByName[strings.ToLower(name)]
	return ds, ok
}

func (dss *dispatchStates) findStateByIndex(index int) (*dispatchState, error) {
	if index < 0 || index >= len(dss.states) {
		return nil, errors.Errorf("invalid stage index %d", index)
	}

	return dss.states[index], nil
}

func (dss *dispatchStates) lastTarget() *dispatchState {
	return dss.states[len(dss.states)-1]
}

type command struct {
	instructions.Command
	sources []*dispatchState
}

func dispatchArg(d *dispatchState, c *instructions.ArgCommand, opt *dispatchOpt) error {
	commitStrs := make([]string, 0, len(c.Args))
	for _, arg := range c.Args {
		validateNoSecretKey("ARG", arg.Key, c.Location(), opt.lint)
		_, hasValue := opt.buildArgValues[arg.Key]
		hasDefault := arg.Value != nil

		skipArgInfo := false // skip the arg info if the arg is inherited from global scope
		if !hasDefault && !hasValue {
			for _, ma := range opt.metaArgs {
				if ma.Key == arg.Key {
					arg.Value = ma.Value
					skipArgInfo = true
					hasDefault = false
				}
			}
		}

		if hasValue {
			v := opt.buildArgValues[arg.Key]
			arg.Value = &v
		} else if hasDefault {
			env := getEnv(d.state)
			v, unmatched, err := opt.shlex.ProcessWord(*arg.Value, env)
			reportUnmatchedVariables(c, d.buildArgs, env, unmatched, opt)
			if err != nil {
				return err
			}
			arg.Value = &v
		}

		ai := argInfo{definition: arg, location: c.Location()}
		if arg.Value != nil {
			d.state = d.state.AddEnv(arg.Key, *arg.Value)
			ai.value = *arg.Value
		}

		if !skipArgInfo {
			d.outline.allArgs[arg.Key] = ai
		}
		d.outline.usedArgs[arg.Key] = struct{}{}

		d.buildArgs = append(d.buildArgs, arg)

		commitStr := arg.Key
		if arg.Value != nil {
			commitStr += "=" + *arg.Value
		}
		commitStrs = append(commitStrs, commitStr)
	}
	return commitToHistory(&d.image, "ARG "+strings.Join(commitStrs, " "), false, nil, d.epoch)
}

func dispatchCat(d *dispatchState, c *instructions.CatCommand, opt *dispatchOpt) error {
	dest, err := pathRelativeToWorkingDir(d.state, c.SourcesAndDest.DestPath, *d.platform)
	if err != nil {
		return err
	}

	commitMessage := bytes.NewBufferString("CAT")

	platform := opt.targetPlatform
	if d.platform != nil {
		platform = *d.platform
	}

	env := getEnv(d.state)
	name := uppercaseCmd(processCmdEnv(opt.shlex, c.String(), env))
	pgName := prefixCommand(d, name, d.prefixPlatform, &platform, env)

	var a *llb.FileAction

	for _, src := range c.SourceContents {
		commitMessage.WriteString(" <<" + src.Path)

		data := src.Data
		f, err := system.CheckSystemDriveAndRemoveDriveLetter(src.Path, d.platform.OS)
		if err != nil {
			return errors.Wrap(err, "removing drive letter")
		}
		st := llb.Scratch().File(
			llb.Mkfile(f, 0644, []byte(data)),
			ggufpackerui.WithInternalName("preparing inline document"),
			llb.Platform(*d.platform),
		)

		opts := []llb.CopyOption{&llb.CopyInfo{
			CreateDestPath: true,
		}}

		if a == nil {
			a = llb.Copy(st, system.ToSlash(f, d.platform.OS), dest, opts...)
		} else {
			a = a.Copy(st, filepath.ToSlash(f), dest, opts...)
		}
	}

	commitMessage.WriteString(" " + c.DestPath)

	fileOpt := []llb.ConstraintsOpt{
		llb.WithCustomName(pgName),
		Location(opt.sourceMap, c.Location()),
	}
	if d.ignoreCache {
		fileOpt = append(fileOpt, llb.IgnoreCache)
	}

	d.state = d.state.File(a, fileOpt...)

	return commitToHistory(&d.image, commitMessage.String(), true, &d.state, d.epoch)
}

func dispatchCmd(d *dispatchState, c *instructions.CmdCommand, lint *linter.Linter) error {
	validateUsedOnce(c, &d.cmd, lint)

	if c.Model == nil {
		return errors.New("command must point out the main model")
	}

	return commitToHistory(&d.image, fmt.Sprintf("CMD %q", c.Args), false, nil, d.epoch)
}

type copyConfig struct {
	params          instructions.SourcesAndDest
	excludePatterns []string
	source          llb.State
	isAddCommand    bool
	cmdToPrint      fmt.Stringer
	chown           string
	chmod           string
	link            bool
	keepGitDir      bool
	checksum        digest.Digest
	parents         bool
	location        []parser.Range
	ignoreMatcher   *patternmatcher.PatternMatcher
	opt             dispatchOpt
}

func dispatchCopy(d *dispatchState, cfg copyConfig) error {
	dest, err := pathRelativeToWorkingDir(d.state, cfg.params.DestPath, *d.platform)
	if err != nil {
		return err
	}

	var copyOpt []llb.CopyOption

	if cfg.chown != "" {
		copyOpt = append(copyOpt, llb.WithUser(cfg.chown))
	}

	if len(cfg.excludePatterns) > 0 {
		// in theory we don't need to check whether there are any exclude patterns,
		// as an empty list is a no-op. However, performing the check makes
		// the code easier to understand and costs virtually nothing.
		copyOpt = append(copyOpt, llb.WithExcludePatterns(cfg.excludePatterns))
	}

	var mode *os.FileMode
	if cfg.chmod != "" {
		p, err := strconv.ParseUint(cfg.chmod, 8, 32)
		if err == nil {
			perm := os.FileMode(p)
			mode = &perm
		}
	}

	if cfg.checksum != "" {
		if !cfg.isAddCommand {
			return errors.New("checksum can't be specified for COPY")
		}
		if len(cfg.params.SourcePaths) != 1 {
			return errors.New("checksum can't be specified for multiple sources")
		}
		if !isHTTPSource(cfg.params.SourcePaths[0]) {
			return errors.New("checksum can't be specified for non-HTTP(S) sources")
		}
	}

	commitMessage := bytes.NewBufferString("")
	if cfg.isAddCommand {
		commitMessage.WriteString("ADD")
	} else {
		commitMessage.WriteString("COPY")
	}

	if cfg.parents {
		commitMessage.WriteString(" " + "--parents")
	}
	if cfg.chown != "" {
		commitMessage.WriteString(" " + "--chown=" + cfg.chown)
	}
	if cfg.chmod != "" {
		commitMessage.WriteString(" " + "--chmod=" + cfg.chmod)
	}

	platform := cfg.opt.targetPlatform
	if d.platform != nil {
		platform = *d.platform
	}

	env := getEnv(d.state)
	name := uppercaseCmd(processCmdEnv(cfg.opt.shlex, cfg.cmdToPrint.String(), env))
	pgName := prefixCommand(d, name, d.prefixPlatform, &platform, env)

	var a *llb.FileAction

	for _, src := range cfg.params.SourcePaths {
		commitMessage.WriteString(" " + src)
		gitRef, gitRefErr := gitutil.ParseGitRef(src)
		if gitRefErr == nil && !gitRef.IndistinguishableFromLocal {
			if !cfg.isAddCommand {
				return errors.New("source can't be a git ref for COPY")
			}
			// TODO: print a warning (not an error) if gitRef.UnencryptedTCP is true
			commit := gitRef.Commit
			if gitRef.SubDir != "" {
				commit += ":" + gitRef.SubDir
			}
			gitOptions := []llb.GitOption{llb.WithCustomName(pgName)}
			if cfg.keepGitDir {
				gitOptions = append(gitOptions, llb.KeepGitDir())
			}
			st := llb.Git(gitRef.Remote, commit, gitOptions...)
			opts := append([]llb.CopyOption{&llb.CopyInfo{
				Mode:           mode,
				CreateDestPath: true,
			}}, copyOpt...)
			if a == nil {
				a = llb.Copy(st, "/", dest, opts...)
			} else {
				a = a.Copy(st, "/", dest, opts...)
			}
		} else if isHTTPSource(src) {
			if !cfg.isAddCommand {
				return errors.New("source can't be a URL for COPY")
			}

			// Resources from remote URLs are not decompressed.
			// https://docs.docker.com/engine/reference/builder/#add
			//
			// Note: mixing up remote archives and local archives in a single ADD instruction
			// would result in undefined behavior: https://github.com/moby/buildkit/pull/387#discussion_r189494717
			u, err := url.Parse(src)
			f := "__unnamed__"
			if err == nil {
				if base := path.Base(u.Path); base != "." && base != "/" {
					f = base
				}
			}

			st := llb.HTTP(src, llb.Filename(f), llb.WithCustomName(pgName), llb.Checksum(cfg.checksum), dfCmd(cfg.params))

			opts := append([]llb.CopyOption{&llb.CopyInfo{
				Mode:           mode,
				CreateDestPath: true,
			}}, copyOpt...)

			if a == nil {
				a = llb.Copy(st, f, dest, opts...)
			} else {
				a = a.Copy(st, f, dest, opts...)
			}
		} else {
			_ = validateCopySourcePath(src, &cfg)
			var patterns []string
			if cfg.parents {
				// detect optional pivot point
				parent, pattern, ok := strings.Cut(src, "/./")
				if !ok {
					pattern = src
					src = "/"
				} else {
					src = parent
				}

				pattern, err = system.NormalizePath("/", pattern, d.platform.OS, false)
				if err != nil {
					return errors.Wrap(err, "removing drive letter")
				}

				patterns = []string{strings.TrimPrefix(pattern, "/")}
			}

			src, err = system.NormalizePath("/", src, d.platform.OS, false)
			if err != nil {
				return errors.Wrap(err, "removing drive letter")
			}

			opts := append([]llb.CopyOption{&llb.CopyInfo{
				Mode:                mode,
				FollowSymlinks:      true,
				CopyDirContentsOnly: true,
				IncludePatterns:     patterns,
				AttemptUnpack:       cfg.isAddCommand,
				CreateDestPath:      true,
				AllowWildcard:       true,
				AllowEmptyWildcard:  true,
			}}, copyOpt...)

			if a == nil {
				a = llb.Copy(cfg.source, src, dest, opts...)
			} else {
				a = a.Copy(cfg.source, src, dest, opts...)
			}
		}
	}

	for _, src := range cfg.params.SourceContents {
		commitMessage.WriteString(" <<" + src.Path)

		data := src.Data
		f, err := system.CheckSystemDriveAndRemoveDriveLetter(src.Path, d.platform.OS)
		if err != nil {
			return errors.Wrap(err, "removing drive letter")
		}
		st := llb.Scratch().File(
			llb.Mkfile(f, 0644, []byte(data)),
			ggufpackerui.WithInternalName("preparing inline document"),
			llb.Platform(*d.platform),
		)

		opts := append([]llb.CopyOption{&llb.CopyInfo{
			Mode:           mode,
			CreateDestPath: true,
		}}, copyOpt...)

		if a == nil {
			a = llb.Copy(st, system.ToSlash(f, d.platform.OS), dest, opts...)
		} else {
			a = a.Copy(st, filepath.ToSlash(f), dest, opts...)
		}
	}

	commitMessage.WriteString(" " + cfg.params.DestPath)

	fileOpt := []llb.ConstraintsOpt{
		llb.WithCustomName(pgName),
		Location(cfg.opt.sourceMap, cfg.location),
	}
	if d.ignoreCache {
		fileOpt = append(fileOpt, llb.IgnoreCache)
	}

	// cfg.opt.llbCaps can be nil in unit tests
	if cfg.opt.llbCaps != nil && cfg.opt.llbCaps.Supports(pb.CapMergeOp) == nil && cfg.link && cfg.chmod == "" {
		pgID := identity.NewID()
		d.cmdIndex-- // prefixCommand increases it
		pgName := prefixCommand(d, name, d.prefixPlatform, &platform, env)

		copyOpts := []llb.ConstraintsOpt{
			llb.Platform(*d.platform),
		}
		copyOpts = append(copyOpts, fileOpt...)
		copyOpts = append(copyOpts, llb.ProgressGroup(pgID, pgName, true))

		mergeOpts := append([]llb.ConstraintsOpt{}, fileOpt...)
		d.cmdIndex--
		mergeOpts = append(mergeOpts, llb.ProgressGroup(pgID, pgName, false), llb.WithCustomName(prefixCommand(d, "LINK "+name, d.prefixPlatform, &platform, env)))

		d.state = d.state.WithOutput(llb.Merge([]llb.State{d.state, llb.Scratch().File(a, copyOpts...)}, mergeOpts...).Output())
	} else {
		d.state = d.state.File(a, fileOpt...)
	}

	return commitToHistory(&d.image, commitMessage.String(), true, &d.state, d.epoch)
}

func dispatchConvert(d *dispatchState, c *instructions.ConvertCommand, opt *dispatchOpt, sources []llb.State) (err error) {
	classes := []string{
		"model",
		"lora",
		"adapter",
	}
	// Extract from https://github.com/ggerganov/llama.cpp/blob/01245f5b1629075543bc4478418c7d72a0b4b3c7/convert_hf_to_gguf.py#L3553-L3556.
	types := []string{
		"F32",
		"F16",
		"BF16",
		"Q8_0",
	}

	commitMessage := bytes.NewBufferString("CONVERT")

	commitMessage.WriteString(" --class=" + c.Class)
	if !slices.Contains(classes, c.Class) {
		return errors.Errorf("invalid class %q", c.Class)
	}
	commitMessage.WriteString(" --type=" + c.Type)
	if !slices.Contains(types, c.Type) {
		return errors.Errorf("invalid type %q", c.Type)
	}

	platform := opt.targetPlatform
	if d.platform != nil {
		platform = *d.platform
	}

	env := getEnv(d.state)
	name := uppercaseCmd(processCmdEnv(opt.shlex, c.String(), env))
	pgName := prefixCommand(d, name, d.prefixPlatform, &platform, env)

	var base string
	if c.Class == "lora" {
		base = c.BaseModel
		if base == "" {
			return errors.New("base model must be specified for class lora")
		}
		base, err = system.NormalizePath("/", base, d.platform.OS, false)
		if err != nil {
			return errors.Wrap(err, "removing drive letter")
		}
		commitMessage.WriteString(" --base=" + base)
	}
	src := c.SourcePaths[0]
	{
		commitMessage.WriteString(" " + src)
		src, err = system.NormalizePath("/", src, d.platform.OS, false)
		if err != nil {
			return errors.Wrap(err, "removing drive letter")
		}
	}

	dest := c.DestPath
	{
		commitMessage.WriteString(" " + dest)
		dest, err = pathRelativeToWorkingDir(d.state, dest, *d.platform)
		if err != nil {
			return err
		}
	}

	runArgs := []string{
		"/app/convert_hf_to_gguf.py",
		path.Join("/run/src", src),
		"--outtype",
		strings.ToLower(c.Type),
		"--outfile",
		path.Join("/run/dest", dest),
	}
	if base != "" {
		runArgs[0] = "/app/convert_lora_to_gguf.py"
		runArgs = append(runArgs, "--base", path.Join("/run/extra", base))
	}
	st := d.state
	if len(sources) > 1 {
		st = sources[0]
	}
	runOpt := []llb.RunOption{
		llb.WithCustomName(pgName),
		Location(opt.sourceMap, c.Location()),
		llb.Args(runArgs),
		llb.AddMount("/run/src", st, llb.Readonly),
		llb.AddMount("/tmp", llb.Scratch(), llb.Tmpfs()),
	}
	if base != "" {
		runOpt = append(runOpt, llb.AddMount("/run/extra", d.state, llb.Readonly))
	}
	if d.ignoreCache {
		runOpt = append(runOpt, llb.IgnoreCache)
	}
	run := sources[len(sources)-1].Run(runOpt...)
	d.state = run.AddMount("/run/dest", d.state)

	return commitToHistory(&d.image, commitMessage.String(), true, &d.state, d.epoch)
}

func dispatchLabel(d *dispatchState, c *instructions.LabelCommand, lint *linter.Linter) error {
	commitMessage := bytes.NewBufferString("LABEL")
	if d.image.Config.Labels == nil {
		d.image.Config.Labels = make(map[string]string, len(c.Labels))
	}
	for _, v := range c.Labels {
		if v.NoDelim {
			msg := linter.RuleLegacyKeyValueFormat.Format(c.Name())
			lint.Run(&linter.RuleLegacyKeyValueFormat, c.Location(), msg)
		}
		d.image.Config.Labels[v.Key] = v.Value
		commitMessage.WriteString(" " + v.String())
	}
	return commitToHistory(&d.image, commitMessage.String(), false, nil, d.epoch)
}

func dispatchQuantize(d *dispatchState, c *instructions.QuantizeCommand, opt *dispatchOpt, sources []llb.State) (err error) {
	// Extract from https://github.com/ggerganov/llama.cpp/blob/c887d8b01726b11ea03dbcaa9d44fa74422d0076/examples/quantize/quantize.cpp#L19-L51.
	types := []string{
		"Q4_0",
		"Q4_1",
		"Q5_0",
		"Q5_1",
		"IQ2_XXS",
		"IQ2_XS",
		"IQ2_S",
		"IQ2_M",
		"IQ1_S",
		"IQ1_M",
		"Q2_K",
		"Q2_K_S",
		"IQ3_XXS",
		"IQ3_S",
		"IQ3_M",
		"Q3_K",
		"IQ3_XS",
		"Q3_K_S",
		"Q3_K_M",
		"Q3_K_L",
		"IQ4_NL",
		"IQ4_XS",
		"Q4_K",
		"Q4_K_S",
		"Q4_K_M",
		"Q5_K",
		"Q5_K_S",
		"Q5_K_M",
		"Q6_K",
		"Q8_0",
		"Q4_0_4_4",
		"Q4_0_4_8",
		"Q4_0_8_8",
	}
	// Extract from https://github.com/ggerganov/llama.cpp/blob/c887d8b01726b11ea03dbcaa9d44fa74422d0076/ggml/src/ggml.c#L579-L974.
	ggmlTypes := map[string]string{
		"I8":       "i8",
		"I16":      "i16",
		"I32":      "i32",
		"I64":      "i64",
		"F64":      "f64",
		"F32":      "f32",
		"F16":      "f16",
		"Q4_0":     "q4_0",
		"Q4_1":     "q4_1",
		"Q5_0":     "q5_0",
		"Q5_1":     "q5_1",
		"Q8_0":     "q8_0",
		"Q8_1":     "q8_1",
		"Q2_K":     "q2_K",
		"Q3_K":     "q3_K",
		"Q4_K":     "q4_K",
		"Q5_K":     "q5_K",
		"Q6_K":     "q6_K",
		"IQ2_XXS":  "iq2_xxs",
		"IQ2_XS":   "iq2_xs",
		"IQ3_XXS":  "iq3_xxs",
		"IQ3_S":    "iq3_s",
		"IQ2_S":    "iq2_s",
		"IQ1_S":    "iq1_s",
		"IQ1_M":    "iq1_m",
		"IQ4_NL":   "iq4_nl",
		"IQ4_XS":   "iq4_xs",
		"Q8_K":     "q8_K",
		"BF16":     "bf16",
		"Q4_0_4_4": "q4_0_4x4",
		"Q4_0_4_8": "q4_0_4x8",
		"Q4_0_8_8": "q4_0_8x8",
	}

	commitMessage := bytes.NewBufferString("QUANTIZE")

	commitMessage.WriteString(" --type=" + c.Type)
	if !slices.Contains(types, c.Type) {
		return errors.Errorf("invalid type %q", c.Type)
	}

	if c.Imatrix == "" {
		// Extract from https://github.com/ggerganov/llama.cpp/blob/c887d8b01726b11ea03dbcaa9d44fa74422d0076/examples/quantize/quantize.cpp#L406-L415.
		if strings.HasPrefix(c.Imatrix, "I") || c.Imatrix == "Q2_K_S" {
			return errors.Errorf("imatrix is required for type %q", c.Imatrix)
		}
	} else {
		commitMessage.WriteString(" --imatrix=" + c.Imatrix)
		c.Imatrix, err = system.NormalizePath("/", c.Imatrix, d.platform.OS, false)
		if err != nil {
			return errors.Wrap(err, "removing drive letter")
		}
	}
	for i := range c.IncludeWeights {
		commitMessage.WriteString(" --include-weights=" + c.IncludeWeights[i])
	}
	for i := range c.ExcludeWeights {
		commitMessage.WriteString(" --exclude-weights=" + c.ExcludeWeights[i])
	}
	if c.LeaveOutputTensor {
		commitMessage.WriteString(" --leave-output-tensor")
	}
	if c.Pure {
		commitMessage.WriteString(" --pure")
	}
	if c.OutputTensorType != "" {
		commitMessage.WriteString(" --output-tensor-type=" + c.OutputTensorType)
		if v, ok := ggmlTypes[c.OutputTensorType]; !ok {
			return errors.Errorf("invalid output-tensor-type %q", c.OutputTensorType)
		} else {
			c.OutputTensorType = v
		}
	}
	if c.TokenEmbeddingType != "" {
		commitMessage.WriteString(" --token-embedding-type=" + c.TokenEmbeddingType)
		if v, ok := ggmlTypes[c.TokenEmbeddingType]; !ok {
			return errors.Errorf("invalid token-embedding-type %q", c.TokenEmbeddingType)
		} else {
			c.TokenEmbeddingType = v
		}
	}

	platform := opt.targetPlatform
	if d.platform != nil {
		platform = *d.platform
	}

	env := getEnv(d.state)
	name := uppercaseCmd(processCmdEnv(opt.shlex, c.String(), env))
	pgName := prefixCommand(d, name, d.prefixPlatform, &platform, env)

	src := c.SourcePaths[0]
	{
		commitMessage.WriteString(" " + src)
		src, err = system.NormalizePath("/", src, d.platform.OS, false)
		if err != nil {
			return errors.Wrap(err, "removing drive letter")
		}
	}

	dest := c.DestPath
	{
		commitMessage.WriteString(" " + dest)
		dest, err = pathRelativeToWorkingDir(d.state, dest, *d.platform)
		if err != nil {
			return err
		}
	}

	runArgs := []string{
		"/app/llama-quantize",
	}
	if c.Imatrix != "" {
		runArgs = append(runArgs, "--imatrix", path.Join("/run/extra", c.Imatrix))
	}
	for i := range c.IncludeWeights {
		runArgs = append(runArgs, "--include-weights", c.IncludeWeights[i])
	}
	for i := range c.ExcludeWeights {
		runArgs = append(runArgs, "--exclude-weights", c.ExcludeWeights[i])
	}
	if c.LeaveOutputTensor {
		runArgs = append(runArgs, "--leave-output-tensor")
	}
	if c.Pure {
		runArgs = append(runArgs, "--pure")
	}
	if c.OutputTensorType != "" {
		runArgs = append(runArgs, "--output-tensor-type", c.OutputTensorType)
	}
	if c.TokenEmbeddingType != "" {
		runArgs = append(runArgs, "--token-embedding-type", c.TokenEmbeddingType)
	}
	runArgs = append(runArgs,
		path.Join("/run/src", src),
		path.Join("/run/dest", dest),
		c.Type)
	st := d.state
	if len(sources) > 1 {
		st = sources[0]
	}
	runOpt := []llb.RunOption{
		llb.WithCustomName(pgName),
		Location(opt.sourceMap, c.Location()),
		llb.Args(runArgs),
		llb.AddMount("/run/src", st, llb.Readonly),
		llb.AddMount("/tmp", llb.Scratch(), llb.Tmpfs()),
	}
	if c.Imatrix != "" {
		runOpt = append(runOpt, llb.AddMount("/run/extra", d.state, llb.Readonly))
	}
	if d.ignoreCache {
		runOpt = append(runOpt, llb.IgnoreCache)
	}
	run := sources[len(sources)-1].Run(runOpt...)
	d.state = run.AddMount("/run/dest", d.state)

	return commitToHistory(&d.image, commitMessage.String(), true, &d.state, d.epoch)
}

func pathRelativeToWorkingDir(s llb.State, p string, platform specs.Platform) (string, error) {
	dir, err := s.GetDir(context.TODO(), llb.Platform(platform))
	if err != nil {
		return "", err
	}

	p, err = system.CheckSystemDriveAndRemoveDriveLetter(p, platform.OS)
	if err != nil {
		return "", errors.Wrap(err, "removing drive letter")
	}

	if system.IsAbs(p, platform.OS) {
		return system.NormalizePath("/", p, platform.OS, true)
	}

	// add slashes for "" and "." paths
	// "" is treated as current directory and not necessariy root
	if p == "." || p == "" {
		p = "./"
	}
	return system.NormalizePath(dir, p, platform.OS, true)
}

func setKVValue(kvpo instructions.KeyValuePairOptional, values map[string]string) instructions.KeyValuePairOptional {
	if v, ok := values[kvpo.Key]; ok {
		kvpo.Value = &v
	}
	return kvpo
}

func dfCmd(cmd interface{}) llb.ConstraintsOpt {
	// TODO: add fmt.Stringer to instructions.Command to remove interface{}
	var cmdStr string
	if cmd, ok := cmd.(fmt.Stringer); ok {
		cmdStr = cmd.String()
	}
	if cmd, ok := cmd.(string); ok {
		cmdStr = cmd
	}
	return llb.WithDescription(map[string]string{
		"com.docker.ggufpackerfile.v1.command": cmdStr,
	})
}

func commitToHistory(img *specs.Image, msg string, withLayer bool, st *llb.State, tm *time.Time) error {
	if st != nil {
		msg += " # buildkit"
	}

	img.History = append(img.History, specs.History{
		CreatedBy:  msg,
		Comment:    historyComment,
		EmptyLayer: !withLayer,
		Created:    tm,
	})
	return nil
}

func allReachableStages(s *dispatchState) map[*dispatchState]struct{} {
	stages := make(map[*dispatchState]struct{})
	addReachableStages(s, stages)
	return stages
}

func addReachableStages(s *dispatchState, stages map[*dispatchState]struct{}) {
	if _, ok := stages[s]; ok {
		return
	}
	stages[s] = struct{}{}
	if s.base != nil {
		addReachableStages(s.base, stages)
	}
	for d := range s.deps {
		addReachableStages(d, stages)
	}
}

func validateCopySourcePath(src string, cfg *copyConfig) error {
	if cfg.ignoreMatcher == nil {
		return nil
	}
	cmd := "Copy"
	if cfg.isAddCommand {
		cmd = "Add"
	}

	ok, err := cfg.ignoreMatcher.MatchesOrParentMatches(src)
	if err != nil {
		return err
	}
	if ok {
		msg := linter.RuleCopyIgnoredFile.Format(cmd, src)
		cfg.opt.lint.Run(&linter.RuleCopyIgnoredFile, cfg.location, msg)
	}

	return nil
}

func validateCircularDependency(states []*dispatchState) error {
	var visit func(*dispatchState, []instructions.Command) []instructions.Command
	if states == nil {
		return nil
	}
	visited := make(map[*dispatchState]struct{})
	paths := make(map[*dispatchState]struct{})

	visit = func(state *dispatchState, current []instructions.Command) []instructions.Command {
		_, ok := visited[state]
		if ok {
			return nil
		}
		visited[state] = struct{}{}
		paths[state] = struct{}{}
		for dep, c := range state.deps {
			next := append(current, c)
			if _, ok := paths[dep]; ok {
				return next
			}
			if c := visit(dep, next); c != nil {
				return c
			}
		}
		delete(paths, state)
		return nil
	}
	for _, state := range states {
		if cmds := visit(state, nil); cmds != nil {
			err := errors.Errorf("circular dependency detected on stage: %s", state.stageName)
			for _, c := range cmds {
				err = parser.WithLocation(err, c.Location())
			}
			return err
		}
	}
	return nil
}

func normalizeContextPaths(paths map[string]struct{}) []string {
	// Avoid a useless allocation if the set of paths is empty.
	if len(paths) == 0 {
		return nil
	}

	pathSlice := make([]string, 0, len(paths))
	for p := range paths {
		if p == "/" {
			return nil
		}
		pathSlice = append(pathSlice, path.Join(".", p))
	}

	sort.Slice(pathSlice, func(i, j int) bool {
		return pathSlice[i] < pathSlice[j]
	})
	return pathSlice
}

// filterPaths returns the local options required to filter an llb.Local
// to only the required paths.
func filterPaths(paths map[string]struct{}) []llb.LocalOption {
	if includePaths := normalizeContextPaths(paths); len(includePaths) > 0 {
		return []llb.LocalOption{llb.FollowPaths(includePaths)}
	}
	return nil
}

func proxyEnvFromBuildArgs(args map[string]string) *llb.ProxyEnv {
	pe := &llb.ProxyEnv{}
	isNil := true
	for k, v := range args {
		if strings.EqualFold(k, "http_proxy") {
			pe.HTTPProxy = v
			isNil = false
		}
		if strings.EqualFold(k, "https_proxy") {
			pe.HTTPSProxy = v
			isNil = false
		}
		if strings.EqualFold(k, "ftp_proxy") {
			pe.FTPProxy = v
			isNil = false
		}
		if strings.EqualFold(k, "no_proxy") {
			pe.NoProxy = v
			isNil = false
		}
		if strings.EqualFold(k, "all_proxy") {
			pe.AllProxy = v
			isNil = false
		}
	}
	if isNil {
		return nil
	}
	return pe
}

type mutableOutput struct {
	llb.Output
}

func autoDetectPlatform(img specs.Image, target specs.Platform, supported []specs.Platform) specs.Platform {
	arch := img.Architecture
	if target.OS == img.OS && target.Architecture == arch {
		return target
	}
	for _, p := range supported {
		if p.OS == img.OS && p.Architecture == arch {
			return p
		}
	}
	return target
}

func uppercaseCmd(str string) string {
	p := strings.SplitN(str, " ", 2)
	p[0] = strings.ToUpper(p[0])
	return strings.Join(p, " ")
}

func processCmdEnv(shlex *shell.Lex, cmd string, env shell.EnvGetter) string {
	w, _, err := shlex.ProcessWord(cmd, env)
	if err != nil {
		return cmd
	}
	return w
}

func prefixCommand(ds *dispatchState, str string, prefixPlatform bool, platform *specs.Platform, env shell.EnvGetter) string {
	if ds.cmdTotal == 0 {
		return str
	}
	out := "["
	if prefixPlatform && platform != nil {
		out += platforms.Format(*platform) + formatTargetPlatform(*platform, platformFromEnv(env)) + " "
	}
	if ds.stageName != "" {
		out += ds.stageName + " "
	}
	ds.cmdIndex++
	out += fmt.Sprintf("%*d/%d] ", int(1+math.Log10(float64(ds.cmdTotal))), ds.cmdIndex, ds.cmdTotal)
	return out + str
}

// formatTargetPlatform formats a secondary platform string for cross compilation cases
func formatTargetPlatform(base specs.Platform, target *specs.Platform) string {
	if target == nil {
		return ""
	}
	if target.OS == "" {
		target.OS = base.OS
	}
	if target.Architecture == "" {
		target.Architecture = base.Architecture
	}
	p := platforms.Normalize(*target)

	if p.OS == base.OS && p.Architecture != base.Architecture {
		archVariant := p.Architecture
		if p.Variant != "" {
			archVariant += "/" + p.Variant
		}
		return "->" + archVariant
	}
	if p.OS != base.OS {
		return "->" + platforms.Format(p)
	}
	return ""
}

// platformFromEnv returns defined platforms based on TARGET* environment variables
func platformFromEnv(env shell.EnvGetter) *specs.Platform {
	var p specs.Platform
	var set bool
	for _, key := range env.Keys() {
		switch key {
		case "TARGETPLATFORM":
			v, _ := env.Get(key)
			p, err := platforms.Parse(v)
			if err != nil {
				continue
			}
			return &p
		case "TARGETOS":
			p.OS, _ = env.Get(key)
			set = true
		case "TARGETARCH":
			p.Architecture, _ = env.Get(key)
			set = true
		case "TARGETVARIANT":
			p.Variant, _ = env.Get(key)
			set = true
		}
	}
	if !set {
		return nil
	}
	return &p
}

func Location(sm *llb.SourceMap, locations []parser.Range) llb.ConstraintsOpt {
	loc := make([]*pb.Range, 0, len(locations))
	for _, l := range locations {
		loc = append(loc, &pb.Range{
			Start: pb.Position{
				Line:      int32(l.Start.Line),
				Character: int32(l.Start.Character),
			},
			End: pb.Position{
				Line:      int32(l.End.Line),
				Character: int32(l.End.Character),
			},
		})
	}
	return sm.Location(loc)
}

func isHTTPSource(src string) bool {
	if !strings.HasPrefix(src, "http://") && !strings.HasPrefix(src, "https://") {
		return false
	}
	// https://github.com/ORG/REPO.git is a git source, not an http source
	if gitRef, gitErr := gitutil.ParseGitRef(src); gitRef != nil && gitErr == nil {
		return false
	}
	return true
}

func isSelfConsistentCasing(s string) bool {
	return s == strings.ToLower(s) || s == strings.ToUpper(s)
}

func validateCommandCasing(dockerfile *parser.Result, lint *linter.Linter) {
	var lowerCount, upperCount int
	for _, node := range dockerfile.AST.Children {
		if isSelfConsistentCasing(node.Value) {
			if strings.ToLower(node.Value) == node.Value {
				lowerCount++
			} else {
				upperCount++
			}
		}
	}

	isMajorityLower := lowerCount > upperCount
	for _, node := range dockerfile.AST.Children {
		// Here, we check both if the command is consistent per command (ie, "CMD" or "cmd", not "Cmd")
		// as well as ensuring that the casing is consistent throughout the ggufpackerfile by comparing the
		// command to the casing of the majority of commands.
		var correctCasing string
		if isMajorityLower && strings.ToLower(node.Value) != node.Value {
			correctCasing = "lowercase"
		} else if !isMajorityLower && strings.ToUpper(node.Value) != node.Value {
			correctCasing = "uppercase"
		}
		if correctCasing != "" {
			msg := linter.RuleConsistentInstructionCasing.Format(node.Value, correctCasing)
			lint.Run(&linter.RuleConsistentInstructionCasing, node.Location(), msg)
		}
	}
}

var reservedStageNames = map[string]struct{}{
	"context": {},
	"scratch": {},
}

func validateStageNames(stages []instructions.Stage, lint *linter.Linter) {
	stageNames := make(map[string]struct{})
	for _, stage := range stages {
		if stage.Name != "" {
			if _, ok := reservedStageNames[stage.Name]; ok {
				msg := linter.RuleReservedStageName.Format(stage.Name)
				lint.Run(&linter.RuleReservedStageName, stage.Location, msg)
			}

			if _, ok := stageNames[stage.Name]; ok {
				msg := linter.RuleDuplicateStageName.Format(stage.Name)
				lint.Run(&linter.RuleDuplicateStageName, stage.Location, msg)
			}
			stageNames[stage.Name] = struct{}{}
		}
	}
}

func reportUnmatchedVariables(cmd instructions.Command, buildArgs []instructions.KeyValuePairOptional, env shell.EnvGetter, unmatched map[string]struct{}, opt *dispatchOpt) {
	if len(unmatched) == 0 {
		return
	}
	for _, buildArg := range buildArgs {
		delete(unmatched, buildArg.Key)
	}
	if len(unmatched) == 0 {
		return
	}
	options := metaArgsKeys(opt.metaArgs)
	options = append(options, env.Keys()...)
	for cmdVar := range unmatched {
		match, _ := suggest.Search(cmdVar, options, runtime.GOOS != "windows")
		msg := linter.RuleUndefinedVar.Format(cmdVar, match)
		opt.lint.Run(&linter.RuleUndefinedVar, cmd.Location(), msg)
	}
}

func mergeLocations(locations ...[]parser.Range) []parser.Range {
	allRanges := []parser.Range{}
	for _, ranges := range locations {
		allRanges = append(allRanges, ranges...)
	}
	if len(allRanges) == 0 {
		return []parser.Range{}
	}
	if len(allRanges) == 1 {
		return allRanges
	}

	sort.Slice(allRanges, func(i, j int) bool {
		return allRanges[i].Start.Line < allRanges[j].Start.Line
	})

	location := []parser.Range{}
	currentRange := allRanges[0]
	for _, r := range allRanges[1:] {
		if r.Start.Line <= currentRange.End.Line {
			currentRange.End.Line = max(currentRange.End.Line, r.End.Line)
		} else {
			location = append(location, currentRange)
			currentRange = r
		}
	}
	location = append(location, currentRange)
	return location
}

func toPBLocation(sourceIndex int, location []parser.Range) pb.Location {
	loc := make([]*pb.Range, 0, len(location))
	for _, l := range location {
		loc = append(loc, &pb.Range{
			Start: pb.Position{
				Line:      int32(l.Start.Line),
				Character: int32(l.Start.Character),
			},
			End: pb.Position{
				Line:      int32(l.End.Line),
				Character: int32(l.End.Character),
			},
		})
	}
	return pb.Location{
		SourceIndex: int32(sourceIndex),
		Ranges:      loc,
	}
}

func reportUnusedFromArgs(values []string, unmatched map[string]struct{}, location []parser.Range, lint *linter.Linter) {
	for arg := range unmatched {
		sg, _ := suggest.Search(arg, values, true)
		msg := linter.RuleUndefinedArgInFrom.Format(arg, sg)
		lint.Run(&linter.RuleUndefinedArgInFrom, location, msg)
	}
}

type instructionTracker struct {
	Loc   []parser.Range
	IsSet bool
}

func (v *instructionTracker) MarkUsed(loc []parser.Range) {
	v.Loc = loc
	v.IsSet = true
}

func validateUsedOnce(c instructions.Command, loc *instructionTracker, lint *linter.Linter) {
	if loc.IsSet {
		msg := linter.RuleMultipleInstructionsDisallowed.Format(c.Name())
		// Report the location of the previous invocation because it is the one
		// that will be ignored.
		lint.Run(&linter.RuleMultipleInstructionsDisallowed, loc.Loc, msg)
	}
	loc.MarkUsed(c.Location())
}

func validateBaseImagePlatform(name string, expected, actual specs.Platform, location []parser.Range, lint *linter.Linter) {
	if expected.OS != actual.OS || expected.Architecture != actual.Architecture {
		expectedStr := platforms.Format(platforms.Normalize(expected))
		actualStr := platforms.Format(platforms.Normalize(actual))
		msg := linter.RuleInvalidBaseImagePlatform.Format(name, expectedStr, actualStr)
		lint.Run(&linter.RuleInvalidBaseImagePlatform, location, msg)
	}
}

func getSecretsRegex() *regexp.Regexp {
	// Check for either full value or first/last word.
	// Examples: api_key, DATABASE_PASSWORD, GITHUB_TOKEN, secret_MESSAGE, AUTH
	// Case insensitive.
	secretsRegexpOnce.Do(func() {
		secretTokens := []string{
			"apikey",
			"auth",
			"credential",
			"credentials",
			"key",
			"password",
			"pword",
			"passwd",
			"secret",
			"token",
		}
		pattern := `(?i)(?:_|^)(?:` + strings.Join(secretTokens, "|") + `)(?:_|$)`
		secretsRegexp = regexp.MustCompile(pattern)
	})
	return secretsRegexp
}

func validateNoSecretKey(instruction, key string, location []parser.Range, lint *linter.Linter) {
	pattern := getSecretsRegex()
	if pattern.MatchString(key) {
		msg := linter.RuleSecretsUsedInArgOrEnv.Format(instruction, key)
		lint.Run(&linter.RuleSecretsUsedInArgOrEnv, location, msg)
	}
}

func validateBaseImagesWithDefaultArgs(stages []instructions.Stage, shlex *shell.Lex, metaArgs []instructions.ArgCommand, optMetaArgs []instructions.KeyValuePairOptional, lint *linter.Linter) {
	// Build the arguments as if no build options were given
	// and using only defaults.
	optMetaArgs, _, err := buildMetaArgs(optMetaArgs, shlex, metaArgs, nil)
	if err != nil {
		// Abandon running the linter. We'll likely fail after this point
		// with the same error but we shouldn't error here inside
		// of the linting check.
		return
	}

	for _, st := range stages {
		nameMatch, err := shlex.ProcessWordWithMatches(st.BaseName, metaArgsToEnvs(optMetaArgs))
		if err != nil {
			return
		}

		// Verify the image spec is potentially valid.
		if _, err := reference.ParseNormalizedNamed(nameMatch.Result); err != nil {
			msg := linter.RuleInvalidDefaultArgInFrom.Format(st.BaseName)
			lint.Run(&linter.RuleInvalidDefaultArgInFrom, st.Location, msg)
		}
	}
}

func buildMetaArgs(metaArgs []instructions.KeyValuePairOptional, shlex *shell.Lex, argCommands []instructions.ArgCommand, buildArgs map[string]string) ([]instructions.KeyValuePairOptional, map[string]argInfo, error) {
	allArgs := make(map[string]argInfo)

	for _, cmd := range argCommands {
		for _, metaArg := range cmd.Args {
			info := argInfo{definition: metaArg, location: cmd.Location()}
			if v, ok := buildArgs[metaArg.Key]; !ok {
				if metaArg.Value != nil {
					result, err := shlex.ProcessWordWithMatches(*metaArg.Value, metaArgsToEnvs(metaArgs))
					if err != nil {
						return nil, nil, parser.WithLocation(err, cmd.Location())
					}
					metaArg.Value = &result.Result
					info.deps = result.Matched
				}
			} else {
				metaArg.Value = &v
			}
			metaArgs = append(metaArgs, metaArg)
			if metaArg.Value != nil {
				info.value = *metaArg.Value
			}
			allArgs[metaArg.Key] = info
		}
	}
	return metaArgs, allArgs, nil
}

type emptyEnvs struct{}

func (emptyEnvs) Get(string) (string, bool) {
	return "", false
}

func (emptyEnvs) Keys() []string {
	return nil
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
