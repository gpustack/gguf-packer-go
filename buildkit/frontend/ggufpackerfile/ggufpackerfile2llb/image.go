package ggufpackerfile2llb

import (
	specs "github.com/gpustack/gguf-packer-go/buildkit/frontend/specs/v1"
)

func clone(src specs.Image) specs.Image {
	img := src
	img.Config = src.Config
	return img
}

func cloneX(src *specs.Image) *specs.Image {
	if src == nil {
		return nil
	}
	img := clone(*src)
	return &img
}

func emptyImage(platform specs.Platform) specs.Image {
	var img specs.Image
	img.Architecture = platform.Architecture
	img.OS = platform.OS
	img.OSVersion = platform.OSVersion
	if platform.OSFeatures != nil {
		img.OSFeatures = append([]string{}, platform.OSFeatures...)
	}
	img.Variant = platform.Variant
	return img
}
