#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
image="${1:-cba-helm-checks}"
container_runtime="${CONTAINER_RUNTIME:-}"

if [ -n "$container_runtime" ]; then
  if ! command -v "$container_runtime" >/dev/null 2>&1; then
    echo "container runtime '$container_runtime' is not installed" >&2
    exit 1
  fi
else
  if command -v podman >/dev/null 2>&1; then
    container_runtime="podman"
  elif command -v docker >/dev/null 2>&1; then
    container_runtime="docker"
  else
    echo "no container runtime found; install podman or docker, or set CONTAINER_RUNTIME" >&2
    exit 1
  fi
fi

ctr_flags=(--rm)

if [ -t 1 ]; then
  ctr_flags+=(-it)
fi

"$container_runtime" build -t "$image" -f "$repo_root/tools/helm-checks/Dockerfile" "$repo_root"

"$container_runtime" run "${ctr_flags[@]}" \
  -v "$repo_root":/workspace -w /workspace \
  "$image" \
  /bin/bash -lc '
    set -euo pipefail
    helm template ./helm | yamllint -d "{extends: default, rules: {key-duplicates: enable, indentation: disable}}" -
    helm lint --strict ./helm
    ct lint --config ct.yaml --all
  '
