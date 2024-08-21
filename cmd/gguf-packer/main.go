package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/gpustack/gguf-packer-go/util/anyx"
	"github.com/gpustack/gguf-packer-go/util/osx"
	"github.com/gpustack/gguf-packer-go/util/signalx"
	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/jedib0t/go-pretty/v6/text"
	"github.com/spf13/cobra"
)

var (
	Version = "v0.0.0"

	storePath string
)

func main() {
	var (
		stdin  = os.Stdin
		stdout = os.Stdout
		stderr = os.Stderr
	)
	storePath = osx.ExpandEnv("GGUF_PACKER_STORE_PATH")
	if storePath == "" {
		hd, err := os.UserHomeDir()
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "getting home directory: %v\n", err)
			os.Exit(1)
		}
		storePath = filepath.Join(hd, "gguf-packer")
	}
	if err := os.MkdirAll(storePath, 0755); err != nil {
		_, _ = fmt.Fprintf(stderr, "creating store directory: %v\n", err)
		os.Exit(1)
	}
	slog.SetDefault(slog.New(slog.NewTextHandler(stderr, nil)))

	app := filepath.Base(os.Args[0])
	root := &cobra.Command{
		Version: Version,
		Use:     app,
		Short:   "Pack the GGUF format model.",
		CompletionOptions: cobra.CompletionOptions{
			DisableDefaultCmd: true,
		},
		Example: sprintf(`  # Serve as BuildKit frontend
  %s llb-frontend

  # Dump the BuildKit LLB of the current directory
  %[1]s llb-dump

  # Pull the model from the registry
  %[1]s pull gpustack/qwen2:0.5b-instruct

  # Inspect the model
  %[1]s inspect gpustack/qwen2:0.5b-instruct

  # Estimate the model memory usage
  %[1]s estimate gpustack/qwen2:0.5b-instruct

  # List all local models
  %[1]s list

  # Remove a local model
  %[1]s remove gpustack/qwen2:0.5b-instruct

  # Run a model by container container: ghcr.io/ggerganov/llama.cpp:server
  %[1]s run gpustack/qwen2:0.5b-instruct`, app),
	}
	for _, cmdCreate := range []func(string) *cobra.Command{
		llbFrontend, llbDump, inspect, pull, estimate, list, remove, run,
	} {
		cmd := cmdCreate(app)
		root.AddCommand(cmd)
	}
	root.SetIn(stdin)
	root.SetOut(stdout)
	root.SetErr(stderr)

	if err := root.ExecuteContext(signalx.Handler()); err != nil {
		_, _ = fmt.Fprintf(stderr, "%v\n", err)
		os.Exit(1)
	}
}

func getModelsStorePath() string {
	return filepath.Join(storePath, "models")
}

func getModelsMetadataStorePath() string {
	return filepath.Join(getModelsStorePath(), "metadata")
}

func getModelsConfigStorePath() string {
	return filepath.Join(getModelsStorePath(), "config")
}

func getModelsLayersStorePath() string {
	return filepath.Join(getModelsStorePath(), "layers")
}

func fprintf(w io.Writer, format string, a ...any) {
	_, _ = fmt.Fprintf(w, format, a...)
}

func fprint(w io.Writer, a ...any) {
	_, _ = fmt.Fprint(w, a...)
}

func tfprint(w io.Writer, border bool, headers, bodies [][]any) {
	tw := table.NewWriter()
	tw.SetOutputMirror(w)
	for i := range headers {
		tw.AppendHeader(headers[i], table.RowConfig{AutoMerge: true, AutoMergeAlign: text.AlignCenter})
	}
	for i := range bodies {
		tw.AppendRow(bodies[i])
	}
	tw.SetColumnConfigs(func() (r []table.ColumnConfig) {
		r = make([]table.ColumnConfig, len(headers[0]))
		for i := range r {
			r[i].Number = i + 1
			r[i].AutoMerge = border
			if len(headers) > 1 && (headers[1][i] == "UMA" || headers[1][i] == "NonUMA") {
				r[i].AutoMerge = false
			}
			r[i].Align = text.AlignCenter
			if !border {
				r[i].Align = text.AlignLeft
			}
			r[i].AlignHeader = text.AlignCenter
		}
		return r
	}())
	{
		tw.Style().Options.DrawBorder = border
		tw.Style().Options.DrawBorder = border
		tw.Style().Options.SeparateHeader = border
		tw.Style().Options.SeparateFooter = border
		tw.Style().Options.SeparateColumns = border
		tw.Style().Options.SeparateRows = border
	}
	tw.Render()
}

func jprint(w io.Writer, v any) {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(v)
}

func sprintf(format any, a ...any) string {
	if v, ok := format.(string); ok {
		if len(a) != 0 {
			return fmt.Sprintf(v, a...)
		}
		return v
	}
	return anyx.String(format)
}

func tenary(c bool, t, f any) any {
	if c {
		return t
	}
	return f
}
