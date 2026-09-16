#!/usr/bin/env bash
set -euo pipefail

repo_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
binary="codex-proxy"
out="${OUT:-${repo_dir}/${binary}}"

err() {
  printf 'codex-proxy build: %s\n' "$*" >&2
  exit 1
}

command -v go >/dev/null 2>&1 || err "required command not found: go"

cd "${repo_dir}"
printf 'Building %s\n' "${out}"
go build -trimpath -ldflags='-s -w' -o "${out}" .
printf 'Built %s\n' "${out}"
