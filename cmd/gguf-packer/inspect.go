package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"

	"github.com/google/go-containerregistry/pkg/crane"
	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	specs "github.com/gpustack/gguf-packer-go/buildkit/frontend/specs/v1"
	"github.com/gpustack/gguf-packer-go/util/osx"
	"github.com/spf13/cobra"
)

func inspect(app string) *cobra.Command {
	var (
		insecure bool
		force    bool
	)

	c := &cobra.Command{
		Use:   "inspect MODEL",
		Short: "Get the low-level information of a model.",
		Example: sprintf(`  # Inspect a model
  %s inspect gpustack/qwen2:0.5b-instruct

  # Force inspect a model from remote
  %[1]s inspect gpustack/qwen2:0.5b-instruct --force`, app),
		Args: cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			model := args[0]

			var cos crane.Options
			{
				co := []crane.Option{
					getAuthnKeychainOption(),
				}
				if insecure {
					co = append(co, crane.Insecure)
				}
				cos = crane.GetOptions(co...)
			}

			rf, err := name.NewTag(model, cos.Name...)
			if err != nil {
				return fmt.Errorf("parsing model reference %q: %w", model, err)
			}

			cf, err := retrieveConfigByOCIReference(force, rf, cos.Remote...)
			if err != nil {
				return err
			}
			cf.History = nil // Remove history.
			jprint(c.OutOrStdout(), cf)
			return nil
		},
	}
	c.Flags().BoolVar(&insecure, "insecure", insecure, "Allow model references to be fetched without TLS.")
	c.Flags().BoolVar(&force, "force", force, "Always inspect the model from the registry.")
	return c
}

func retrieveConfigByOCIReference(force bool, ref name.Reference, opts ...remote.Option) (cf specs.Image, err error) {
	// Read from local.
	if !force {
		mdp := getModelMetadataStorePath(ref)
		if osx.ExistsLink(mdp) {
			return retrieveConfigByPath(mdp)
		}
	}

	// Otherwise, read from remote.
	rd, err := remote.Get(ref, opts...)
	if err != nil {
		return cf, fmt.Errorf("getting model remote %q: %w", ref.Name(), err)
	}
	img, err := retrieveOCIImage(rd)
	if err != nil {
		return cf, err
	}
	cf, _, err = retrieveConfigByOCIImage(img)
	return cf, err
}

func retrieveConfigByPath(cfp string) (cf specs.Image, err error) {
	cfBs, err := os.ReadFile(cfp)
	if err != nil {
		return cf, fmt.Errorf("reading model config: %w", err)
	}
	if err = json.Unmarshal(cfBs, &cf); err != nil {
		return cf, fmt.Errorf("unmarshalling model config: %w", err)
	}
	if !isConfigAvailable(&cf) {
		return cf, errors.New("unavailable model config")
	}
	return cf, nil
}
