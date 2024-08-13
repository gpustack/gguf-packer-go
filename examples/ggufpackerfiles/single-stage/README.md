# Single Stage Convert From Local

This example demonstrates how to convert a model from local to `Q5_K_M` format with a single stage.

- Copy the model([Qwen2-0.5B-Instruct](https://huggingface.co/Qwen/Qwen2-7B-Instruct)) from the local directory.
- Convert it to `F16` format GGUF file.
- Quantize the `F16` format GGUF file to `Q5_K_M` format.
- Parameterize how to run with `Q5_K_M` format GGUF file, including context size, system prompt and so on.

## Prerequisites

- [Docker](https://docs.docker.com/engine/install/)

## Steps

1. Clone this repository in recursive mode to get the submodules.

```shell
$ git clone --recurse--submodules https://github.com/gpustack/gguf-packer-go
```

2. Change the directory to this example.

```shell
$ cd gguf-packer-go/examples/ggufpackerfiles/single-stage
```

3. Build this example.

```shell
$ docker build --build-arg BUILDKIT_SYNTAX=gpustack/gguf-packer:latest --file GGUFPackerfile --tag single-stage/qwen2:0.5b-instruct-q5-k-m --load $(pwd)

$ # or build with external buildkitd as below, see https://github.com/moby/buildkit.
$ # buildctl build --frontend gateway.v0 --opt source=gpustack/gguf-packer:latest --local context=$(pwd) --output type=docker,name=single-stage/qwen2:0.5b-instruct-q5-k-m | docker load
```

4. Review the result.

```shell
$ docker images single-stage/qwen2:0.5b-instruct-q5-k-m
```

The result image is tagged as `single-stage/qwen2:0.5b-instruct-q5-k-m`, and size around 2.4GB.

## Further

To reduce the size of the image further, you can view the [multi-stages example](../multi-stages).
