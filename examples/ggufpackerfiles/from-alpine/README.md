# Refer Runnable Base Image

This example demonstrates how to refer a runnable base image.

- Refer to the `alpine` image as the base image.
- Add remote quantified `Q5_K_M` format GGUF file .
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
$ cd gguf-packer-go/examples/ggufpackerfiles/from-alpine
```

3. Build this example.

```shell
$ docker build --build-arg BUILDKIT_SYNTAX=gpustack/gguf-packer:latest --file GGUFPackerfile --tag from-alpine/qwen2:0.5b-instruct-q5-k-m --load $(pwd)

$ # or build with external buildkitd as below, see https://github.com/moby/buildkit.
$ # buildctl build --frontend gateway.v0 --opt source=gpustack/gguf-packer:latest --local context=$(pwd) --output type=docker,name=from-alpine/qwen2:0.5b-instruct-q5-k-m | docker load
```

4. Review the result.

```shell
$ docker images from-alpine/qwen2:0.5b-instruct-q5-k-m
```

The result image is tagged as `from-alpine/qwen2:0.5b-instruct-q5-k-m`, and size around 429MB.

5. Run the result image.

```shell
$ docker run --rm -it from-alpine/qwen2:0.5b-instruct-q5-k-m ls -lh /app
total 401M
drwxr-xr-x    1 root     root        4.0K Aug  2 07:53 ..
drwxr-xr-x    1 root     root        4.0K Aug  2 07:51 .
-rw-r--r--    1 root     root        1.8K Aug  2 07:50 README.md
-rw-r--r--    1 root     root          26 Aug  2 07:50 .ggufpackerignore
-rw-r--r--    1 root     root         517 Aug  2 05:56 system-prompt.txt
-rw-------    1 root     root      400.6M Jun 12 05:27 Qwen2-0.5B-Instruct.Q5_K_M.gguf
```
