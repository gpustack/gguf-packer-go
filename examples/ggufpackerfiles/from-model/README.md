# Reuse Model

This example demonstrates how to reuse an existing model image.

- Refer to the `thxcode/qwen2:0.5B-instruct-q5-k-m` image as the base image.
- Customize new system prompt.
- Reuse the parameters from base image.

## Prerequisites

- [Docker](https://docs.docker.com/engine/install/)

## Steps

1. Clone this repository.

```shell
$ git clone https://github.com/gpustack/gguf-packer-go
```

2. Change the directory to this example.

```shell
$ cd gguf-packer-go/examples/ggufpackerfiles/from-model
```

3. Build this example.

```shell
$ docker build --build-arg BUILDKIT_SYNTAX=gpustack/gguf-packer:latest --file GGUFPackerfile --tag from-model/qwen2:0.5b-instruct-q5-k-m --load $(pwd)

$ # or build with external buildkitd as below, see https://github.com/moby/buildkit.
$ # buildctl build --frontend gateway.v0 --opt source=gpustack/gguf-packer:latest --local context=$(pwd) --output type=docker,name=from-model/qwen2:0.5b-instruct-q5-k-m | docker load
```

4. Review the result.

```shell
$ docker images from-model/qwen2:0.5b-instruct-q5-k-m
```

The result image is tagged as `from-model/qwen2:0.5b-instruct-q5-k-m`, and size around 420MB.

## Further

Due to the lack of an executable file system, it is difficult for us to see the files in the image. We can reference an
executable file system and then configure the model and `CMD` as what [../from-alpine example](../from-alpine) does.
