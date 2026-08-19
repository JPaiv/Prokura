# syntax=docker/dockerfile:1

# Stage 1: tools.
# This stage downloads the CLI tools. Each tool has its own RUN step.
# Only the /out directory is copied to the dev image. The other files in
# this stage are not used.
# To upgrade a tool, change its version ARG.
FROM debian:bookworm-slim AS tools

ARG TARGETARCH
ARG KIND_VERSION=0.27.0
ARG K3D_VERSION=5.8.3
ARG KUSTOMIZE_VERSION=5.6.0
ARG KUBEBUILDER_VERSION=4.3.1
ARG KUBECONFORM_VERSION=0.6.7
ARG YQ_VERSION=4.45.1
ARG TASK_VERSION=3.53.1
# TILT_VERSION is empty by default. Then the Tilt installer downloads the
# latest release. Set a version (for example 0.34.2) to pin Tilt.
ARG TILT_VERSION=

RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates curl \
    && rm -rf /var/lib/apt/lists/*

RUN mkdir -p /out/bin /out/zsh /out/bash

# kind
RUN curl -fsSLo /out/bin/kind \
    "https://github.com/kubernetes-sigs/kind/releases/download/v${KIND_VERSION}/kind-linux-${TARGETARCH}"

# k3d
RUN curl -fsSLo /out/bin/k3d \
    "https://github.com/k3d-io/k3d/releases/download/v${K3D_VERSION}/k3d-linux-${TARGETARCH}"

# kustomize. The release tag is "kustomize/vX.Y.Z". "%2F" is the URL code for "/".
RUN curl -fsSL \
    "https://github.com/kubernetes-sigs/kustomize/releases/download/kustomize%2Fv${KUSTOMIZE_VERSION}/kustomize_v${KUSTOMIZE_VERSION}_linux_${TARGETARCH}.tar.gz" \
    | tar -xz -C /out/bin kustomize

# kubebuilder
RUN curl -fsSLo /out/bin/kubebuilder \
    "https://github.com/kubernetes-sigs/kubebuilder/releases/download/v${KUBEBUILDER_VERSION}/kubebuilder_linux_${TARGETARCH}"

# kubeconform
RUN curl -fsSL \
    "https://github.com/yannh/kubeconform/releases/download/v${KUBECONFORM_VERSION}/kubeconform-linux-${TARGETARCH}.tar.gz" \
    | tar -xz -C /out/bin kubeconform

# yq
RUN curl -fsSLo /out/bin/yq \
    "https://github.com/mikefarah/yq/releases/download/v${YQ_VERSION}/yq_linux_${TARGETARCH}"

# task (https://taskfile.dev). The project uses a Taskfile, not a Makefile.
RUN curl -fsSL \
    "https://github.com/go-task/task/releases/download/v${TASK_VERSION}/task_linux_${TARGETARCH}.tar.gz" \
    | tar -xz -C /out/bin task

# Tilt.
# If TILT_VERSION is set: download the release archive.
# If TILT_VERSION is empty: run the official installer. The installer uses
# sudo unless $HOME/.local/bin is in PATH. This stage has no sudo. So the
# script adds $HOME/.local/bin to PATH and moves the binary to /out/bin.
RUN <<'EOF'
set -eu
if [ -n "${TILT_VERSION}" ]; then
  case "${TARGETARCH}" in
    arm64) tarch=arm64 ;;
    *)     tarch=x86_64 ;;
  esac
  curl -fsSL "https://github.com/tilt-dev/tilt/releases/download/v${TILT_VERSION}/tilt.${TILT_VERSION}.linux.${tarch}.tar.gz" \
    | tar -xz -C /out/bin tilt
else
  mkdir -p /root/.local/bin
  curl -fsSL https://raw.githubusercontent.com/tilt-dev/tilt/master/scripts/install.sh \
    | PATH="/root/.local/bin:${PATH}" bash
  mv /root/.local/bin/tilt /out/bin/tilt
fi
EOF

# Set file permissions. Generate shell completions. Print the version of
# each tool. If a version ARG is wrong, the build fails in this step.
RUN <<'EOF'
set -eu
chmod 0755 /out/bin/*
for t in kind k3d kustomize kubebuilder tilt task; do
  "/out/bin/$t" completion zsh  > "/out/zsh/_$t"  || rm -f "/out/zsh/_$t"
  "/out/bin/$t" completion bash > "/out/bash/$t" || rm -f "/out/bash/$t"
done
/out/bin/kind version
/out/bin/k3d version
/out/bin/kustomize version
/out/bin/kubebuilder version
/out/bin/kubeconform -v
/out/bin/yq --version
/out/bin/task --version
/out/bin/tilt version
EOF

# Stage 2: dev image.
# Base: the official Go devcontainer image. It contains Go, gopls, delve,
# golangci-lint, zsh with oh-my-zsh, and the non-root user "vscode" with sudo.
# devcontainer features add kubectl, helm, and docker-in-docker.
FROM mcr.microsoft.com/devcontainers/go:1-1.24-bookworm AS dev

USER root

# The base image has an apt source for yarn. The signing key of that source
# is not valid. This makes "apt-get update" fail in this step and in the
# feature install steps. This image does not need yarn. Remove the source.
# The apt cache is in BuildKit cache mounts. It is not stored in the image.
RUN --mount=type=cache,target=/var/cache/apt,sharing=locked \
    --mount=type=cache,target=/var/lib/apt,sharing=locked \
    grep -rl 'dl.yarnpkg.com' /etc/apt/sources.list.d | xargs -r rm -f \
    && export DEBIAN_FRONTEND=noninteractive \
    && apt-get update \
    && apt-get install -y --no-install-recommends bash-completion jq make

# zsh reads completions from /usr/local/share/zsh/site-functions by default.
# No change to ~/.zshrc is needed.
COPY --from=tools /out/bin/  /usr/local/bin/
COPY --from=tools /out/zsh/  /usr/local/share/zsh/site-functions/
COPY --from=tools /out/bash/ /etc/bash_completion.d/

USER vscode
