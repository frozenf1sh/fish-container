#!/usr/bin/env bash
set -euo pipefail

if [[ "$(uname -s)" != "Linux" ]]; then
  echo "e2e-linux: Linux is required" >&2
  exit 1
fi

if [[ "$(id -u)" -ne 0 ]]; then
  exec sudo -E "$0" "$@"
fi

runtime_bin="${1:-./bin/fish-container}"
runtime_bin="$(readlink -f "$runtime_bin")"
if [[ ! -x "$runtime_bin" ]]; then
  echo "e2e-linux: runtime binary not found: $runtime_bin" >&2
  exit 1
fi
if ! command -v docker >/dev/null 2>&1; then
  echo "e2e-linux: docker is required only to materialize the local test rootfs" >&2
  exit 1
fi

test_root="$(mktemp -d -t fish-container-e2e.XXXXXX)"
local_data="$test_root/local-data"
image_data="$test_root/image-data"
rootfs="$test_root/rootfs"
docker_id=""

cleanup() {
  set +e
  for data_and_id in "$local_data:local-smoke" "$image_data:image-smoke"; do
    data_root="${data_and_id%%:*}"
    container_id="${data_and_id##*:}"
    merged="$data_root/snapshots/overlay/$container_id/merged"
    if mountpoint -q "$merged" 2>/dev/null; then
      umount -l "$merged"
    fi
  done
  if [[ -n "$docker_id" ]]; then
    docker rm -f "$docker_id" >/dev/null 2>&1 || true
  fi
  rm -rf "$test_root"
}
trap cleanup EXIT

mkdir -p "$rootfs"
docker_id="$(docker create alpine:3.22 /bin/true)"
docker export "$docker_id" | tar -C "$rootfs" -xf -
docker rm "$docker_id" >/dev/null
docker_id=""

echo "[e2e] local rootfs lifecycle"
local_output="$($runtime_bin run \
  --data-root "$local_data" \
  --rootfs "$rootfs" \
  --container local-smoke \
  /bin/echo hello-local-rootfs 2>&1)"
echo "$local_output"
grep -q "hello-local-rootfs" <<<"$local_output"
$runtime_bin state --data-root "$local_data" local-smoke | grep -q '"status": "stopped"'
$runtime_bin delete --data-root "$local_data" local-smoke

echo "[e2e] OCI image lifecycle"
$runtime_bin pull --data-root "$image_data" alpine:3.22
image_output="$($runtime_bin run \
  --data-root "$image_data" \
  --image alpine:3.22 \
  --container image-smoke \
  /bin/echo hello-oci-image 2>&1)"
echo "$image_output"
grep -q "hello-oci-image" <<<"$image_output"
$runtime_bin state --data-root "$image_data" image-smoke | grep -q '"status": "stopped"'
$runtime_bin delete --data-root "$image_data" image-smoke

echo "[e2e] PASS: local rootfs and OCI image paths"
