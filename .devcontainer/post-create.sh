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

# Git identity for this repo. The private key is in 1Password on the host.
# VS Code forwards the host SSH agent, so only the public key is here.
GIT_NAME="Juho Päivärinta"
GIT_EMAIL="juho.paivarinta@outlook.com"
SSH_PUBLIC_KEY="ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAINoi0gz7OXOTjc04B4M63nTSRbywMO85L9bUQq7iq5up"

ENVTEST_BIN_DIR="${HOME}/.local/share/kubebuilder-envtest"
WORKSPACE="${PWD}"

log() { printf '\n==> %s\n' "$*"; }

log "Fixing ownership of cache volumes"
sudo mkdir -p /go/pkg/mod "${HOME}/.cache/go-build" "${HOME}/.kube"
sudo chown "$(id -u):$(id -g)" /go/pkg "${HOME}/.cache"
sudo chown -R "$(id -u):$(id -g)" /go/pkg/mod "${HOME}/.cache/go-build" "${HOME}/.kube"

log "Git safe.directory for the bind-mounted workspace"
git config --global --add safe.directory "${WORKSPACE}" || true

log "Git identity, SSH signing and GitHub SSH auth (1Password agent)"
mkdir -p "${HOME}/.ssh" "${HOME}/.config/git"
chmod 700 "${HOME}/.ssh"
printf '%s\n' "${SSH_PUBLIC_KEY}" > "${HOME}/.ssh/prokura.pub"
printf '%s %s\n' "${GIT_EMAIL}" "${SSH_PUBLIC_KEY}" > "${HOME}/.config/git/allowed_signers"
git config --global user.name "${GIT_NAME}"
git config --global user.email "${GIT_EMAIL}"
git config --global gpg.format ssh
# The host config can point at op-ssh-sign. That binary is not in the
# container. ssh-keygen finds the private key through the forwarded agent.
git config --global gpg.ssh.program ssh-keygen
git config --global user.signingkey "key::${SSH_PUBLIC_KEY}"
git config --global gpg.ssh.allowedSignersFile "${HOME}/.config/git/allowed_signers"
git config --global commit.gpgsign true
git config --global tag.gpgsign true
# Push and pull over SSH, not through the HTTPS credential helper.
git config --global url."git@github.com:JPaiv/".insteadOf "https://github.com/JPaiv/"
# Offer only this key. The agent holds other identities too.
touch "${HOME}/.ssh/config"
grep -q 'prokura.pub' "${HOME}/.ssh/config" || cat >> "${HOME}/.ssh/config" <<'EOF'

Host github.com
  IdentityFile ~/.ssh/prokura.pub
  IdentitiesOnly yes
EOF
# GitHub publishes its host keys over HTTPS. Pin them so the first push
# does not stop at a host key prompt.
ssh-keygen -F github.com -f "${HOME}/.ssh/known_hosts" >/dev/null 2>&1 \
  || curl -fsSL https://api.github.com/meta \
    | jq -r '.ssh_keys[] | "github.com " + .' >> "${HOME}/.ssh/known_hosts"

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
