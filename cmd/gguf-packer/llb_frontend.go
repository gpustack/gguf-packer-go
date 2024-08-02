package main

import (
	ggufpacker "github.com/gpustack/gguf-packer-go"
	"github.com/moby/buildkit/frontend/gateway/grpcclient"
	"github.com/spf13/cobra"
)

func llbFrontend(app string) *cobra.Command {
	c := &cobra.Command{
		Use:   "llb-frontend",
		Short: "Serve as BuildKit frontend.",
		Example: sprintf(`  # Serve as BuildKit frontend
  %s llb-frontend`, app),
		Args: cobra.ExactArgs(0),
		RunE: func(c *cobra.Command, args []string) error {
			return grpcclient.RunFromEnvironment(c.Context(), ggufpacker.Build)
		},
	}
	return c
}
