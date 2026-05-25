#!/usr/bin/env bash
set -euo pipefail

kind_version="${1:-v0.31.0}"
kubectl_version="${2:-v1.35.0}"

: "${RUNNER_TOOL_CACHE:?RUNNER_TOOL_CACHE is required}"

case "$(uname -m)" in
  i386|i686)
    arch="386"
    ;;
  x86_64)
    arch="amd64"
    ;;
  arm|aarch64|arm64)
    arch="arm64"
    ;;
  *)
    echo "unsupported architecture: $(uname -m)" >&2
    exit 1
    ;;
esac

cache_dir="${RUNNER_TOOL_CACHE}/kind/${kind_version}/${arch}"
kind_dir="${cache_dir}/kind/bin"
kubectl_dir="${cache_dir}/kubectl/bin"

check_sha256() {
  shasum -a 256 -c
}

install_kind() {
  mkdir -p "${kind_dir}"
  if [[ -x "${kind_dir}/kind" ]]; then
    return
  fi

  tmp="$(mktemp -d)"
  trap 'rm -rf "${tmp}"' RETURN

  curl -fsSL -o "${tmp}/kind-linux-${arch}" "https://github.com/kubernetes-sigs/kind/releases/download/${kind_version}/kind-linux-${arch}"
  curl -fsSL -o "${tmp}/kind-linux-${arch}.sha256sum" "https://github.com/kubernetes-sigs/kind/releases/download/${kind_version}/kind-linux-${arch}.sha256sum"
  (
    cd "${tmp}"
    grep "kind-linux-${arch}" "kind-linux-${arch}.sha256sum" | check_sha256
  )
  mv "${tmp}/kind-linux-${arch}" "${kind_dir}/kind"
  chmod +x "${kind_dir}/kind"
}

install_kubectl() {
  mkdir -p "${kubectl_dir}"
  if [[ -x "${kubectl_dir}/kubectl" ]]; then
    return
  fi

  tmp="$(mktemp -d)"
  trap 'rm -rf "${tmp}"' RETURN

  curl -fsSL -o "${tmp}/kubectl" "https://dl.k8s.io/release/${kubectl_version}/bin/linux/${arch}/kubectl"
  curl -fsSL -o "${tmp}/kubectl.sha256" "https://dl.k8s.io/release/${kubectl_version}/bin/linux/${arch}/kubectl.sha256"
  printf '%s  %s\n' "$(cat "${tmp}/kubectl.sha256")" "${tmp}/kubectl" | check_sha256
  mv "${tmp}/kubectl" "${kubectl_dir}/kubectl"
  chmod +x "${kubectl_dir}/kubectl"
}

install_kind
install_kubectl

if [[ "$(uname -s)" != "Linux" ]]; then
  echo "installed Linux kind/kubectl into ${cache_dir}; skipping execution on $(uname -s)"
  exit 0
fi

"${kind_dir}/kind" version
"${kubectl_dir}/kubectl" version --client=true
