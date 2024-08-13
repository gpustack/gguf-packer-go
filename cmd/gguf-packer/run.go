package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"

	"github.com/google/go-containerregistry/pkg/name"
	specs "github.com/gpustack/gguf-packer-go/buildkit/frontend/specs/v1"
	"github.com/gpustack/gguf-packer-go/util/osx"
	"github.com/gpustack/gguf-packer-go/util/strconvx"
	"github.com/gpustack/gguf-parser-go/util/stringx"
	"github.com/spf13/cobra"
)

func run(app string) *cobra.Command {
	var (
		by     = "ghcr.io/ggerganov/llama.cpp:server"
		dryRun bool
	)
	c := &cobra.Command{
		Use:   "run MODEL [ARG...]",
		Short: "Run a model by specific process, like container image or executable binary.",
		Example: sprintf(`  # Run a model by container image: ghcr.io/ggerganov/llama.cpp:server
  %s run gpustack/qwen2:latest

  # Customize model running
  %[1]s run gpustack/qwen2:latest -- --port 8888 -c 8192 -np 4

  # Run a model by executable binary: llama-box
  %[1]s run gpustack/qwen2:latest --by llama-box

  # Dry run to print the command that would be executed
  %[1]s run gpustack/qwen2:latest --dry-run`, app),
		Args:                  cobra.MinimumNArgs(1),
		DisableFlagsInUseLine: true,
		FParseErrWhitelist: cobra.FParseErrWhitelist{
			UnknownFlags: true,
		},
		RunE: func(c *cobra.Command, args []string) error {
			isByContainer := true
			if _, err := name.ParseReference(by, name.StrictValidation); err != nil {
				isByContainer = false
				if !dryRun {
					if _, err = exec.LookPath(by); err != nil {
						return fmt.Errorf("looking up binary %s: %v", by, err)
					}
				}
			}

			var cfp, lsp string
			{
				model := args[0]
				rf, err := name.NewTag(model)
				if err != nil {
					return fmt.Errorf("parsing model reference %q: %w", model, err)
				}
				mdp := getModelMetadataStorePath(rf)
				if !osx.ExistsLink(mdp) {
					if err = pull(app).RunE(c, []string{model}); err != nil {
						return err
					}
				}
				cfp, err = os.Readlink(mdp)
				if err != nil {
					return fmt.Errorf("reading link %s: %w", mdp, err)
				}
				lsp = convertConfigStorePathToLayersStorePath(cfp)
			}

			img, err := retrieveConfigByPath(cfp)
			if err != nil {
				return err
			}

			wdp := lsp
			if isByContainer {
				wdp = "/gp-" + stringx.RandomHex(4)
			}

			var (
				cmdExec string
				cmdArgs []string
			)

			if isByContainer {
				cmdExec = "docker"
				cmdArgs = []string{
					"run",
					"--rm",
					"--interactive",
					"--tty",
				}
				if isDockerGPUSupported(c.Context()) {
					cmdArgs = append(cmdArgs,
						"--gpus", "all")
				} else {
					cmdArgs = append(cmdArgs,
						"--privileged")
				}
				port := "8080"
				for i, s := 1, len(args); i < s; i++ {
					if args[i] == "--port" {
						if i+1 >= s {
							return fmt.Errorf("missing value for %q", args[i])
						}
						port = args[i+1]
						args = append(args[:i], args[i+2:]...)
						break
					}
				}
				cmdArgs = append(cmdArgs,
					"--publish", fmt.Sprintf("%s:8080", port),
					"--volume", fmt.Sprintf("%s:%s", lsp, wdp),
					by,
				)
			} else {
				cmdExec = by
			}

			execArgs := img.Config.Cmd
			{
				cfg := img.Config
				join := filepath.Join
				if isByContainer {
					join = path.Join
				}
				for _, v := range append([]*specs.GGUFFile{cfg.Model, cfg.Drafter, cfg.Projector}, cfg.Adapters...) {
					if v == nil {
						continue
					}
					execArgs[v.CmdParameterIndex] = join(wdp, v.CmdParameterValue)
				}
				for i, s := 0, len(execArgs); i < s; i++ {
					if strings.HasPrefix(execArgs[i], "-") {
						if !strings.HasSuffix(execArgs[i], "-file") {
							continue
						}
						if i+1 >= s {
							continue
						}
						i++
						execArgs[i] = join(wdp, execArgs[i])
					}
					execArgs[i] = strconvx.Quote(execArgs[i])
				}
			}
			cmdArgs = append(cmdArgs, execArgs...)

			cmdArgs = append(cmdArgs, args[1:]...)

			if isByContainer {
				cmdArgs = append(cmdArgs, "--host", "0.0.0.0")
			}

			if dryRun {
				fprintf(c.OutOrStdout(), "%s %s", cmdExec, strings.Join(cmdArgs, " "))
				return nil
			}

			cmd := exec.CommandContext(c.Context(), cmdExec, cmdArgs...)
			cmd.Stdin = c.InOrStdin()
			cmd.Stdout = c.OutOrStdout()
			cmd.Stderr = c.ErrOrStderr()
			err = cmd.Run()
			if err != nil && strings.Contains(err.Error(), "signal: killed") {
				return nil
			}
			return err
		},
	}
	c.Flags().StringVar(&by, "by", by, "Specify how to run the model. "+
		"If given a strict format container image reference, it will be run via Docker container, "+
		"otherwise it will be run via executable binary.")
	c.Flags().BoolVar(&dryRun, "dry-run", dryRun, "Print the command that would be executed, but do not execute it.")
	return c
}

func isDockerGPUSupported(ctx context.Context) bool {
	bs, err := exec.
		CommandContext(ctx, "docker", "info", "--format", "json").
		CombinedOutput()
	if err != nil {
		return false
	}

	var r struct {
		Runtimes map[string]any `json:"Runtimes"`
	}
	if err = json.Unmarshal(bs, &r); err != nil {
		return false
	}

	_, ok := r.Runtimes["nvidia"]
	return ok
}
