package ggufpackerui

import (
	"bytes"
	"context"
	"encoding/json"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/containerd/platforms"
	"github.com/distribution/reference"
	controlapi "github.com/moby/buildkit/api/services/control"
	"github.com/moby/buildkit/client/llb"
	"github.com/moby/buildkit/frontend/gateway/client"
	"github.com/moby/buildkit/solver/pb"
	"github.com/moby/buildkit/util/flightcontrol"
	"github.com/moby/patternmatcher/ignorefile"
	"github.com/opencontainers/go-digest"
	"github.com/pkg/errors"

	"github.com/gpustack/gguf-packer-go/buildkit/frontend/ggufpackerfile/linter"
	specs "github.com/gpustack/gguf-packer-go/buildkit/frontend/specs/v1"
)

const (
	buildArgPrefix = "build-arg:"
	labelPrefix    = "label:"

	keyTarget           = "target"
	keyCgroupParent     = "cgroup-parent"
	keyForceNetwork     = "force-network-mode"
	keyGlobalAddHosts   = "add-hosts"
	keyImageResolveMode = "image-resolve-mode"
	keyMultiPlatform    = "multi-platform"
	keyNoCache          = "no-cache"
	keyShmSize          = "shm-size"
	keyTargetPlatform   = "platform"
	keyUlimit           = "ulimit"
	keyCacheFrom        = "cache-from"    // for registry only. deprecated in favor of keyCacheImports
	keyCacheImports     = "cache-imports" // JSON representation of []CacheOptionsEntry

	keyCacheNSArg            = "build-arg:BUILDKIT_CACHE_MOUNT_NS"
	keyMultiPlatformArg      = "build-arg:BUILDKIT_MULTI_PLATFORM"
	keyGGUFPackerfileLintArg = "build-arg:BUILDKIT_GGUFPACKER_CHECK"
	keyContextKeepGitDirArg  = "build-arg:BUILDKIT_CONTEXT_KEEP_GIT_DIR"
	keySourceDateEpoch       = "build-arg:SOURCE_DATE_EPOCH"

	keyGGUFPackerConvertImageArg  = "build-arg:BUILDKIT_GGUFPACKER_CONVERT_IMAGE"
	keyGGUFPackerQuantizeImageArg = "build-arg:BUILDKIT_GGUFPACKER_QUANTIZE_IMAGE"
	keyGGUFPackerParseImageArg    = "build-arg:BUILDKIT_GGUFPACKER_PARSE_IMAGE"
)

const (
	DefaultImage = "docker.io/gpustack/gguf-packer:latest"
)

type Config struct {
	BuildArgs        map[string]string
	CacheIDNamespace string
	CgroupParent     string
	Epoch            *time.Time
	ExtraHosts       []llb.HostIP
	ImageResolveMode llb.ResolveMode
	Labels           map[string]string
	NetworkMode      pb.NetMode
	ShmSize          int64
	Target           string
	Ulimits          []pb.Ulimit
	LinterConfig     *linter.Config

	CacheImports           []client.CacheOptionsEntry
	TargetPlatforms        []specs.Platform // nil means default
	BuildPlatforms         []specs.Platform
	MultiPlatformRequested bool

	ConvertImage  string
	QuantizeImage string
	ParseImage    string
}

type Client struct {
	Config
	client      client.Client
	ignoreCache []string
	g           flightcontrol.CachedGroup[*buildContext]
	bopts       client.BuildOpts

	ggufpackerignore     []byte
	ggufpackerignoreName string
}

type Source struct {
	*llb.SourceMap
	Warn func(context.Context, string, client.WarnOpts)
}

type ContextOpt struct {
	AsyncLocalOpts func() []llb.LocalOption
	Platform       *specs.Platform
	ResolveMode    string
	CaptureDigest  *digest.Digest
}

func validateMinCaps(c client.Client) error {
	opts := c.BuildOpts().Opts
	caps := c.BuildOpts().LLBCaps

	if err := caps.Supports(pb.CapFileBase); err != nil {
		return errors.Wrap(err, "needs BuildKit 0.5 or later")
	}
	if opts["override-copy-image"] != "" {
		return errors.New("support for \"override-copy-image\" was removed in BuildKit 0.11")
	}
	if v, ok := opts["build-arg:BUILDKIT_DISABLE_FILEOP"]; ok {
		if b, err := strconv.ParseBool(v); err == nil && b {
			return errors.New("support for \"BUILDKIT_DISABLE_FILEOP\" build-arg was removed in BuildKit 0.11")
		}
	}
	return nil
}

func NewClient(c client.Client) (*Client, error) {
	if err := validateMinCaps(c); err != nil {
		return nil, err
	}

	bc := &Client{
		client: c,
		bopts:  c.BuildOpts(), // avoid grpc on every call
	}

	if err := bc.init(); err != nil {
		return nil, err
	}

	return bc, nil
}

func (bc *Client) BuildOpts() client.BuildOpts {
	return bc.bopts
}

func (bc *Client) init() error {
	opts := bc.bopts.Opts

	defaultBuildPlatform := platforms.Normalize(platforms.DefaultSpec())
	if workers := bc.bopts.Workers; len(workers) > 0 && len(workers[0].Platforms) > 0 {
		defaultBuildPlatform = workers[0].Platforms[0]
	}
	buildPlatforms := []specs.Platform{defaultBuildPlatform}
	targetPlatforms := []specs.Platform{}
	if v := opts[keyTargetPlatform]; v != "" {
		var err error
		targetPlatforms, err = parsePlatforms(v)
		if err != nil {
			return err
		}
	}
	bc.BuildPlatforms = buildPlatforms
	bc.TargetPlatforms = targetPlatforms

	resolveMode, err := parseResolveMode(opts[keyImageResolveMode])
	if err != nil {
		return err
	}
	bc.ImageResolveMode = resolveMode

	extraHosts, err := parseExtraHosts(opts[keyGlobalAddHosts])
	if err != nil {
		return errors.Wrap(err, "failed to parse additional hosts")
	}
	bc.ExtraHosts = extraHosts

	shmSize, err := parseShmSize(opts[keyShmSize])
	if err != nil {
		return errors.Wrap(err, "failed to parse shm size")
	}
	bc.ShmSize = shmSize

	ulimits, err := parseUlimits(opts[keyUlimit])
	if err != nil {
		return errors.Wrap(err, "failed to parse ulimit")
	}
	bc.Ulimits = ulimits

	defaultNetMode, err := parseNetMode(opts[keyForceNetwork])
	if err != nil {
		return err
	}
	bc.NetworkMode = defaultNetMode

	var ignoreCache []string
	if v, ok := opts[keyNoCache]; ok {
		if v == "" {
			ignoreCache = []string{} // means all stages
		} else {
			ignoreCache = strings.Split(v, ",")
		}
	}
	bc.ignoreCache = ignoreCache

	multiPlatform := len(targetPlatforms) > 1
	if v := opts[keyMultiPlatformArg]; v != "" {
		opts[keyMultiPlatform] = v
	}
	if v := opts[keyMultiPlatform]; v != "" {
		b, err := strconv.ParseBool(v)
		if err != nil {
			return errors.Errorf("invalid boolean value for multi-platform: %s", v)
		}
		if !b && multiPlatform {
			return errors.Errorf("conflicting config: returning multiple target platforms is not allowed")
		}
		multiPlatform = b
	}
	bc.MultiPlatformRequested = multiPlatform

	var cacheImports []client.CacheOptionsEntry
	// new API
	if cacheImportsStr := opts[keyCacheImports]; cacheImportsStr != "" {
		var cacheImportsUM []controlapi.CacheOptionsEntry
		if err := json.Unmarshal([]byte(cacheImportsStr), &cacheImportsUM); err != nil {
			return errors.Wrapf(err, "failed to unmarshal %s (%q)", keyCacheImports, cacheImportsStr)
		}
		for _, um := range cacheImportsUM {
			cacheImports = append(cacheImports, client.CacheOptionsEntry{Type: um.Type, Attrs: um.Attrs})
		}
	}
	// old API
	if cacheFromStr := opts[keyCacheFrom]; cacheFromStr != "" {
		cacheFrom := strings.Split(cacheFromStr, ",")
		for _, s := range cacheFrom {
			im := client.CacheOptionsEntry{
				Type: "registry",
				Attrs: map[string]string{
					"ref": s,
				},
			}
			// FIXME(AkihiroSuda): skip append if already exists
			cacheImports = append(cacheImports, im)
		}
	}
	bc.CacheImports = cacheImports

	epoch, err := parseSourceDateEpoch(opts[keySourceDateEpoch])
	if err != nil {
		return err
	}
	bc.Epoch = epoch

	bc.BuildArgs = filter(opts, buildArgPrefix)
	bc.Labels = filter(opts, labelPrefix)
	bc.CacheIDNamespace = opts[keyCacheNSArg]
	bc.CgroupParent = opts[keyCgroupParent]
	bc.Target = opts[keyTarget]

	if v, ok := opts[keyGGUFPackerfileLintArg]; ok {
		bc.LinterConfig, err = linter.ParseLintOptions(v)
		if err != nil {
			return errors.Wrapf(err, "failed to parse %s", keyGGUFPackerfileLintArg)
		}
	}

	bc.ConvertImage = tenary(opts[keyGGUFPackerConvertImageArg] != "", opts[keyGGUFPackerConvertImageArg], DefaultImage)
	bc.QuantizeImage = tenary(opts[keyGGUFPackerQuantizeImageArg] != "", opts[keyGGUFPackerQuantizeImageArg], DefaultImage)
	bc.ParseImage = tenary(opts[keyGGUFPackerParseImageArg] != "", opts[keyGGUFPackerConvertImageArg], DefaultImage)

	return nil
}

func (bc *Client) buildContext(ctx context.Context) (*buildContext, error) {
	return bc.g.Do(ctx, "initcontext", func(ctx context.Context) (*buildContext, error) {
		return bc.initContext(ctx)
	})
}

func (bc *Client) ReadEntrypoint(ctx context.Context, lang string, opts ...llb.LocalOption) (*Source, error) {
	bctx, err := bc.buildContext(ctx)
	if err != nil {
		return nil, err
	}

	var src *llb.State

	if !bctx.forceLocalGGUFPackerfile {
		if bctx.ggufpackerfile != nil {
			src = bctx.ggufpackerfile
		}
	}

	if src == nil {
		name := "load build definition from " + bctx.filename

		filenames := []string{bctx.filename, bctx.filename + DefaultGGUFPackerignoreName}

		// ggufpackerfile is also supported casing moby/moby#10858
		if path.Base(bctx.filename) == DefaultGGUFPackerfileName {
			filenames = append(filenames, path.Join(path.Dir(bctx.filename), strings.ToLower(DefaultGGUFPackerfileName)))
		}

		opts = append([]llb.LocalOption{
			llb.FollowPaths(filenames),
			llb.SessionID(bc.bopts.SessionID),
			llb.SharedKeyHint(bctx.ggufpackerfileLocalName),
			WithInternalName(name),
			llb.Differ(llb.DiffNone, false),
		}, opts...)

		lsrc := llb.Local(bctx.ggufpackerfileLocalName, opts...)
		src = &lsrc
	}

	def, err := src.Marshal(ctx, bc.marshalOpts()...)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to marshal local source")
	}

	defVtx, err := def.Head()
	if err != nil {
		return nil, err
	}

	res, err := bc.client.Solve(ctx, client.SolveRequest{
		Definition: def.ToPB(),
	})
	if err != nil {
		return nil, errors.Wrapf(err, "failed to resolve ggufpackerfile")
	}

	ref, err := res.SingleRef()
	if err != nil {
		return nil, err
	}

	dt, err := ref.ReadFile(ctx, client.ReadRequest{
		Filename: bctx.filename,
	})
	if err != nil {
		if path.Base(bctx.filename) == DefaultGGUFPackerfileName {
			var err1 error
			dt, err1 = ref.ReadFile(ctx, client.ReadRequest{
				Filename: path.Join(path.Dir(bctx.filename), strings.ToLower(DefaultGGUFPackerfileName)),
			})
			if err1 == nil {
				err = nil
			}
		}
		if err != nil {
			return nil, errors.Wrapf(err, "failed to read ggufpackerfile")
		}
	}
	smap := llb.NewSourceMap(src, bctx.filename, lang, dt)
	smap.Definition = def

	dt, err = ref.ReadFile(ctx, client.ReadRequest{
		Filename: bctx.filename + DefaultGGUFPackerignoreName,
	})
	if err == nil {
		bc.ggufpackerignore = dt
		bc.ggufpackerignoreName = bctx.filename + DefaultGGUFPackerignoreName
	}

	return &Source{
		SourceMap: smap,
		Warn: func(ctx context.Context, msg string, opts client.WarnOpts) {
			if opts.Level == 0 {
				opts.Level = 1
			}
			if opts.SourceInfo == nil {
				opts.SourceInfo = &pb.SourceInfo{
					Data:       smap.Data,
					Filename:   smap.Filename,
					Language:   smap.Language,
					Definition: smap.Definition.ToPB(),
				}
			}
			_ = bc.client.Warn(ctx, defVtx, msg, opts)
		},
	}, nil
}

func (bc *Client) MainContext(ctx context.Context, opts ...llb.LocalOption) (*llb.State, error) {
	bctx, err := bc.buildContext(ctx)
	if err != nil {
		return nil, err
	}

	if bctx.context != nil {
		return bctx.context, nil
	}

	excludes, err := bc.ggufpackerIgnorePatterns(ctx, bctx)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to read ggufpackerignore patterns")
	}

	opts = append([]llb.LocalOption{
		llb.SessionID(bc.bopts.SessionID),
		llb.ExcludePatterns(excludes),
		llb.SharedKeyHint(bctx.contextLocalName),
		WithInternalName("load build context"),
	}, opts...)

	st := llb.Local(bctx.contextLocalName, opts...)

	return &st, nil
}

func (bc *Client) NamedContext(ctx context.Context, name string, opt ContextOpt) (*llb.State, *specs.Image, error) {
	named, err := reference.ParseNormalizedNamed(name)
	if err != nil {
		return nil, nil, errors.Wrapf(err, "invalid context name %s", name)
	}
	name = strings.TrimSuffix(reference.FamiliarString(named), ":latest")

	pp := platforms.DefaultSpec()
	if opt.Platform != nil {
		pp = *opt.Platform
	}
	pname := name + "::" + platforms.Format(platforms.Normalize(pp))
	st, img, err := bc.namedContext(ctx, name, pname, opt)
	if err != nil || st != nil {
		return st, img, err
	}
	return bc.namedContext(ctx, name, name, opt)
}

func (bc *Client) IsNoCache(name string) bool {
	if len(bc.ignoreCache) == 0 {
		return bc.ignoreCache != nil
	}
	for _, n := range bc.ignoreCache {
		if strings.EqualFold(n, name) {
			return true
		}
	}
	return false
}

func (bc *Client) GGUFPackerIgnorePatterns(ctx context.Context) ([]string, error) {
	if bc == nil {
		return nil, nil
	}
	bctx, err := bc.buildContext(ctx)
	if err != nil {
		return nil, err
	}
	if bctx.context != nil {
		return nil, nil
	}

	return bc.ggufpackerIgnorePatterns(ctx, bctx)
}

func DefaultMainContext(opts ...llb.LocalOption) *llb.State {
	opts = append([]llb.LocalOption{
		llb.SharedKeyHint(DefaultLocalNameContext),
		WithInternalName("load build context"),
	}, opts...)
	st := llb.Local(DefaultLocalNameContext, opts...)
	return &st
}

func WithInternalName(name string) llb.ConstraintsOpt {
	return llb.WithCustomName("[internal] " + name)
}

func (bc *Client) ggufpackerIgnorePatterns(ctx context.Context, bctx *buildContext) ([]string, error) {
	if bc.ggufpackerignore == nil {
		st := llb.Local(bctx.contextLocalName,
			llb.SessionID(bc.bopts.SessionID),
			llb.FollowPaths([]string{DefaultGGUFPackerignoreName}),
			llb.SharedKeyHint(bctx.contextLocalName+"-"+DefaultGGUFPackerignoreName),
			WithInternalName("load "+DefaultGGUFPackerignoreName),
			llb.Differ(llb.DiffNone, false),
		)
		def, err := st.Marshal(ctx, bc.marshalOpts()...)
		if err != nil {
			return nil, err
		}
		res, err := bc.client.Solve(ctx, client.SolveRequest{
			Definition: def.ToPB(),
		})
		if err != nil {
			return nil, err
		}
		ref, err := res.SingleRef()
		if err != nil {
			return nil, err
		}
		dt, _ := ref.ReadFile(ctx, client.ReadRequest{ // ignore error
			Filename: DefaultGGUFPackerignoreName,
		})
		if dt == nil {
			dt = []byte{}
		}
		bc.ggufpackerignore = dt
		bc.ggufpackerignoreName = DefaultGGUFPackerignoreName
	}
	var err error
	var excludes []string
	if len(bc.ggufpackerignore) != 0 {
		excludes, err = ignorefile.ReadAll(bytes.NewBuffer(bc.ggufpackerignore))
		if err != nil {
			return nil, errors.Wrapf(err, "failed parsing %s", bc.ggufpackerignoreName)
		}
	}
	return excludes, nil
}
