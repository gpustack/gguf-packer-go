# Add Quantified Model From HTTP

This example demonstrates how to add a quantified model from remote.

- Add the quantified model([Qwen2-0.5B-Instruct-GGUF](https://huggingface.co/Qwen/Qwen2-0.5B-Instruct-GGUF)) from
  remote.
- Parameterize how to run with `Q5_K_M` format GGUF file, including context size, system prompt and so on.

## Prerequisites

- [Docker](https://docs.docker.com/engine/install/)

## Steps

1. Clone this repository.

```shell
$ git clone https://github.com/gpustack/gguf-packer-go
```

2. Change the directory to this example.

```shell
$ cd gguf-packer-go/examples/ggufpackerfiles/add-from-http
```

3. Build this example.

```shell
$ docker build --build-arg BUILDKIT_SYNTAX=gpustack/gguf-packer:latest --file GGUFPackerfile --tag add-from-http/qwen2:0.5b-instruct-q5-k-m --load $(pwd)

$ # or build with external buildkitd as below, see https://github.com/moby/buildkit.
$ # buildctl build --frontend gateway.v0 --opt source=gpustack/gguf-packer:latest --local context=$(pwd) --output type=docker,name=add-from-http/qwen2:0.5b-instruct-q5-k-m | docker load
```

4. Review the result.

```shell
$ docker images add-from-http/qwen2:0.5b-instruct-q5-k-m
```

The result image is tagged as `add-from-http/qwen2:0.5b-instruct-q5-k-m`, and size around 420MB.

## Further

Usually we don't need to start from scratch, just use `FROM` to reference another base image, please view
the [from-model example](../from-model) for more details.
