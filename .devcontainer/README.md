# Devcontainer

This directory defines the development environment for Prokura.

## What you get

- Go 1.24 with module and build caches on named volumes.
- Kubernetes tools: `kubectl`, `helm`, `kind`, `k3d`, `kustomize`, `kubebuilder`, `kubeconform`, `yq`.
- `task` (<https://taskfile.dev>). The project uses a Taskfile, not a Makefile.
- `tilt` for a local development loop.
- Docker-in-docker for building images and running local clusters.
- envtest binaries for the controller unit tests.

Tool versions are pinned with `ARG` values in the [Dockerfile](Dockerfile).

## First start

VS Code runs [post-create.sh](post-create.sh) once after it creates the
container. The script installs the Go tools, downloads the envtest binaries,
and configures the shell. It is safe to re-run with
**Dev Containers: Rebuild Container**.

## Local cluster

Use [cluster.sh](cluster.sh) to manage a local cluster:

```bash
.devcontainer/cluster.sh up          # create a k3d cluster (default)
.devcontainer/cluster.sh up kind     # create a kind cluster instead
.devcontainer/cluster.sh audit       # tail the API server audit log
.devcontainer/cluster.sh status
.devcontainer/cluster.sh down
```

Both providers start `kube-apiserver` with [audit-policy.yaml](audit-policy.yaml).
The audit log shows each impersonated call with the envelope identity and the
`prokura.dev/run` extra. This lets you watch what a mandate does.

The k3d provider also creates a local image registry for Tilt on port 5111.
