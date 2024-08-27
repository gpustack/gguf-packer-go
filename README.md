# GGUF Packer

> tl;dr, Deliver LLMs of [GGUF](https://github.com/ggerganov/ggml/blob/master/docs/gguf.md) format via Dockerfile.

[![Go Report Card](https://goreportcard.com/badge/github.com/gpustack/gguf-packer-go)](https://goreportcard.com/report/github.com/gpustack/gguf-packer-go)
[![CI](https://img.shields.io/github/actions/workflow/status/gpustack/gguf-packer-go/cmd.yml?label=ci)](https://github.com/gpustack/gguf-packer-go/actions)
[![License](https://img.shields.io/github/license/gpustack/gguf-packer-go?label=license)](https://github.com/gpustack/gguf-packer-go#license)
[![Download](https://img.shields.io/github/downloads/gpustack/gguf-packer-go/total)](https://github.com/gpustack/gguf-packer-go/releases)
[![Docker Pulls](https://img.shields.io/docker/pulls/gpustack/gguf-packer)](https://hub.docker.com/r/gpustack/gguf-packer)
[![Release](https://img.shields.io/github/v/release/gpustack/gguf-packer-go)](https://github.com/gpustack/gguf-packer-go/releases/latest)

[GGUF](https://github.com/ggerganov/ggml/blob/master/docs/gguf.md) is a file format for storing models for inference
with GGML and executors based on GGML. GGUF is a binary format that is designed for fast loading and saving of models,
and for ease of reading. Models are traditionally developed using PyTorch or another framework, and then converted to
GGUF for use in GGML.

GGUF Packer aids in building Large Language Model (LLM) distributions. All you need is [Docker](https://www.docker.com/)
(or [BuildKit daemon](https://github.com/moby/buildkit?tab=readme-ov-file#quick-start)).

## Key Features

- **Efficient Distribution**: GGUF Packer uses the [BuildKit](https://github.com/moby/buildkit) frontend to streamline
  the building of LLM distributions.
- **Docker Integration**: It leverages Docker and BuildKit for seamless build process, allowing the use of Dockerfile
  directly instead of the [Ollama Model File](https://github.com/ollama/ollama/blob/main/docs/modelfile.md).
- **[Cloud-Native](https://www.cncf.io/)  Support**: It aligns with cloud-native practices,
  referencing [KEP-4639 OCI VolumeSource PoC](https://github.com/kubernetes/kubernetes/issues/125463).

## Agenda

- [Quick Start](#quick-start)
    + [Prerequisites](#prerequisites)
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
        * [ADD](#add)
        * [ARG](#arg)
        * [CAT](#cat)
        * [CMD](#cmd)
        * [COPY](#copy)
        * [CONVERT](#convert)
        * [FROM](#from)
        * [LABEL](#label)
        * [QUANTIZE](#quantize)
- [Motivation](#motivation)
    + [Docker Image](#docker-image)
    + [OCI Distribution](#oci-distribution)
    + [Ollama Model](#ollama-model)

## Quick Start

### Prerequisites

Install [Docker](https://docs.docker.com/engine/install/)
and [GGUF Packer](https://github.com/gpustack/gguf-packer-go/releases).

### Write Dockerfile

To get started, create a `Dockefile` file with the following content:

```dockerfile
# syntax=gpustack/gguf-packer:latest

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

The provided `Dockerfile` will build a distribution package for
the [Qwen2-0.5B-Instruct](https://huggingface.co/Qwen/Qwen2-0.5B-Instruct/tree/main) large language model (LLM). The
model has been quantized using the `Q5_K_M` quantization technique, which reduces the model size and inference latency
without significantly impacting accuracy.

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

The `ADD` instruction allows you to clone the model from a Git repository. However, please note that the Git LFS (Large
File Storage) is not yet supported(see [moby/buildkit#5212](https://github.com/moby/buildkit/pull/5212)). To achieve
this functionality, you can use a development version of BuildKit.

First, set up a BuildKit daemon by running the command:

```shell
$ docker buildx create --name "git-lfs" --driver "docker-container" --driver-opt "image=thxcode/buildkit:v0.15.1-git-lfs" --buildkitd-flags "--allow-insecure-entitlement security.insecure --allow-insecure-entitlement network.host" --bootstrap 
```

Next, build and publish your model. By including the `--push` argument, the built model will be automatically published
to the Docker registry:

```shell
$ export REPO="YOUR_REPOSITORY"
$ docker build --builder git-lfs --tag ${REPO}/qwen2:0.5b-instruct-q5-k-m-demo --load --push $(pwd)
```

### Estimate Model Memory Usage

Once the building process is complete, we can utilize `gguf-packer` to estimate the model:

```shell
$ gguf-packer estimate ${REPO}/qwen2:0.5b-instruct-q5-k-m-demo
+-------+--------------+--------------------+-----------------+-----------+----------------+---------------+----------------+----------------+------------------------+----------------------+
|  ARCH | CONTEXT SIZE | BATCH SIZE (L / P) | FLASH ATTENTION | MMAP LOAD | EMBEDDING ONLY | DISTRIBUTABLE | OFFLOAD LAYERS | FULL OFFLOADED |           RAM          |        VRAM 0        |
|       |              |                    |                 |           |                |               |                |                +-----------+------------+-----------+----------+
|       |              |                    |                 |           |                |               |                |                |    UMA    |   NONUMA   |    UMA    |  NONUMA  |
+-------+--------------+--------------------+-----------------+-----------+----------------+---------------+----------------+----------------+-----------+------------+-----------+----------+
| qwen2 |     8192     |     2048 / 512     |     Disabled    |  Enabled  |       No       | Not Supported |   25 (24 + 1)  |       Yes      | 90.94 MiB | 240.94 MiB | 96.58 MiB | 1.03 GiB |
+-------+--------------+--------------------+-----------------+-----------+----------------+---------------+----------------+----------------+-----------+------------+-----------+----------+
```

### Build Model with other Quantize Type

You can build the model using various quantization types by setting the `QUANTIZE_TYPE` argument:

```shell
$ export QUANTIZE_TYPE="Q4_K_M" 
$ docker build --builder git-lfs --tag ${REPO}/qwen2:0.5b-instruct-$(echo "${QUANTIZE_TYPE}" | tr '[:upper:]' '[:lower:]' | sed 's/_/-/g')-demo --build-arg QUANTIZE_TYPE=${QUANTIZE_TYPE} --load --push $(pwd)
```

With build cache, the total build time will be reduced.

### Pull Model from Container Image Registry

You can retrieve the published models from the Docker registry using `gguf-packer`:

```shell
$ gguf-packer pull ${REPO}/qwen2:0.5b-instruct-q5-k-m-demo
$ gguf-packer pull ${REPO}/qwen2:0.5b-instruct-q4-k-m-demo
$ gguf-packer list

      NAME                  TAG                  ID       ARCH    PARAMS     BPW          TYPE          CREATED         SIZE     
  ${REPO}/qwen2  0.5b-instruct-q4-k-m-demo  a0d46ab8fd9f  qwen2  494.03 M  6.35 bpw  IQ2_XXS/Q4_K_M  19 minutes ago  379.38 MiB  
  ${REPO}/qwen2  0.5b-instruct-q5-k-m-demo  269bac3c0e20  qwen2  494.03 M  6.71 bpw  IQ3_XXS/Q5_K_M  30 minutes ago  400.62 MiB
```

### Run Model

To run a local model
using [ghcr.io/ggerganov/llama.cpp](https://github.com/ggerganov/llama.cpp/pkgs/container/llama.cpp), you can utilize
the gguf-packer:

```shell
$ gguf-packer run ${REPO}/qwen2:0.5b-instruct-q5-k-m-demo -- --flash-attn
```

You can preview the running command by using the `--dry-run` option:

```shell
$ gguf-packer run ${REPO}/qwen2:0.5b-instruct-q5-k-m-demo --dry-run -- --flash-attn

docker run --rm --interactive --tty --privileged --publish 8080:8080 --volume ${GGUF_PACKER_STORE_PATH}/models/layers/sha256/269bac3c0e202559a2e75f88d087df3324f95b6aaf108e9e70e8b8895aaa8561:/gp-849d4691 ghcr.io/ggerganov/llama.cpp:server -m /gp-849d4691/Qwen2-0.5B-Instruct.Q5_K_M.gguf -c 8192 --system-prompt-file /gp-849d4691/system-prompt.txt --chat-template "{% for message in messages %}{{'<|im_start|>' + message['role'] + '\\n' + message['content'] + '<|im_end|>' + '\\n'}}{% endfor %}{% if add_generation_prompt %}{{ '<|im_start|>assistant\\n' }}{% endif %}" --flash-attn --host 0.0.0.0
```

### Refer Model

Since the `${REPO}/qwen2:0.5b-instruct-q5-k-m-demo` is a standard OCI Artifact, you can refer it using the `FROM`
instruction in other Dockerfiles.

You can rebuild a model based on [Ubuntu:22.04](https://hub.docker.com/_/ubuntu/tags).

```shell
$ docker build --builder git-lfs --tag ${REPO}/qwen2:0.5b-instruct-q5-k-m-demo2 --build-arg BASE=ubuntu:22.04 --load --push $(pwd)
```

To proceed, create a file named `Dockerfile.infer` with the following content:

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

Once the `Dockerfile.infer` is created, you can build the container image using the following command:

```shell
$ docker build --builder git-lfs --tag ${REPO}/qwen2:0.5b-instruct-q5-k-m-demo2-infer --build-arg REPO=${REPO} --file Dockerfile.infer --load $(pwd) 
```

And, you can run the built image with `docker run`:

```shell
$ docker run --rm --interactive --tty ${REPO}/qwen2:0.5b-instruct-q5-k-m-demo2-infer
```

## GGUFPackerfile

`GGUFPackerfile` is the preferred file name of the GGUF Packer frontend. It can be simply understood that when a
`Dockerfile` is added with a specific syntax, this `Dockerfile` is equivalent to `GGUFPackerfile`.

<table>
<thead>
<tr>
<th> </th>
<th> Command </th>
<th> Content </th>
</tr>
</thead>
<tbody>
<tr>
<td> 

**Dockerfile**

</td>
<td> 

``` shell
$ docker build --tag ${TAG}
```

</td>
<td>

```dockerfile
# syntax=gpustack/gguf-packer:latest
FROM scratch
```

</td>
</tr>
<tr>
<td> 

**GGUFPackerfile**

</td>
<td> 

```
$ docker build --tag ${TAG} \
    --build-arg BUILDKIT_SYNTAX=gpustack/gguf-packer:latest \
    --file GGUFPackerfile 
```

</td>
<td>

``` dockerfile
FROM scratch
```

</td>
</tr>
</tbody>
</table>

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
When using `Dockerfile` file, A `# syntax=gpustack/gguf-packer:latest` must add to the top of the file,
see [Usage](#usage).

### Instructions

#### ADD

The `ADD` instruction copies new files or directories from `<src>` and adds them to the filesystem of the image at the
path `<dest>`. Files and directories can be copied from the build context, a remote URL, or a Git repository.

```dockerfile
# syntax=gpustack/gguf-packer:latest

# add from http
ADD https://huggingface.co/QuantFactory/Qwen2-0.5B-Instruct-GGUF/resolve/main/Qwen2-0.5B-Instruct.Q5_K_M.gguf /app/Qwen2-0.5B-Instruct.Q5_K_M.gguf

# add from git repository
ADD https://huggingface.co/Qwen/Qwen2-0.5B-Instruct.git /app/Qwen2-0.5B-Instruct
```

##### Available Options

- `ADD [--keep-git-dir=<boolean>] <src> ... <dir>`, preserve the `.git` directory when adding from a Git repository.
- `ADD [--checksum=<hash>] <src> ... <dir>`, only support HTTP/HTTPS URLs, the checksum is formatted
  as <algorithm>:<hash>. The supported algorithms are sha256, sha384, and sha512.
- `ADD [--chown=<user>:<group>] [--chmod=<perms> ...] <src> ... <dest>`,
  referring [Dockerfile/COPY --chown --chmod](https://github.com/moby/buildkit/blob/master/frontend/dockerfile/docs/reference.md#copy---chown---chmod).
- `ADD [--link[=<boolean>]] <src> ... <dest>`,
  referring [Dockerfile/COPY --link](https://github.com/moby/buildkit/blob/master/frontend/dockerfile/docs/reference.md#copy---link).
- `ADD [--exclude=<path> ...] <src> ... <dest>`,
  referring [Dockerfile/COPY --exclude](https://github.com/moby/buildkit/blob/master/frontend/dockerfile/docs/reference.md#copy---exclude).

#### ARG

The `ARG` instruction defines a variable that users can pass at build-time to the builder with the `docker build`
command using the `--build-arg <varname>=<value>` flag.

```dockerfile
# syntax=gpustack/gguf-packer:latest

ARG REPO=QuantFactory
ARG MODEL=Qwen2-0.5B-Instruct
ARG QUANTIZE_TYPE=Q5_K_M

ADD https://huggingface.co/${REPO}/${MODEL}-GGUF/resolve/main/${MODEL}.${QUANTIZE_TYPE}.gguf /app/${MODEL}.${QUANTIZE_TYPE}.gguf
```

GGUF Packer supports global `ARG`s, which means you can use the same `ARG` in multiple stages.

#### CAT

The `CAT` instruction allows you to concatenate content to a file.

```dockerfile
# syntax=gpustack/gguf-packer:latest

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

The `CMD` instruction sets the command to be executed. There can only be one `CMD` instruction in a Dockerfile. If you
list more than one `CMD`, only the last one takes effect.

```dockerfile
# syntax=gpustack/gguf-packer:latest

ARG CHAT_TEMPLATE="{% for message in messages %}{{'<|im_start|>' + message['role'] + '\n' + message['content'] + '<|im_end|>' + '\n'}}{% endfor %}{% if add_generation_prompt %}{{ '<|im_start|>assistant\n' }}{% endif %}"

CMD ["-m", "/app/Qwen2-0.5B-Instruct.Q5_K_M.gguf", "-c", "8192", "--system-prompt-file", "/app/system-prompt.txt", "--chat-template", "${CHAT_TEMPLATE}"]
```

#### COPY

The `COPY` instruction copies new files or directories from `<src>` and adds them to the filesystem of the image at the
path `<dest>`. Files and directories can be copied from the build context, build stage, named context, or an image.

```dockerfile
# syntax=gpustack/gguf-packer:latest

# copy from local
COPY Qwen2-0.5B-Instruct.Q5_K_M.gguf /app/

# copy from other stage
COPY --from=original /app/Qwen2-0.5B-Instruct.Q5_K_M.gguf /app/
```

##### Available Options

- `COPY [--from=<image|stage|context>] <src> ... <dest>`, by default, the `COPY` instruction copies files from the build
  context. The `COPY --from` flag lets you copy files from an image, a build stage, or a named context instead.
- `COPY [--parents[=<boolean>]] <src> ... <dest>`, preserves parent directories for `<src>` entries.
- `COPY [--chown=<user>:<group>] [--chmod=<perms> ...] <src> ... <dest>`,
  referring [Dockerfile/COPY --chown --chmod](https://github.com/moby/buildkit/blob/master/frontend/dockerfile/docs/reference.md#copy---chown---chmod).
- `COPY [--link[=<boolean>]] <src> ... <dest>`,
  referring [Dockerfile/COPY --link](https://github.com/moby/buildkit/blob/master/frontend/dockerfile/docs/reference.md#copy---link).
- `COPY [--exclude=<path> ...] <src> ... <dest>`,
  referring [Dockerfile/COPY --exclude](https://github.com/moby/buildkit/blob/master/frontend/dockerfile/docs/reference.md#copy---exclude).

#### CONVERT

The `CONVERT` instruction allows you to convert safetensors model files to a GGUF model file.

```dockerfile
# syntax=gpustack/gguf-packer:latest

# convert safetensors model files from current stage
ADD     https://huggingface.co/Qwen/Qwen2-0.5B-Instruct.git /app/Qwen2-0.5B-Instruct
CONVERT --type=F16 /app/Qwen2-0.5B-Instruct /app/Qwen2-0.5B-Instruct.F16.gguf

# convert from other stage
CONVERT --from=other-stage --type=F16 /app/Qwen2-0.5B-Instruct /app/Qwen2-0.5B-Instruct.F16.gguf

# convert from build context
CONVERT --from=context --type=F16 /app/Qwen2-0.5B-Instruct /app/Qwen2-0.5B-Instruct.F16.gguf

# convert a PEFT LoRA adapter to GGUF file
ADD        https://huggingface.co/inflaton/Qwen2-1.5B-MAC-lora.git Qwen2-1.5B-MAC-lora
ADD        https://huggingface.co/Qwen/Qwen2-1.5B.git Qwen2-1.5B
CONVERT    --type=F16 --class=lora --base=Qwen2-1.5B Qwen2-1.5B-MAC-lora Qwen2-1.5B-MAC-lora.F16.gguf
```

##### Available Options

- `CONVERT [--from=<image|stage|context>] <src> <dest>`, by default, the `CONVERT` instruction converts file from the
  build context. The `CONVERT --from` flag lets you convert file from an image, a build stage, or a named context
  instead.
- `CONVERT [--class=<model|lora>] <src> <dest>`, specify the class for the model, default is `model`.
    + `CONVERT --class=lora --base=<path> <src> <dest>`, convert a PEFT LoRA adapter to GGUF file, must provide the
      `base` model.
- `CONVERT [--type=<type>] <src> <dest>`, specify the output type for `<dest>`, select from `F32`, `F16`, `BF16` and
  `Q8_0`, default is `F16`.

#### FROM

The `FROM` instruction initializes a new build stage and sets
the [base image](https://docs.docker.com/reference/glossary/#base-image) for subsequent instructions. As such, a valid
Dockerfile must start with a `FROM` instruction. The image can be any valid image.

`FROM` can appear multiple times within a single Dockerfile to create multiple images or use one build stage as a
dependency for another. Simply make a note of the last image ID output by the commit before each new `FROM` instruction.
Each `FROM` instruction clears any state created by previous instructions.

```dockerfile
# syntax=gpustack/gguf-packer:latest

FROM scratch 

# reference another image
FROM thxcode/qwen2:0.5b-instruct-q5-k-m
```

#### LABEL

The `LABEL` instruction adds metadata to an image. A `LABEL` is a key-value pair. To include spaces within a `LABEL`
value, use quotes and backslashes as you would in command-line parsing.

```dockerfile
# syntax=gpustack/gguf-packer:latest

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
# syntax=gpustack/gguf-packer:latest

# quantize a GGUF file from current stage
CONVERT  --type=F16 /app/Qwen2-0.5B-Instruct /app/Qwen2-0.5B-Instruct.F16.gguf
QUANTIZE --type=Q5_K_M /app/Qwen2-0.5B-Instruct.F16.gguf /app/Qwen2-0.5B-Instruct.Q5_K_M.gguf

# quantize from other stage
QUANTIZE --from=other-stage --type=Q5_K_M /app/Qwen2-0.5B-Instruct.F16.gguf /app/Qwen2-0.5B-Instruct.Q5_K_M.gguf

# quantize from build context
QUANTIZE --from=context --type=Q5_K_M /app/Qwen2-0.5B-Instruct.F16.gguf /app/Qwen2-0.5B-Instruct.Q5_K_M.gguf
```

##### Available Options

- `QUANTIZE [--from=<image|stage|context>] <src> <dest>`, by default, the `QUANTIZE` instruction quantizes file from the
  build context. The `QUANTIZE --from` flag lets you quantize file from an image, a build stage, or a named context
  instead.
- `QUANTIZE [--type=<type>] <src> <dest>`, specify the output type for `<dest>`,
  referring [llama.cpp/quantize](https://github.com/ggerganov/llama.cpp/blob/c887d8b01726b11ea03dbcaa9d44fa74422d0076/examples/quantize/quantize.cpp#L19-L51),
  upper case, default is `Q5_K_M`.
- `QUANTIZE [--pure] <src> <dest>`, indicate to disable k-quant mixtures and quantize all tensors to the same type.
- `QUANTIZE [--imatrix=<path>] <src> <dest>`, introduce a file as importance matrix for quant optimizations.
    + `QUANTIZE --imatrix=<path> [--include-weights=<tensor_name,...>] <src> <dest>`, specify to use the importance
      matrix for this/these tensors.
    + `QUANTIZE --imatrix=<path> [--exclude-weights=<tensor_name,...>] <src> <dest>`, specify to use the importance
      matrix, but exclude for this/these tensors.
- `QUANTIZE [--leave-output-tensor] <src> <dest>`, indicate to not quantize the `output.weight` tensor.
- `QUANTIZE [--output-tensor-type=<type>] <src> <dest>`, indicate the output tensor type,
  referring [llama.cpp/ggml](https://github.com/ggerganov/llama.cpp/blob/c887d8b01726b11ea03dbcaa9d44fa74422d0076/ggml/src/ggml.c#L579-L974),
  upper case.
- `QUANTIZE [--token-embedding-type=<type>] <src> <dest>`, indicate the token embedding type,
  referring [llama.cpp/ggml](https://github.com/ggerganov/llama.cpp/blob/c887d8b01726b11ea03dbcaa9d44fa74422d0076/ggml/src/ggml.c#L579-L974),
  upper case.

## Motivation

In the realm of Large Language Model (LLM) world, three projects stand
out: [GGML](https://github.com/ggerganov/ggml), [LLaMA.Cpp](https://github.com/ggerganov/llama.cpp),
and [Ollama](https://github.com/ollama/ollama). LLaMA.Cpp is built on GGML, and Ollama extends LLaMA.Cpp.

GGML presents an alternative for engineers who prefer avoiding Python due to common issues like environment
configuration, regional limitations, and installation complexities: a tensor computing library rooted in C/C++. With
GGML's quantized model file, the [GGUF](https://github.com/ggerganov/ggml/blob/master/docs/gguf.md) format, GGML
empowers edge devices to run LLMs efficiently.

LLaMa.cpp encapsulates various prominent LLM architectures and, with its
flagship [llama-server](https://github.com/ggerganov/llama.cpp/blob/master/examples/server/README.md), enables users to
set up
an OpenAI GPT-like service on their laptops with ease.

However, managing a Chat LLM service involves numerous parameters and model file management challenges. Ollama addresses
this by introducing a [Model File](https://github.com/ollama/ollama/blob/main/docs/modelfile.md) that facilitates the
distribution of the model file and its parameters, much like a
Dockerfile.

While the Ollama Model File is a Dockerfile-like tool for building and distributing Ollama models only, it does not
align well with the Cloud Native ecosystem. Let's explore the reasons why.

### Docker Image

Take, for example, the renowned DockerHub registry's [alpine](https://hub.docker.com/_/alpine/tags) image. We can
retrieve its manifest using [crane](https://github.com/google/go-containerregistry/blob/main/cmd/crane/README.md):

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

The `mediaType` of `alpine:latest` image manifest
is `application/vnd.docker.distribution.manifest.list.v2+json`, indicating a manifest list for multiple platforms.
For [OCI](https://opencontainers.org/) compatibility,
the corresponding `mediaType`
is [
`application/vnd.oci.image.index.v1+json`](https://github.com/opencontainers/image-spec/blob/main/media-types.md#applicationvndociimageindexv1json).

Delving deeper into the `linux/amd64` platform manifest for `alpine:latest`:

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

Here, the `mediaType` is `application/vnd.docker.distribution.manifest.v2+json`, which translates
to [
`application/vnd.oci.image.manifest.v1+json`](https://github.com/opencontainers/image-spec/blob/main/media-types.md#applicationvndociimagemanifestv1json)
for [OCI](https://opencontainers.org/) compatibility.

The manifest includes a special `config` field, referencing the image configuration as a JSON object detailing the
image's settings.

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

### OCI Distribution

The [OCI Distribution Specification](https://github.com/opencontainers/distribution-spec/blob/main/spec.md) defines a
standard for image registries that store and serve images. Examples of OCI Registries
include [Docker Registry](https://docs.docker.com/registry/), [GitHub Container Registry](https://docs.github.com/en/packages/guides/about-github-container-registry), [Harbor](https://goharbor.io/), [Quay](https://www.quay.io/), [Azure Container Registry](https://azure.microsoft.com/en-us/services/container-registry/), [Google Container Registry](https://cloud.google.com/container-registry).

Initially designed for storing container images, OCI Registries now also
support [OCI Artifacts]((https://github.com/opencontainers/artifacts)), with Helm charts being a prime example. Helm
charts, once managed through [Git repositories](https://github.com/rancher/charts)
or [released independently](https://github.com/prometheus-community/helm-charts/releases), can now
be [distributed as OCI Artifacts](https://helm.sh/docs/topics/registries/),
streamlining operations to a single OCI registry management task.

We can use `crane` to retrieve a Helm chart's manifest and download it as below:

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

Examining the Ollama model, specifically the [LLaMa3.1:8B](https://ollama.com/library/llama3.1) model, we initially
assumed it conformed to the standard OCI Registry.

However, attempts to retrieve its manifest with `crane` resulted in a 404 error, indicating non-compliance with OCI
standards.

```shell
$ crane manifest ollama.com/library/llama3.1:8b | jq .
Error: fetching manifest ollama.com/library/llama3.1:8b: GET https://ollama.com/v2/: unexpected status code 404 Not Found: 404 page not found

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

$ crane blob ollama.com/library/llama3.1@sha256:e711233e734332fe5f8a09b2407fb5a083e39ca7e0ba90788026414cd4c059af | jq .
Error: pulling layer ollama.com/library/llama3.1@sha256:e711233e734332fe5f8a09b2407fb5a083e39ca7e0ba90788026414cd4c059af: GET https://ollama.com/v2/: unexpected status code 404 Not Found: 404 page not found

$ curl https://ollama.com/v2/library/llama3.1/blobs/sha256:e711233e734332fe5f8a09b2407fb5a083e39ca7e0ba90788026414cd4c059af
<a href="https://dd20bb891979d25aebc8bec07b2b3bbc.r2.cloudflarestorage.com/ollama/docker/registry/v2/blobs/sha256/e7/e711233e734332fe5f8a09b2407fb5a083e39ca7e0ba90788026414cd4c059af/data?X-Amz-Algorithm=AWS4-HMAC-SHA256&amp;X-Amz-Credential=66040c77ac1b787c3af820529859349a%2F20240725%2Fauto%2Fs3%2Faws4_request&amp;X-Amz-Date=20240725T145758Z&amp;X-Amz-Expires=86400&amp;X-Amz-SignedHeaders=host&amp;X-Amz-Signature=262290faed14709a9c0faf9f8b8d567d3fec84731b2877852d275bc979f0530f">Temporary Redirect</a>.

$ curl -L https://ollama.com/v2/library/llama3.1/blobs/sha256:e711233e734332fe5f8a09b2407fb5a083e39ca7e0ba90788026414cd4c059af
{"model_format":"gguf","model_family":"llama","model_families":["llama"],"model_type":"8.0B","file_type":"Q4_0","architecture":"amd64","os":"linux","rootfs":{"type":"layers","diff_ids":["sha256:87048bcd55216712ef14c11c2c303728463207b165bf18440b9b84b07ec00f87","sha256:11ce4ee3e170f6adebac9a991c22e22ab3f8530e154ee669954c4bc73061c258","sha256:f1cd752815fcf68c3c2e73b2b00b5396c5dffb9eebe49567573f275f9ec85fcd","sha256:56bb8bd477a519ffa694fc449c2413c6f0e1d3b1c88fa7e3c9d88d3ae49d4dcb"]}}
```

**The Ollama model's distribution method is not a good practice.** Why?

Helm chart packages are usually very small, and the network cost of deploying OCI in a nearby network is very low, so
we don't need to disguise the Helm chart as a container image.

However, large model files, such as the 4.3GB LLaMa3.1:8B model, incur significant network costs when distributed
without compression.

Moreover, we can see many Ollama **pre-download** images in DockerHub, which is inefficient, wasting storage and network
resources.

[![](./docs/assets/dockerhub-ollama-model-cache.jpg)](https://hub.docker.com/search?q=ollama)

In conclusion, while Ollama has gained popularity in managing LLM distributions, its approach diverges from best
practices for OCI Artifacts. GGUF Packer, on the other hand, offers a contemporary solution that adheres to OCI
standards, reducing both network and storage overhead.

## License

MIT
