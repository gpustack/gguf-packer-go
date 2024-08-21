# GGUF Packer

Deliver LLMs of [GGUF](https://github.com/ggerganov/ggml/blob/master/docs/gguf.md) format via Dockerfile, 
see [https://github.com/gpustack/gguf-packer-go](https://github.com/gpustack/gguf-packer-go).

## Usage

```shell
$ gguf-packer --help
Pack the GGUF format model.

Usage:
  gguf-packer [command]

Examples:
  # Serve as BuildKit frontend
  gguf-packer llb-frontend

  # Dump the BuildKit LLB of the current directory
  gguf-packer llb-dump

  # Pull the model from the registry
  gguf-packer pull gpustack/qwen2:0.5b-instruct

  # Inspect the model
  gguf-packer inspect gpustack/qwen2:0.5b-instruct

  # Estimate the model memory usage
  gguf-packer estimate gpustack/qwen2:0.5b-instruct

  # List all local models
  gguf-packer list

  # Remove a local model
  gguf-packer remove gpustack/qwen2:0.5b-instruct

  # Run a model by container container: ghcr.io/ggerganov/llama.cpp:server
  gguf-packer run gpustack/qwen2:0.5b-instruct

Available Commands:
  estimate     Estimate the model memory usage.
  help         Help about any command
  inspect      Get the low-level information of a model.
  list         List all local models.
  llb-dump     Dump the BuildKit LLB of the GGUFPackerfile.
  llb-frontend Serve as BuildKit frontend.
  pull         Download a model from a registry.
  remove       Remove one or more local models.
  run          Run a model by specific process, like container image or executable binary.

Flags:
  -h, --help      help for gguf-packer
  -v, --version   version for gguf-packer

Use "gguf-packer [command] --help" for more information about a command.

```

## License

MIT
