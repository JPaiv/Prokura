#!/usr/bin/env bash
# Runs once after the devcontainer is created (see devcontainer.json).
# Idempotent: safe to re-run with "Dev Containers: Rebuild Container".
set -euo pipefail

# Pins for Go-based tools. Once the repo has a go.mod, move these to
# `tool` directives (Go 1.24+) and run them with `go tool <name>`;
# Dependabot can then bump them like any other module.
CONTROLLER_GEN_VERSION="v0.17.2"
ADDLICENSE_VERSION="v1.1.1"
ENVTEST_K8S_VERSION="1.32.x"

ENVTEST_BIN_DIR="${HOME}/.local/share/kubebuilder-envtest"
WORKSPACE="${PWD}"

log() { printf '\n==> %s\n' "$*"; }

log "Fixing ownership of cache volumes"
sudo mkdir -p /go/pkg/mod "${HOME}/.cache/go-build" "${HOME}/.kube"
sudo chown "$(id -u):$(id -g)" /go/pkg "${HOME}/.cache"
sudo chown -R "$(id -u):$(id -g)" /go/pkg/mod "${HOME}/.cache/go-build" "${HOME}/.kube"

log "Git safe.directory for the bind-mounted workspace"
git config --global --add safe.directory "${WORKSPACE}" || true

log "Installing Go tools"
go install "sigs.k8s.io/controller-tools/cmd/controller-gen@${CONTROLLER_GEN_VERSION}"
go install "sigs.k8s.io/controller-runtime/tools/setup-envtest@latest"
go install "github.com/google/addlicense@${ADDLICENSE_VERSION}"

log "Downloading envtest binaries (etcd + kube-apiserver ${ENVTEST_K8S_VERSION}) for unit tests"
mkdir -p "${ENVTEST_BIN_DIR}"
ASSETS="$(setup-envtest use "${ENVTEST_K8S_VERSION}" --bin-dir "${ENVTEST_BIN_DIR}" -p path)"

log "Shell environment"
for rc in "${HOME}/.zshrc" "${HOME}/.bashrc"; do
  [ -f "${rc}" ] || touch "${rc}"
  grep -q 'KUBEBUILDER_ASSETS' "${rc}" || cat >> "${rc}" <<EOF

# --- prokura devcontainer ---
export KUBEBUILDER_ASSETS="${ASSETS}"
export PATH="\${PATH}:/go/bin:\${HOME}/go/bin"
alias k=kubectl
alias kns='kubectl config set-context --current --namespace'
EOF
done

if [ -f "${WORKSPACE}/go.mod" ]; then
  log "Warming the module cache"
  (cd "${WORKSPACE}" && go mod download)
fi

log "Tool versions"
go version
kubectl version --client --output=yaml 2>/dev/null | head -4 || true
helm version --short
kind version
k3d version | head -1
kustomize version
kubebuilder version 2>/dev/null | head -1 || true
tilt version
controller-gen --version
docker version --format 'docker {{.Server.Version}}' 2>/dev/null || echo "docker: daemon not up yet (docker-in-docker starts with the container)"

cat <<'EOF'

Ready. Local cluster:
  .devcontainer/cluster.sh up          # k3d (default) with a local registry and API audit logging
  .devcontainer/cluster.sh up kind     # kind with API audit logging
  .devcontainer/cluster.sh audit       # tail the API server audit log
  .devcontainer/cluster.sh down
EOF
