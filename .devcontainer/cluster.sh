#!/usr/bin/env bash
# Local cluster for developing Prokura inside the devcontainer.
#
#   .devcontainer/cluster.sh up [k3d|kind]   create (k3d is the default)
#   .devcontainer/cluster.sh down            delete
#   .devcontainer/cluster.sh audit           tail the API server audit log, one line per event
#   .devcontainer/cluster.sh audit raw       tail the raw JSON events
#   .devcontainer/cluster.sh status
#
# Both providers run kube-apiserver with the audit policy in this directory so
# you can watch impersonated calls (and their prokura.dev/run extra) land.
# k3d also creates a local image registry for Tilt; in your Tiltfile:
#   default_registry('localhost:5111', host_from_cluster='k3d-prokura-registry:5111')
set -euo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
NAME="${CLUSTER_NAME:-prokura}"
REGISTRY_PORT="${REGISTRY_PORT:-5111}"

K3D_AUDIT_LOG="/var/lib/rancher/k3s/server/logs/audit.log"
KIND_AUDIT_LOG="/var/log/kubernetes/kube-apiserver-audit.log"

usage() { sed -n '2,14p' "${BASH_SOURCE[0]}"; exit 1; }

detect_provider() {
  if k3d cluster list -o json 2>/dev/null | jq -e --arg n "${NAME}" 'any(.[]; .name == $n)' >/dev/null; then
    echo k3d
  elif kind get clusters 2>/dev/null | grep -qx "${NAME}"; then
    echo kind
  else
    echo ""
  fi
}

k3d_up() {
  k3d cluster create "${NAME}" \
    --agents 1 \
    --registry-create "${NAME}-registry:0.0.0.0:${REGISTRY_PORT}" \
    --volume "${HERE}/audit-policy.yaml:/etc/rancher/k3s/audit-policy.yaml@server:*" \
    --k3s-arg "--disable=traefik@server:*" \
    --k3s-arg "--kube-apiserver-arg=audit-policy-file=/etc/rancher/k3s/audit-policy.yaml@server:*" \
    --k3s-arg "--kube-apiserver-arg=audit-log-path=${K3D_AUDIT_LOG}@server:*" \
    --k3s-arg "--kube-apiserver-arg=audit-log-maxage=1@server:*" \
    --k3s-arg "--kube-apiserver-arg=audit-log-maxbackup=1@server:*" \
    --wait
}

kind_up() {
  # kubeadm v1beta4 (Kubernetes 1.31+): extraArgs is a list of name/value pairs.
  # For node images older than 1.31 switch to the map form.
  local cfg
  cfg="$(mktemp)"
  cat >"${cfg}" <<EOF
kind: Cluster
apiVersion: kind.x-k8s.io/v1alpha4
name: ${NAME}
nodes:
  - role: control-plane
    kubeadmConfigPatches:
      - |
        kind: ClusterConfiguration
        apiServer:
          extraArgs:
            - name: audit-log-path
              value: ${KIND_AUDIT_LOG}
            - name: audit-policy-file
              value: /etc/kubernetes/policies/audit-policy.yaml
            - name: audit-log-maxage
              value: "1"
            - name: audit-log-maxbackup
              value: "1"
          extraVolumes:
            - name: audit-policies
              hostPath: /etc/kubernetes/policies
              mountPath: /etc/kubernetes/policies
              readOnly: true
              pathType: DirectoryOrCreate
            - name: audit-logs
              hostPath: /var/log/kubernetes
              mountPath: /var/log/kubernetes
              readOnly: false
              pathType: DirectoryOrCreate
    extraMounts:
      - hostPath: ${HERE}/audit-policy.yaml
        containerPath: /etc/kubernetes/policies/audit-policy.yaml
        readOnly: true
  - role: worker
EOF
  kind create cluster --config "${cfg}" --wait 120s
  rm -f "${cfg}"
}

audit_tail() {
  local provider mode
  provider="$(detect_provider)"
  mode="${1:-pretty}"
  [ -n "${provider}" ] || { echo "no cluster named ${NAME}" >&2; exit 1; }

  local container path
  if [ "${provider}" = k3d ]; then
    container="k3d-${NAME}-server-0"; path="${K3D_AUDIT_LOG}"
  else
    container="${NAME}-control-plane"; path="${KIND_AUDIT_LOG}"
  fi

  if [ "${mode}" = raw ]; then
    exec docker exec "${container}" tail -n 50 -F "${path}"
  fi
  docker exec "${container}" tail -n 50 -F "${path}" | jq -c --unbuffered '{
    t: .stageTimestamp,
    verb,
    uri: .requestURI,
    user: .user.username,
    as: .impersonatedUser.username,
    run: ((.impersonatedUser.extra["prokura.dev/run"] // [])[0]),
    code: .responseStatus.code
  }'
}

cmd="${1:-}"
case "${cmd}" in
  up)
    provider="${2:-${CLUSTER_PROVIDER:-k3d}}"
    case "${provider}" in
      k3d)  k3d_up ;;
      kind) kind_up ;;
      *) echo "unknown provider: ${provider}" >&2; exit 1 ;;
    esac
    kubectl cluster-info
    echo
    echo "Audit log: .devcontainer/cluster.sh audit"
    ;;
  down)
    case "$(detect_provider)" in
      k3d)  k3d cluster delete "${NAME}" ;;
      kind) kind delete cluster --name "${NAME}" ;;
      *) echo "no cluster named ${NAME}" ;;
    esac
    ;;
  audit)  audit_tail "${2:-pretty}" ;;
  status)
    p="$(detect_provider)"
    echo "provider: ${p:-none}"
    [ -n "${p}" ] && kubectl get nodes -o wide
    ;;
  *) usage ;;
esac
