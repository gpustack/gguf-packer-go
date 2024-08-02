# Add Model From Git Repository

The model files are usually large, and [Git LFS](https://git-lfs.com/) is used in the management of Git Repo.

The original `ADD` instruction of the Dockerfile frontend
supports [adding files from a URL](https://github.com/moby/buildkit/blob/master/frontend/dockerfile/docs/reference.md#adding-files-from-a-url),
but it doesn't support Git LFS, see https://github.com/moby/buildkit/pull/5212.

In this example, we use
a [containerizing buildkitd](https://hub.docker.com/repository/docker/thxcode/buildkit/tags) which supports Git LFS to
finish the demonstration.

This example demonstrates how to add a model from a Git repository with Git LFS.

- First stage:
    - Clone the model from the Git repository.
    - Convert it to `F16` format GGUF file.
- Second stage:
    - Quantize the `F16` format GGUF file to `Q5_K_M` format.
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
$ cd gguf-packer-go/examples/ggufpackerfiles/add-from-git
```

3. Start buildkitd.

```shell
$ docker buildx create --name "git-lfs" --driver "docker-container" --driver-opt "image=thxcode/buildkit:v0.15.1-git-lfs" --buildkitd-flags "--allow-insecure-entitlement security.insecure --allow-insecure-entitlement network.host" --bootstrap 
```   

4. Build this example.

```shell
$ docker build --builder git-lfs --build-arg BUILDKIT_SYNTAX=thxcode/gguf-packer:latest --file GGUFPackerfile --tag add-from-git/qwen2:0.5b-instruct-q5-k-m --load $(pwd)

$ # or build with external buildkitd as below, see https://github.com/moby/buildkit.
$ # buildctl build --frontend gateway.v0 --opt source=thxcode/gguf-packer:latest --local context=$(pwd) --output type=docker,name=add-from-git/qwen2:0.5b-instruct-q5-k-m | docker load
```

5. Review the result.

```shell
$ docker images add-from-git/qwen2:0.5b-instruct-q5-k-m
```

The result image is tagged as `add-from-git/qwen2:0.5b-instruct-q5-k-m`, and size around 420MB.

Compare to the [multi-stages example](../multi-stages), this example leverages `ADD` instruction to avoid maintaining a
model in local.

## Further

The community provides a large number of already quantified models, and we can `ADD` the quantified model and then
customize the `CMD`, please view the [add-from-http example](../add-from-http) for more details.
