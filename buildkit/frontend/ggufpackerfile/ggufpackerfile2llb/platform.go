package ggufpackerfile2llb

import (
	"github.com/containerd/platforms"

	"github.com/gpustack/gguf-packer-go/buildkit/frontend/ggufpackerfile/instructions"
	specs "github.com/gpustack/gguf-packer-go/buildkit/frontend/specs/v1"
)

type platformOpt struct {
	targetPlatform specs.Platform
	buildPlatforms []specs.Platform
	implicitTarget bool
}

func buildPlatformOpt(opt *ConvertOpt) *platformOpt {
	buildPlatforms := opt.BuildPlatforms
	targetPlatform := opt.TargetPlatform
	implicitTargetPlatform := false

	if opt.TargetPlatform != nil && opt.BuildPlatforms == nil {
		buildPlatforms = []specs.Platform{*opt.TargetPlatform}
	}
	if len(buildPlatforms) == 0 {
		buildPlatforms = []specs.Platform{platforms.DefaultSpec()}
	}

	for i := range buildPlatforms {
		if buildPlatforms[i].OS == "darwin" {
			buildPlatforms[i].OS = "linux"
		}
	}

	if opt.TargetPlatform == nil {
		implicitTargetPlatform = true
		targetPlatform = &buildPlatforms[0]
	}

	return &platformOpt{
		targetPlatform: *targetPlatform,
		buildPlatforms: buildPlatforms,
		implicitTarget: implicitTargetPlatform,
	}
}

func getPlatformArgs(po *platformOpt) []instructions.KeyValuePairOptional {
	bp := po.buildPlatforms[0]
	tp := po.targetPlatform
	m := map[string]string{
		"BUILDPLATFORM":  platforms.Format(bp),
		"BUILDOS":        bp.OS,
		"BUILDARCH":      bp.Architecture,
		"BUILDVARIANT":   bp.Variant,
		"TARGETPLATFORM": platforms.Format(tp),
		"TARGETOS":       tp.OS,
		"TARGETARCH":     tp.Architecture,
		"TARGETVARIANT":  tp.Variant,
	}
	opts := make([]instructions.KeyValuePairOptional, 0, len(m))
	for k, v := range m {
		s := v
		opts = append(opts, instructions.KeyValuePairOptional{Key: k, Value: &s})
	}
	return opts
}
