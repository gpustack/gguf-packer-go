# Convert PEFT LoRA adapter to GGUF format

In this example, we use
a [containerizing buildkitd](https://hub.docker.com/repository/docker/thxCode/buildkit/tags) which supports Git LFS to
finish the demonstration.

This example demonstrates how to add a model from a Git repository with Git LFS.

- First stage:
    - Clone the base model and LoRA adapter from the Git repository.
    - Convert LoRA adapter to `F16` format GGUF file.
- Second stage:
    - Combine a pre-quantified model with the `F16` LoRA adapter GGUF file.
    - Parameterize how to run.

## Prerequisites

- [Docker](https://docs.docker.com/engine/install/)

## Steps

1. Clone this repository.

```shell
$ git clone https://github.com/gpustack/gguf-packer-go
```

2. Change the directory to this example.

```shell
$ cd gguf-packer-go/examples/ggufpackerfiles/convert-lora
```

3. Start buildkitd.

```shell
$ docker buildx create --name "git-lfs" --driver "docker-container" --driver-opt "image=thxcode/buildkit:v0.15.1-git-lfs" --buildkitd-flags "--allow-insecure-entitlement security.insecure --allow-insecure-entitlement network.host" --bootstrap 
```   

4. Build this example.

```shell
$ docker build --builder git-lfs --build-arg BUILDKIT_SYNTAX=gpustack/gguf-packer:latest --file GGUFPackerfile --tag convert-lora/qwen2:1.5b-mac-lora --load $(pwd)

$ # or build with external buildkitd as below, see https://github.com/moby/buildkit.
$ # buildctl build --frontend gateway.v0 --opt source=gpustack/gguf-packer:latest --local context=$(pwd) --output type=docker,name=convert-lora/qwen2:1.5b-mac-lora | docker load
```

5. Review the result.

```shell
$ docker images convert-lora/qwen2:1.5b-mac-lora
```
