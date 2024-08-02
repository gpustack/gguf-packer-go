package gguf_packer

import (
	"context"

	"github.com/moby/buildkit/client/llb"
	"github.com/moby/buildkit/frontend/gateway/client"

	"github.com/gpustack/gguf-packer-go/buildkit/frontend/ggufpackerfile/builder"
	"github.com/gpustack/gguf-packer-go/buildkit/frontend/ggufpackerfile/ggufpackerfile2llb"
)

// Build to build an image from a GGUFPackerfile.
func Build(ctx context.Context, c client.Client) (*client.Result, error) {
	return builder.Build(ctx, c)
}

// ToLLB to convert a GGUFPackerfile to LLB.
func ToLLB(ctx context.Context, bs []byte) (*llb.State, error) {
	st, _, _, _, err := ggufpackerfile2llb.ToLLB(ctx, bs, ggufpackerfile2llb.ConvertOpt{})
	return st, err
}
