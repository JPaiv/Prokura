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

## Git signing and SSH

The SSH authentication and signing key is in 1Password. The private key never
enters the container:

1. On the host, turn on the 1Password SSH agent and point `SSH_AUTH_SOCK` at
   it (`~/.1password/agent.sock` on Linux). VS Code forwards that agent into
   the container.
2. Add the public key to GitHub twice: once as an **Authentication key** and
   once as a **Signing key**.

[post-create.sh](post-create.sh) pins the identity and the public key. It
configures git to sign commits and tags with SSH (`ssh-keygen` through the
agent), and to push to `github.com/JPaiv` over SSH with only that key.
1Password asks you to approve each use.

## Terminal

The integrated terminal uses a Matrix palette: green on black. Red and yellow
stay distinct so test failures and warnings still stand out.

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
