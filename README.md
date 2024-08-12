# GGUF Packer

> tl;dr, Deliver LLMs of [GGUF](https://github.com/ggerganov/ggml/blob/master/docs/gguf.md) via Dockerfile.

[![Go Report Card](https://goreportcard.com/badge/github.com/thxcode/gguf-packer-go)](https://goreportcard.com/report/github.com/thxcode/gguf-packer-go)
[![CI](https://img.shields.io/github/actions/workflow/status/thxcode/gguf-packer-go/cmd.yml?label=ci)](https://github.com/thxcode/gguf-packer-go/actions)
[![License](https://img.shields.io/github/license/thxcode/gguf-packer-go?label=license)](https://github.com/thxcode/gguf-packer-go#license)
[![Download](https://img.shields.io/github/downloads/thxcode/gguf-packer-go/total)](https://github.com/thxcode/gguf-packer-go/releases)
[![Docker Pulls](https://img.shields.io/docker/pulls/thxcode/gguf-packer)](https://hub.docker.com/r/thxcode/gguf-packer)
[![Release](https://img.shields.io/github/v/release/thxcode/gguf-packer-go)](https://github.com/thxcode/gguf-packer-go/releases/latest)

[GGUF](https://github.com/ggerganov/ggml/blob/master/docs/gguf.md) is a file format for storing models for inference
with GGML and executors based on GGML. GGUF is a binary format that is designed for fast loading and saving of models,
and for ease of reading. Models are traditionally developed using PyTorch or another framework, and then converted to
GGUF for use in GGML.

GGUF Packer can help you to build Large Language Model (LLM) distributions, all you need is
a [Docker](https://www.docker.com/)(
or [BuildKit daemon](https://github.com/moby/buildkit?tab=readme-ov-file#quick-start)).

- GGUF Packer implements the [BuildKit](https://github.com/moby/buildkit) frontend to leverage all the features of
  BuildKit.
- Use Dockerfile directly instead of
  the [Ollama Model File](https://github.com/ollama/ollama/blob/main/docs/modelfile.md).
- [CloudNative](https://www.cncf.io/) friendly.

## Agenda

- [Quick Start](#quick-start)
    + [Requirements](#requirements)
    + [Write Dockerfile](#write-dockerfile)
    + [Build Model](#build-model)
    + [Estimate Model Memory Usage](#estimate-model-memory-usage)
    + [Build Model with other Quantize Type](#build-model-with-other-quantize-type)
    + [Pull Model from Container Image Registry](#pull-model-from-container-image-registry)
    + [Run Model](#run-model)
    + [Refer Model](#refer-model)
- [GGUFPackerfile](#ggufpackerfile)
- [Overview](#overview)
    + [Format](#format)
    + [Instructions](#instructions)
- [Motivation](#motivation)
    + [Docker Image](#docker-image)
    + [OCI Distribution](#oci-distribution)
    + [Ollama Model](#ollama-model)
    + [Conclusion](#conclusion)

## Quick Start

### Requirements

- [Docker](https://docs.docker.com/engine/install/)
- [GGUF Packer](./cmd/gguf-packer) from [releases](https://github.com/gpustack/gguf-packer-go/releases)
  or `go install github.com/gpustack/gguf-packer-go/cmd/gguf-packer` from HEAD.

### Write Dockerfile

Create a `Dockefile` file with below content.

```dockerfile
# syntax=thxcode/gguf-packer:latest

ARG        BASE=scratch
ARG        QUANTIZE_TYPE=Q5_K_M
ARG        CHAT_TEMPLATE="{% for message in messages %}{{'<|im_start|>' + message['role'] + '\n' + message['content'] + '<|im_end|>' + '\n'}}{% endfor %}{% if add_generation_prompt %}{{ '<|im_start|>assistant\n' }}{% endif %}"

FROM       scratch AS f16
ADD        https://huggingface.co/Qwen/Qwen2-0.5B-Instruct.git  Qwen2-0.5B-Instruct
CONVERT    --type=F16  Qwen2-0.5B-Instruct  Qwen2-0.5B-Instruct.F16.gguf

FROM       ${BASE}
LABEL      gguf.model.from="Hugging Face"
QUANTIZE   --from=f16 --type=${QUANTIZE_TYPE}  Qwen2-0.5B-Instruct.F16.gguf  Qwen2-0.5B-Instruct.${QUANTIZE_TYPE}.gguf
CAT        <<EOF system-prompt.txt
You are an AI assistant that follows instruction extremely well. Help as much as you can.
In answering questions, follow these steps:
1. Entity Identification: Identify the main entities involved in the query. Entities can include people, objects, events, or abstract concepts.
2. Relationship Comprehension: Determine the relationships between these entities. Relationships may be explicit in the text or may need to be inferred based on context and general knowledge.
3. Implication Understanding: Understand the general implications of these relationships. These implications can be based on established facts, principles, or rules related to the identified relationships.
4. Question Contextualization: Align the implications of the relationships with the context of the query. This alignment should guide your response to the query.
5. Answer Generation: Based on the understanding of the entities, their relationships, and implications, generate an appropriate response to the query.
EOF
CMD        ["-m", "Qwen2-0.5B-Instruct.${QUANTIZE_TYPE}.gguf", "-c", "8192", "--system-prompt-file", "system-prompt.txt", "--chat-template", "${CHAT_TEMPLATE}"]
```

The above `Dockerfile` will build a LLM distribution with
the [Qwen2-0.5B-Instruct](https://huggingface.co/Qwen/Qwen2-0.5B-Instruct/tree/main) model, which is quantized
by `Q5_K_M`.

- `ARG BASE=...`: The base image for the build, default is `scratch`.
- `ARG QUANTIZE_TYPE=...`: The quantize type for the model, default is `Q5_K_M`.
- `ARG CHAT_TEMPLATE="..."`: The chat template for the model, default is the OpenAI GPT-3 chat template.
- `FROM scratch AS f16`: The first stage to convert the model to `F16` format, named `f16`.
    + `ADD ...`: Add the model from the Hugging Face repository.
    + `CONVERT ...`: Convert the model to `F16` format.
- `FROM ${BASE}`: The second stage to quantize the model from the first stage, and set the system prompt and chat
  template.
    + `LABEL ...`: Add metadata to the image.
    + `QUANTIZE ...`: Quantize the model from the first stage.
    + `CAT ...`: Concatenate the system prompt to a file.
    + `CMD ...`: Specify the default commands.

### Build Model

`ADD` instruction allows cloning the model from the Git repository, however, the Git LFS is not supported yet,
see [moby/buildkit#5212](https://github.com/moby/buildkit/pull/5212). We can use a developing buildkitd to archive the
goal.

First, set up a containerizing buildkitd.

```shell
$ docker buildx create --name "git-lfs" --driver "docker-container" --driver-opt "image=thxcode/buildkit:v0.15.1-git-lfs" --buildkitd-flags "--allow-insecure-entitlement security.insecure --allow-insecure-entitlement network.host" --bootstrap 
```

Then, build and publish the model. With `--push` argument, the built model will be published to the Docker registry.

```shell
$ export REPO="YOUR_REPOSITORY"
$ docker build --builder git-lfs --tag ${REPO}/qwen2:0.5b-instruct-q5-k-m-demo --load --push $(pwd)
```

### Estimate Model Memory Usage

After the build is completed, we can use `gguf-packer` to estimate the model.

```shell
$ gguf-packer estimate ${REPO}/qwen2:0.5b-instruct-q5-k-m-demo
+--------------+--------------+--------------------+-----------------+--------------+----------------+----------------+----------------+---------------------------------+------------+-------------+
|     ARCH     | CONTEXT SIZE | BATCH SIZE (L / P) | FLASH ATTENTION | MMAP SUPPORT | EMBEDDING ONLY | OFFLOAD LAYERS | FULL OFFLOADED |        UMA (RAM + VRAM)         | NONUMA RAM | NONUMA VRAM |
+--------------+--------------+--------------------+-----------------+--------------+----------------+----------------+----------------+---------------------------------+------------+-------------+
|    qwen2     |     8192     |     2048 / 512     |      false      |     true     |     false      |  25 (24 + 1)   |      Yes       | 65.52 MiB + 96 MiB = 161.52 MiB | 215.52 MiB |  1.02 GiB   |
+--------------+--------------+--------------------+-----------------+--------------+----------------+----------------+----------------+---------------------------------+------------+-------------+
```

### Build Model with other Quantize Type

We can build the model with other quantize types by setting the `QUANTIZE_TYPE` argument.

```shell
$ export QUANTIZE_TYPE="Q4_K_M" 
$ docker build --builder git-lfs --tag ${REPO}/qwen2:0.5b-instruct-$(echo "${QUANTIZE_TYPE}" | tr '[:upper:]' '[:lower:]' | sed 's/_/-/g')-demo --build-arg QUANTIZE_TYPE=${QUANTIZE_TYPE} --load --push $(pwd)
```

With build cache, the total build time will be reduced.

### Pull Model from Container Image Registry

We can pull the published models from the Docker registry by `gguf-packer`.

```shell
$ gguf-packer pull ${REPO}/qwen2:0.5b-instruct-q5-k-m-demo
$ gguf-packer pull ${REPO}/qwen2:0.5b-instruct-q4-k-m-demo
$ gguf-packer list

      NAME                  TAG                  ID       ARCH    PARAMS     BPW          TYPE          CREATED         SIZE     
  ${REPO}/qwen2  0.5b-instruct-q4-k-m-demo  a0d46ab8fd9f  qwen2  494.03 M  6.35 bpw  IQ2_XXS/Q4_K_M  19 minutes ago  379.38 MiB  
  ${REPO}/qwen2  0.5b-instruct-q5-k-m-demo  269bac3c0e20  qwen2  494.03 M  6.71 bpw  IQ3_XXS/Q5_K_M  30 minutes ago  400.62 MiB
```

### Run Model

We can use `gguf-packer` to run a local model
with [ghcr.io/ggerganov/llama.cpp](https://github.com/ggerganov/llama.cpp/pkgs/container/llama.cpp).

```shell
$ gguf-packer run ${REPO}/qwen2:0.5b-instruct-q5-k-m-demo -- --flash-attn
```

Or get the running command with `--dry-run`.

```shell
$ gguf-packer run ${REPO}/qwen2:0.5b-instruct-q5-k-m-demo --dry-run -- --flash-attn

docker run --rm --interactive --tty --privileged --publish 8080:8080 --volume ${GGUF_PACKER_STORE_PATH}/models/layers/sha256/269bac3c0e202559a2e75f88d087df3324f95b6aaf108e9e70e8b8895aaa8561:/gp-849d4691 ghcr.io/ggerganov/llama.cpp:server -m /gp-849d4691/Qwen2-0.5B-Instruct.Q5_K_M.gguf -c 8192 --system-prompt-file /gp-849d4691/system-prompt.txt --chat-template "{% for message in messages %}{{'<|im_start|>' + message['role'] + '\\n' + message['content'] + '<|im_end|>' + '\\n'}}{% endfor %}{% if add_generation_prompt %}{{ '<|im_start|>assistant\\n' }}{% endif %}" --flash-attn --host 0.0.0.0
```

### Refer Model

Since the `${REPO}/qwen2:0.5b-instruct-q5-k-m-demo` is a standard OCI Artifact, we can refer it via `FROM` in other
Dockerfiles.

Let's build a model based on [Ubuntu:22.04](https://hub.docker.com/_/ubuntu/tags).

```shell
$ docker build --builder git-lfs --tag ${REPO}/qwen2:0.5b-instruct-q5-k-m-demo2 --build-arg BASE=ubuntu:22.04 --load --push $(pwd)
```

Create a `Dockerfile.infer` file with below content.

```dockerfile
# syntax=docker/dockerfile:1.7-labs
ARG  REPO=""
FROM ${REPO}/qwen2:0.5b-instruct-q5-k-m-demo2
RUN  apt-get update && \
     apt-get install -y libcurl4-openssl-dev libgomp1 curl 
ENV LC_ALL=C.utf8
COPY --from=ghcr.io/ggerganov/llama.cpp:server /llama-server /
ENTRYPOINT [ "/llama-server" ]
# reuse model file and system prompt file from the base image
CMD ["-m", "Qwen2-0.5B-Instruct.Q5_K_M.gguf", "-c", "8192", "--system-prompt-file", "system-prompt.txt"]
```

- `ARG REPO=...`: The repository of the model image.
- `FROM ...`: The base image for the build.
- `RUN ...`: Install the dependencies.
- `ENV ...`: Set the local.
- `COPY --from=... ...`: Copy the llama-server binary from the llama.cpp
  image.
- `ENTRYPOINT ...`: Specify the default commands.
- `CMD ...`: Specify the default commands.

Build the inference image.

```shell
$ docker build --builder git-lfs --tag ${REPO}/qwen2:0.5b-instruct-q5-k-m-demo2-infer --build-arg REPO=${REPO} --file Dockerfile.infer --load $(pwd) 
```

Run the inference image.

```shell
$ docker run --rm --interactive --tty ${REPO}/qwen2:0.5b-instruct-q5-k-m-demo2-infer
```

## GGUFPackerfile

`GGUFPackerfile` is the preferred file name of the GGUF Packer frontend. It can be simply understood that when a
`Dockerfile` is added with a specific syntax, this `Dockerfile` is equivalent to `GGUFPackerfile`.

|              | Dockerfile                                                   | GGUFPackerfile                                                                                                                        |
|--------------|--------------------------------------------------------------|---------------------------------------------------------------------------------------------------------------------------------------|
|              | # syntax=thxcode/gguf-packer:latest<br/>FROM scratch<br/>... | <br/>FROM scratch<br/>...                                                                                                             |
| docker build | docker build \ <br/> --tag ${TAG} $(pwd) <br/> <br/> <br/>   | docker build \ <br/> --build-arg BUILDKIT_SYNTAX=thxcode/gguf-packer:latest \ <br/> --file GGUFPackerfile \ <br/> --tag ${TAG} $(pwd) |                                  

See [examples](./examples) for more details.

## Overview

GGUF Packer is a high-level languages have been implemented
for [BuildKit LLB](https://github.com/moby/buildkit?tab=readme-ov-file#exploring-llb), which supports the following
instructions:

| Instruction             | Description                                                                                      |
|-------------------------|--------------------------------------------------------------------------------------------------|
| [`ADD`](#add)           | Add local or remote files and directories.                                                       |
| [`ARG`](#arg)           | Use build-time variables. <br/> Allow one GGUFPackerfile to build various models.                |
| [`CAT`](#cat)           | Concatenate content to a file. <br/> Be able to create system prompt by hand.                    |
| [`CMD`](#cmd)           | Specify default commands. <br/> Declare the main model, drafter, multimodal projector and so on. |
| [`COPY`](#copy)         | Copy files and directories.                                                                      |
| [`CONVERT`](#convert)   | Convert safetensors model files to a GGUF model file.                                            |
| [`FROM`](#from)         | Set the base image for the build.                                                                |
| [`LABEL`](#label)       | Add metadata to an image.                                                                        |
| [`QUANTIZE`](#quantize) | Quantize a GGUF file.                                                                            |

### Format

The format follows the definition
of [Dockerfile](https://github.com/moby/buildkit/blob/master/frontend/dockerfile/docs/reference.md#format), here is an
example:

```dockerfile
# Comment
INSTRUCTION arguments
```

BuildKit treats lines that begin with `#` as a comment, unless the line is a
valid [parser directive](https://github.com/moby/buildkit/blob/master/frontend/dockerfile/docs/reference.md#parser-directives).
When using `Dockerfile` file, A `# syntax=thxcode/gguf-packer:latest` must add to the top of the file,
see [Usage](#usage).

### Instructions

#### ADD

The `ADD` instruction follows the definition
of [Dockerfile](https://github.com/moby/buildkit/blob/master/frontend/dockerfile/docs/reference.md#add),
which be able to add a quantified model file to the image or copy an original model from Hugging Face.

```dockerfile
# syntax=thxcode/gguf-packer:latest

# add from http
ADD https://huggingface.co/QuantFactory/Qwen2-0.5B-Instruct-GGUF/resolve/main/Qwen2-0.5B-Instruct.Q5_K_M.gguf /app/Qwen2-0.5B-Instruct.Q5_K_M.gguf

# add from git repository
ADD https://huggingface.co/Qwen/Qwen2-0.5B-Instruct.git /app/Qwen2-0.5B-Instruct
```

##### Available Options

- `ADD [--keep-git-dir=<boolean>] <src> ... <dir>`
- `ADD [--checksum=<hash>] <src> ... <dir>`
- `ADD [--chown=<user>:<group>] [--chmod=<perms> ...] <src> ... <dest>`
- `ADD [--link[=<boolean>]] <src> ... <dest>`
- `ADD [--exclude=<path> ...] <src> ... <dest>`

The available options are the same as the Dockerfile's `ADD` instruction, please retrieve the information from the
Dockerfile's guide.

#### ARG

The `ARG` instruction follows the definition
of [Dockerfile](https://github.com/moby/buildkit/blob/master/frontend/dockerfile/docs/reference.md#arg),
which be able to use build-time variables.

```dockerfile
# syntax=thxcode/gguf-packer:latest

ARG REPO=QuantFactory
ARG MODEL=Qwen2-0.5B-Instruct
ARG QUANTIZE_TYPE=Q5_K_M

ADD https://huggingface.co/${REPO}/${MODEL}-GGUF/resolve/main/${MODEL}.${QUANTIZE_TYPE}.gguf /app/${MODEL}.${QUANTIZE_TYPE}.gguf
```

GGUFPackerfile supports global `ARG`s, which means you can use the same `ARG` in multiple stages.

#### CAT

The `CAT` instruction allows you to concatenate content to a file.

```dockerfile
# syntax=thxcode/gguf-packer:latest

CAT <<EOF /app/system-prompt.txt
You are an AI assistant that follows instruction extremely well. Help as much as you can.
In answering questions, follow these steps:
1. Entity Identification: Identify the main entities involved in the query. Entities can include people, objects, events, or abstract concepts.
2. Relationship Comprehension: Determine the relationships between these entities. Relationships may be explicit in the text or may need to be inferred based on context and general knowledge.
3. Implication Understanding: Understand the general implications of these relationships. These implications can be based on established facts, principles, or rules related to the identified relationships.
4. Question Contextualization: Align the implications of the relationships with the context of the query. This alignment should guide your response to the query.
5. Answer Generation: Based on the understanding of the entities, their relationships, and implications, generate an appropriate response to the query.
EOF

# llama-server like application's arguments
CMD ["-c", "8192", "--system-prompt-file", "/app/system-prompt.txt"]
```

#### CMD

The `CMD` instruction follows the definition
of [Dockerfile](https://github.com/moby/buildkit/blob/master/frontend/dockerfile/docs/reference.md#cmd), but only
support exec forms.

```dockerfile
# syntax=thxcode/gguf-packer:latest

ARG CHAT_TEMPLATE="{% for message in messages %}{{'<|im_start|>' + message['role'] + '\n' + message['content'] + '<|im_end|>' + '\n'}}{% endfor %}{% if add_generation_prompt %}{{ '<|im_start|>assistant\n' }}{% endif %}"

CMD ["-m", "/app/Qwen2-0.5B-Instruct.Q5_K_M.gguf", "-c", "8192", "--system-prompt-file", "/app/system-prompt.txt", "--chat-template", "${CHAT_TEMPLATE}"]
```

#### COPY

The `COPY` instruction follows the definition
of [Dockerfile](https://github.com/moby/buildkit/blob/master/frontend/dockerfile/docs/reference.md#copy).

```dockerfile
# syntax=thxcode/gguf-packer:latest

# copy from local
COPY Qwen2-0.5B-Instruct.Q5_K_M.gguf /app/

# copy from other stage
COPY --from=original /app/Qwen2-0.5B-Instruct.Q5_K_M.gguf /app/
```

##### Available Options

- `COPY [--from=<image|stage|context>] <src> ... <dest>`
- `COPY [--parents[=<boolean>]] <src> ... <dest>`
- `COPY [--chown=<user>:<group>] [--chmod=<perms> ...] <src> ... <dest>`
- `COPY [--link[=<boolean>]] <src> ... <dest>`
- `COPY [--exclude=<path> ...] <src> ... <dest>`

The available options are the same as the Dockerfile's `COPY` instruction, please retrieve the information from the
Dockerfile's guide.

#### CONVERT

The `CONVERT` instruction allows you to convert safetensors model files to a GGUF model file.

```dockerfile
# syntax=thxcode/gguf-packer:latest

ADD     https://huggingface.co/Qwen/Qwen2-0.5B-Instruct.git /app/Qwen2-0.5B-Instruct

# convert safetensors model files from current stage
CONVERT --type=F16 /app/Qwen2-0.5B-Instruct /app/Qwen2-0.5B-Instruct.F16.gguf

# convert from other stage
CONVERT --from=other-stage --type=F16 /app/Qwen2-0.5B-Instruct /app/Qwen2-0.5B-Instruct.F16.gguf

# convert from build context
CONVERT --from=context --type=F16 /app/Qwen2-0.5B-Instruct /app/Qwen2-0.5B-Instruct.F16.gguf
```

##### Available Options

- `CONVERT [--from=<image|stage|context>] <src> ... <dest>`, convert safetensors model files to GGUF model file from
  image, other stage or context, where places `<src>`.
- `CONVERT [--type=<type>] <src> <dest>`,
  see [llama.cpp/convert_hf_to_gguf.py](https://github.com/ggerganov/llama.cpp/blob/01245f5b1629075543bc4478418c7d72a0b4b3c7/convert_hf_to_gguf.py#L3553-L3556).

#### FROM

The `FROM` instruction follows the definition
of [Dockerfile](https://github.com/moby/buildkit/blob/master/frontend/dockerfile/docs/reference.md#from).

```dockerfile
# syntax=thxcode/gguf-packer:latest

FROM scratch 

# reference another model
FROM gpustack/qwen2:0.5b-instruct-q5-k-m
```

#### LABEL

The `LABEL` instruction follows the definition
of [Dockerfile](https://github.com/moby/buildkit/blob/master/frontend/dockerfile/docs/reference.md#label).

```dockerfile
# syntax=thxcode/gguf-packer:latest

LABEL org.opencontainers.image.title="Qwen2-0.5B-Instruct" \
      org.opencontainers.image.description="Qwen2 0.5B Instruct model" \
      org.opencontainers.image.url="https://huggingface.co/Qwen/Qwen2-0.5B-Instruct" \
      org.opencontainers.image.source="https://huggingface.co/Qwen/Qwen2-0.5B-Instruct"
```

##### Export Labels

Since GGUF format model files will
record [the general metadata](https://github.com/ggerganov/ggml/blob/master/docs/gguf.md#general-metadata), GGUF Packer
can retrieve these values and export them as labels.

- `gguf.model.architecture`: The architecture of the model.
- `gguf.model.parameters`: The parameters of the model.
- `gguf.model.bpw`: The bits per weight of the model.
- `gguf.model.filetype`: The file type of the model.
- `gguf.model.name`: The name of the model, if specified.
- `gguf.model.vendor`: The vendor of the model, if specified.
- `gguf.model.authors`: The authors of the model, if specified.
- `gguf.model.url`: The URL of the model, if specified.
- `gguf.model.description`: The description of the model, if specified.
- `gguf.model.licenses`: The licenses of the model, if specified.
- `gguf.model.usage`: The usage of the model, default is `text-to-text`.

All labels can be overridden by the Dockerfile/GGUFPackerfile.

#### QUANTIZE

The `QUANTIZE` instruction allows you to quantize a GGUF file.

```dockerfile
# syntax=thxcode/gguf-packer:latest

# quantize a GGUF file from current stage
CONVERT  --type=F16 /app/Qwen2-0.5B-Instruct /app/Qwen2-0.5B-Instruct.F16.gguf
QUANTIZE --type=Q5_K_M /app/Qwen2-0.5B-Instruct.F16.gguf /app/Qwen2-0.5B-Instruct.Q5_K_M.gguf

# quantize from other stage
QUANTIZE --from=other-stage --type=Q5_K_M /app/Qwen2-0.5B-Instruct.F16.gguf /app/Qwen2-0.5B-Instruct.Q5_K_M.gguf

# quantize from build context
QUANTIZE --from=context --type=Q5_K_M /app/Qwen2-0.5B-Instruct.F16.gguf /app/Qwen2-0.5B-Instruct.Q5_K_M.gguf
```

##### Available Options

- `QUANTIZE [--from=<image|stage|context>] <src> ... <dest>`, quantize a GGUF file from current stage, other stage or
  context, where places `<src>`.
- `QUANTIZE [--type=<type>] [--imatrix=<path>] [--include-weights=...] [--exclude-weights=...] [--leave-output-tensor] [--pure] [--output-tensor-type=<GGML Type>] [--token-embedding-type=<GGML Type>] <src> ... <dest>`,
  see [llama.cpp/quantize.cpp](https://github.com/ggerganov/llama.cpp/blob/15fa07a5c564d3ed7e7eb64b73272cedb27e73ec/examples/quantize/quantize.cpp#L98-L121).

## Motivation

In Large Language Model (LLM) world, there are three popular
projects: [GGML](https://github.com/ggerganov/ggml), [LLaMa.cpp](https://github.com/ggerganov/llama.cpp)
and [Ollama](https://github.com/ollama/ollama). LLaMa.cpp is based on GGML, and Ollama is based on LLaMa.cpp.

GGML provides another option for engineers who don't like Python (many people fall on issues such as environment
configuration, regional dependence and installation compilation): a tensor computing library based on C/C++. With the
model file quantized by GGML, that is, the [GGUF](https://github.com/ggerganov/ggml/blob/master/docs/gguf.md) format
file mentioned above, GGML can give edge devices the opportunity to run LLM.

LLaMa.cpp, based on GGML, it encapsulates various popular LLM architectures. With its famous example,
the [llama-server](https://github.com/ggerganov/llama.cpp/blob/master/examples/server/README.md), you can easily to own
an OpenAI GPT like service on your laptop.

To own a Chat LLM service, there are many parameters to be set, and the model file is not easy to be managed.
Ollama provides a solution to this problem, which uses
a [Model File](https://github.com/ollama/ollama/blob/main/docs/modelfile.md) to deliver the model file and parameters.

Ollama Model File is similar to Dockerfile, allows you to build an Ollama model and distribute it. But, the Ollama model
is not friendly to the CloudNative world. Let's dig into why.

### Docker Image

This is a famous DockerHub registry, [alpine](https://hub.docker.com/_/alpine/tags).

[![](./docs/assets/dockerhub-alpine-latest.jpg)](https://hub.docker.com/layers/library/alpine/latest/images/sha256-eddacbc7e24bf8799a4ed3cdcfa50d4b88a323695ad80f317b6629883b2c2a78?context=explore)

We can use [crane](https://github.com/google/go-containerregistry/blob/main/cmd/crane/README.md) to retrieve its
manifest.

```shell
$ crane manifest docker.io/library/alpine:latest | jq .

{
  "manifests": [
    {
      "digest": "sha256:eddacbc7e24bf8799a4ed3cdcfa50d4b88a323695ad80f317b6629883b2c2a78",
      "mediaType": "application/vnd.docker.distribution.manifest.v2+json",
      "platform": {
        "architecture": "amd64",
        "os": "linux"
      },
      "size": 528
    },
    {
      "digest": "sha256:5c7e326e3c8a8c51654a6c5d94dac98d7f6fc4b2a762d86aaf67b7e76a6aee46",
      "mediaType": "application/vnd.docker.distribution.manifest.v2+json",
      "platform": {
        "architecture": "arm",
        "os": "linux",
        "variant": "v6"
      },
      "size": 528
    },
    ...
  ],
  "mediaType": "application/vnd.docker.distribution.manifest.list.v2+json",
  "schemaVersion": 2
}
```

We can see that the `mediaType` of `alpine:latest` image manifest
is `application/vnd.docker.distribution.manifest.list.v2+json`,
which means it's a manifest list that contains multiple platforms. For [OCI](https://opencontainers.org/) compatibility,
the `mediaType`
is [`application/vnd.oci.image.index.v1+json`](https://github.com/opencontainers/image-spec/blob/main/media-types.md#applicationvndociimageindexv1json).

Next, we go ahead to dig out the `alpine:latest`'s `linux/amd64` platform manifest.

```shell
$ crane manifest docker.io/library/alpine@sha256:eddacbc7e24bf8799a4ed3cdcfa50d4b88a323695ad80f317b6629883b2c2a78 | jq .

{
  "schemaVersion": 2,
  "mediaType": "application/vnd.docker.distribution.manifest.v2+json",
  "config": {
    "mediaType": "application/vnd.docker.container.image.v1+json",
    "size": 1471,
    "digest": "sha256:324bc02ae1231fd9255658c128086395d3fa0aedd5a41ab6b034fd649d1a9260"
  },
  "layers": [
    {
      "mediaType": "application/vnd.docker.image.rootfs.diff.tar.gzip",
      "size": 3622892,
      "digest": "sha256:c6a83fedfae6ed8a4f5f7cbb6a7b6f1c1ec3d86fea8cb9e5ba2e5e6673fde9f6"
    }
  ]
}
``` 

We can see that the `mediaType` of `alpine:latest(linux/amd64)` image manifest
is `application/vnd.docker.distribution.manifest.v2+json`,
for [OCI](https://opencontainers.org/) compatibility, it
is [`application/vnd.oci.image.manifest.v1+json`](https://github.com/opencontainers/image-spec/blob/main/media-types.md#applicationvndociimagemanifestv1json).

In this manifest, a special field `config` is a reference to the image configuration, which is a JSON object that
contains the image's configuration.

```shell
$ crane blob docker.io/library/alpine@sha256:324bc02ae1231fd9255658c128086395d3fa0aedd5a41ab6b034fd649d1a9260 | jq .

{
  "architecture": "amd64",
  "config": {
    "Hostname": "",
    "Domainname": "",
    "User": "",
    "AttachStdin": false,
    "AttachStdout": false,
    "AttachStderr": false,
    "Tty": false,
    "OpenStdin": false,
    "StdinOnce": false,
    "Env": [
      "PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"
    ],
    "Cmd": [
      "/bin/sh"
    ],
    "Image": "sha256:3e25db883ea289c8b0d3006e7a6a03c56be09c50f03f6b228ba2fe74fd8194d2",
    "Volumes": null,
    "WorkingDir": "",
    "Entrypoint": null,
    "OnBuild": null,
    "Labels": null
  },
  "container": "fc33bc50844a0d6cdfc682fcf85647ec60783decbd3850e58ab7e013ef892cfb",
  "container_config": {...},
  "created": "2024-07-22T22:26:43.778747613Z",
  "docker_version": "23.0.11",
  "history": [...],
  "os": "linux",
  "rootfs": {...}
}
```

> Note: you can also
> use `crane config docker.io/library/alpine@sha256:eddacbc7e24bf8799a4ed3cdcfa50d4b88a323695ad80f317b6629883b2c2a78` to
> get the above configuration.

The configuration represents the detail of this image, including
the `architecture`, `os`, `config`, `created`, `history` and so on. There is also a `config` field in this
configuration, which is a JSON object that contains the running detail of the image.

### OCI Distribution

[OCI Distribution Specification](https://github.com/opencontainers/distribution-spec/blob/main/spec.md) is a
specification for an image registry, which is a server that stores and serves images. Those implementations implemented
this distribution-spec are called OCI Registries,
like [Docker Registry](https://docs.docker.com/registry/), [GitHub Container Registry](https://docs.github.com/en/packages/guides/about-github-container-registry), [Harbor](https://goharbor.io/), [Quay](https://www.quay.io/), [Azure Container Registry](https://azure.microsoft.com/en-us/services/container-registry/), [Google Container Registry](https://cloud.google.com/container-registry)
and so on.

At begin, we only store the Container Image in the OCI Registry, but now, we can store things that respected
the [OCI Artifacts Specification](https://github.com/opencontainers/artifacts) in the OCI Registry. Helm chart is a good
example.

People initially put chart packages in [Git repositories](https://github.com/rancher/charts)
or [released them in some way](https://github.com/prometheus-community/helm-charts/releases). Later, Helm
supported [distributing chart packages](https://helm.sh/docs/topics/registries/) in the form of OCI Artifact. This
changed the operation and maintenance work from managing OCI registry and Git repositories to only managing OCI
registry.

We can use crane to read a famous Helm chart package in the OCI
Registry, [Argo CD](https://github.com/argoproj/argo-helm/pkgs/container/argo-helm%2Fargo-cd).

```shell
$ crane manifest ghcr.io/argoproj/argo-helm/argo-cd:7.3.11 | jq .

{
  "schemaVersion": 2,
  "config": {
    "mediaType": "application/vnd.cncf.helm.config.v1+json",
    "digest": "sha256:42242c5441612b0cedb4cfc87ad5c257ec062ff6fab8c27557a072739eff0d71",
    "size": 940
  },
  "layers": [
    {
      "mediaType": "application/vnd.cncf.helm.chart.provenance.v1.prov",
      "digest": "sha256:2920df17e16b736156075e5859b7cb09d127d91bcaefdafd63860fb775609df9",
      "size": 1870
    },
    {
      "mediaType": "application/vnd.cncf.helm.chart.content.v1.tar+gzip",
      "digest": "sha256:4249ea76c915bb04f4dda095e608004c08f13a7d0e0da2d1836ffc57a8592f7b",
      "size": 168713
    }
  ]
}
```

Not as the `apline:latest` we saw before, there is no `mediaType` field in the `argo-cd:7.3.11` manifest. Since this
field is no a REQUIRED property, it's OK. When we try to pull this chart by Docker CLI, it will be failed.

```shell
$ docker pull ghcr.io/argoproj/argo-helm/argo-cd:7.3.11
7.3.11: Pulling from argoproj/argo-helm/argo-cd
unsupported media type application/vnd.cncf.helm.config.v1+json
```

Although the Docker CLI can recognize manifest of an OCI Artifact like what `crane` do, it will not solve
the `config.mediaType` doesn't match `application/vnd.docker.container.image.v1+json`
or `application/vnd.oci.image.config.v1+json`.

So we can use `crane`, Helm CLI or [ORAS](https://oras.land/docs/commands/oras_pull) to pull the chart package.

```shell
$ crane pull ghcr.io/argoproj/argo-helm/argo-cd:7.3.11 argo-cd.tar

$ tar xf argo-cd.tar

$ ls -alth .
total 760
drwxr-xr-x   7 gpustack  wheel   224B Jul 26 13:26 .
drwxrwxrwt  53 root     wheel   1.7K Jul 26 13:26 ..
-rw-r--r--@  1 gpustack  wheel   172K Jul 26 13:23 argo-cd.tar
-rw-r--r--   1 gpustack  wheel   1.8K Jan  1  1970 2920df17e16b736156075e5859b7cb09d127d91bcaefdafd63860fb775609df9.tar.gz
-rw-r--r--   1 gpustack  wheel   165K Jan  1  1970 4249ea76c915bb04f4dda095e608004c08f13a7d0e0da2d1836ffc57a8592f7b.tar.gz
-rw-r--r--   1 gpustack  wheel   302B Jan  1  1970 manifest.json
-rw-r--r--   1 gpustack  wheel   940B Jan  1  1970 sha256:42242c5441612b0cedb4cfc87ad5c257ec062ff6fab8c27557a072739eff0d71
```

### Ollama Model

Now, let's take a look at the Ollama Model. This is the LLaMa3.1:8B model on Ollama Models.

[![](./docs/assets/ollama-llama3.1-8b.jpg)](https://ollama.com/library/llama3.1)

At first, we thought it was also a standard OCI Registry, so we tried to get its manifest by crane.

```shell
$ crane manifest ollama.com/library/llama3.1:8b | jq .

Error: fetching manifest ollama.com/library/llama3.1:8b: GET https://ollama.com/v2/: unexpected status code 404 Not Found: 404 page not found
```

Em, isn't it a standard OCI Registry?

```shell
$ curl https://ollama.com/v2/library/llama3.1/manifests/8b | jq .

{
  "schemaVersion": 2,
  "mediaType": "application/vnd.docker.distribution.manifest.v2+json",
  "config": {
    "digest": "sha256:e711233e734332fe5f8a09b2407fb5a083e39ca7e0ba90788026414cd4c059af",
    "mediaType": "application/vnd.docker.container.image.v1+json",
    "size": 485
  },
  "layers": [
    {
      "digest": "sha256:87048bcd55216712ef14c11c2c303728463207b165bf18440b9b84b07ec00f87",
      "mediaType": "application/vnd.ollama.image.model",
      "size": 4661211808
    },
    {
      "digest": "sha256:8cf247399e57085e6b34c345ebea38c1aa3e2b25c8294eecb746dd7b01dd9079",
      "mediaType": "application/vnd.ollama.image.template",
      "size": 1692
    },
    {
      "digest": "sha256:f1cd752815fcf68c3c2e73b2b00b5396c5dffb9eebe49567573f275f9ec85fcd",
      "mediaType": "application/vnd.ollama.image.license",
      "size": 12321
    },
    {
      "digest": "sha256:56bb8bd477a519ffa694fc449c2413c6f0e1d3b1c88fa7e3c9d88d3ae49d4dcb",
      "mediaType": "application/vnd.ollama.image.params",
      "size": 96
    }
  ]
}

```

Well, perhaps we can use `crane` to pull its `config` file.

```shell
$ crane blob ollama.com/library/llama3.1@sha256:e711233e734332fe5f8a09b2407fb5a083e39ca7e0ba90788026414cd4c059af | jq .

Error: pulling layer ollama.com/library/llama3.1@sha256:e711233e734332fe5f8a09b2407fb5a083e39ca7e0ba90788026414cd4c059af: GET https://ollama.com/v2/: unexpected status code 404 Not Found: 404 page not found
```

Ops, we cannot. OKay, it's not a standard OCI Registry.

```shell
$ curl https://ollama.com/v2/library/llama3.1/blobs/sha256:e711233e734332fe5f8a09b2407fb5a083e39ca7e0ba90788026414cd4c059af

<a href="https://dd20bb891979d25aebc8bec07b2b3bbc.r2.cloudflarestorage.com/ollama/docker/registry/v2/blobs/sha256/e7/e711233e734332fe5f8a09b2407fb5a083e39ca7e0ba90788026414cd4c059af/data?X-Amz-Algorithm=AWS4-HMAC-SHA256&amp;X-Amz-Credential=66040c77ac1b787c3af820529859349a%2F20240725%2Fauto%2Fs3%2Faws4_request&amp;X-Amz-Date=20240725T145758Z&amp;X-Amz-Expires=86400&amp;X-Amz-SignedHeaders=host&amp;X-Amz-Signature=262290faed14709a9c0faf9f8b8d567d3fec84731b2877852d275bc979f0530f">Temporary Redirect</a>.

$ curl -L https://ollama.com/v2/library/llama3.1/blobs/sha256:e711233e734332fe5f8a09b2407fb5a083e39ca7e0ba90788026414cd4c059af

{"model_format":"gguf","model_family":"llama","model_families":["llama"],"model_type":"8.0B","file_type":"Q4_0","architecture":"amd64","os":"linux","rootfs":{"type":"layers","diff_ids":["sha256:87048bcd55216712ef14c11c2c303728463207b165bf18440b9b84b07ec00f87","sha256:11ce4ee3e170f6adebac9a991c22e22ab3f8530e154ee669954c4bc73061c258","sha256:f1cd752815fcf68c3c2e73b2b00b5396c5dffb9eebe49567573f275f9ec85fcd","sha256:56bb8bd477a519ffa694fc449c2413c6f0e1d3b1c88fa7e3c9d88d3ae49d4dcb"]}}
```

### Conclusion

With Ollama model, Ollama become the most popular tool to manage the LLM distribution, but it's not a standard OCI
Artifact. **The distribution mode of Ollama model is not a good practice**.

Helm chart packages are usually very small, and the network cost of deploying OCI in a nearby network is very low, so
we don't need to disguise the Helm chart as a Container Image.

However, the model files are huge. For example, the LLaMa3.1:8B model above is 4.3GB. If the same method as the
Helm chart is used, the network cost will be very high. BTW, **Ollama has not yet compressed the Model(File)s**, which
further slows down the delivery process.

Using `ollama pull` to keep the Ollama model in an OCI Artifact is another **waste of storage and network
resources**.

[![](./docs/assets/dockerhub-ollama-model-cache.jpg)](https://hub.docker.com/search?q=ollama)

Now, with GGUF Packer, we can build a true OCI Artifact for the LLM distribution. We can
use [docker --volumes-from](https://docs.docker.com/storage/volumes/#back-up-restore-or-migrate-data-volumes) to mount
the model to any LLaMACpp-like service container or build on top of the model.

## License

MIT
