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
	"github.com/olekukonko/tablewriter"
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
  %[1]s pull gpustack/qwen2:latest

  # Inspect the model
  %[1]s inspect gpustack/qwen2:latest

  # Estimate the model memory usage
  %[1]s estimate gpustack/qwen2:latest

  # List all local models
  %[1]s list

  # Remove a local model
  %[1]s remove gpustack/qwen2:latest

  # Run a model via Docker container: ghcr.io/ggerganov/llama.cpp:server
  %[1]s run gpustack/qwen2:latest`, app),
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

func tfprint(w io.Writer, border bool, header []string, mergeCells []int, body ...[]string) {
	tb := tablewriter.NewWriter(w)

	tb.SetTablePadding("\t")
	tb.SetHeaderLine(border)
	tb.SetRowLine(border)
	tb.SetBorder(border)
	tb.SetAlignment(tablewriter.ALIGN_CENTER)
	if !border {
		tb.SetAlignment(tablewriter.ALIGN_LEFT)
		tb.SetColumnSeparator("")
	}

	tb.SetHeaderAlignment(tablewriter.ALIGN_CENTER)
	tb.SetHeader(header)

	tb.SetAutoWrapText(false)
	tb.SetColMinWidth(0, 12)
	tb.SetAutoMergeCells(false)
	if len(mergeCells) != 0 {
		tb.SetAutoMergeCellsByColumnIndex(mergeCells)
	}
	for i := range body {
		tb.Append(body[i])
	}

	tb.Render()
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
