# syntax=docker/dockerfile:1.7-labs
FROM --platform=$TARGETPLATFORM ubuntu:22.04
SHELL ["/bin/bash", "-c"]

ARG TARGETPLATFORM
ARG TARGETOS
ARG TARGETARCH

ENV DEBIAN_FRONTEND=noninteractive \
    TZ=UTC \
    LC_ALL=C.UTF-8
RUN <<EOF
set -eux
apt-get update && apt-get install -y --no-install-recommends \
  ca-certificates  \
  git-lfs git  \
  python3 python3-pip \
  libcurl4-openssl-dev libgomp1
rm -rf /var/lib/apt/lists/*
EOF

# get llama-tools
COPY --from=ghcr.io/ggerganov/llama.cpp:full-50addec9a532a6518146ab837a85504850627316 --parents \
    /usr/local/lib/python3.10/dist-packages \
    /app/gguf-py \
    /app/convert_hf_to_gguf.py \
    /app/convert_lora_to_gguf.py \
    /app/llama-quantize \
    /

# get gguf-parser
COPY --from=docker.io/gpustack/gguf-parser:v0.7.2 --chmod=755 \
    /bin/gguf-parser \
    /bin/

COPY --chmod=755 .dist/gguf-packer-${TARGETOS}-${TARGETARCH} /bin/gguf-packer
ENTRYPOINT ["/bin/gguf-packer", "llb-frontend"]
