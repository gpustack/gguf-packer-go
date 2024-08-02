# Multi Stages Convert From Local

This example demonstrates how to convert a model from local to `Q5_K_M` format with multi-stages,
and how to quantize various types of models with the same GGUFPackerfile.

- First stage:
    - Copy the model([Qwen2-0.5B-Instruct](https://huggingface.co/Qwen/Qwen2-7B-Instruct)) from the local directory.
    - Convert it to `F16` format GGUF file.
- Second stage:
    - Quantize the `F16` format GGUF file from the first stage to `Q5_K_M` format.
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
$ cd gguf-packer-go/examples/ggufpackerfiles/multi-stages
```

3. Build this example.

```shell
$ docker build --build-arg BUILDKIT_SYNTAX=thxcode/gguf-packer:latest --file GGUFPackerfile --tag multi-stages/qwen2:0.5b-instruct-q5-k-m --load $(pwd)

$ # or build with external buildkitd as below, see https://github.com/moby/buildkit.
$ # buildctl build --frontend gateway.v0 --opt source=thxcode/gguf-packer:latest --local context=$(pwd) --output type=docker,name=multi-stages/qwen2:0.5b-instruct-q5-k-m | docker load
```

4. Review the result.

```shell
$ docker images multi-stages/qwen2:0.5b-instruct-q5-k-m
```

The result image is tagged as `multi-stages/qwen2:0.5b-instruct-q5-k-m`, and size around 420MB.

Compare to the [single-stage example](../single-stage), this example leverages
multi-stages build to reduce the size of the image.

## Further

Use the single GGUFPackerfile to build other quantization model, such as `Q6_K` type.

```shell
$ docker build --build-arg BUILDKIT_SYNTAX=thxcode/gguf-packer:latest --build-arg QUANTIZE_TYPE=Q6_K --file GGUFPackerfile --tag multi-stages/qwen2:0.5b-instruct-q6-k --load $(pwd)

$ # or build with external buildkitd as below, see https://github.com/moby/buildkit.
$ # buildctl build --frontend gateway.v0 --opt source=thxcode/gguf-packer:latest --opt build-arg:QUANTIZE_TYPE=Q6_K --local context=$(pwd) --output type=docker,name=multi-stages/qwen2:0.5b-instruct-q6-k | docker load

$ docker images multi-stages/qwen2:0.5b-instruct-q6-k
```

The result image is tagged as `multi-stages/qwen2:0.5b-instruct-q6-k`, and size around 506MB.

To avoid caching the safetensors model files, we can adjust the GGUFPackerfile content as below:

```diff
  ARG        CHAT_TEMPLATE="{% for message in messages %}{{'<|im_start|>' + message['role'] + '\n' + message['content'] + '<|im_end|>' + '\n'}}{% endfor %}{% if add_generation_prompt %}{{ '<|im_start|>assistant\n' }}{% endif %}"

  FROM       scratch AS f16
- COPY       /                      .
- CONVERT    --type=${CONVERT_TYPE} ${MODEL_NAME} ${MODEL_NAME}.${CONVERT_TYPE}.gguf
+ CONVERT    --from=context --type=${CONVERT_TYPE} ${MODEL_NAME} ${MODEL_NAME}.${CONVERT_TYPE}.gguf

  FROM       f16 AS quantize
```

To avoid maintaining a local original model, we can `ADD` the model from remote as what [add-from-git example](../add-from-git) does.
