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
external_data="$test_root/external-data"
rootfs="$test_root/rootfs"
docker_id=""

cleanup() {
  set +e
  for data_and_id in "$local_data:local-smoke" "$image_data:created-smoke" "$image_data:bundle-source" "$image_data:image-smoke" "$image_data:kill-smoke" "$external_data:external-smoke"; do
    data_root="${data_and_id%%:*}"
    container_id="${data_and_id##*:}"
    "$runtime_bin" delete --force --data-root "$data_root" "$container_id" >/dev/null 2>&1 || true
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

echo "[e2e] OCI create/start barrier"
$runtime_bin create \
  --data-root "$image_data" \
  --image alpine:3.22 \
  --container created-smoke \
  /bin/sh -c 'echo started > /started-marker'
$runtime_bin state --data-root "$image_data" created-smoke | grep -q '"status": "created"'
test ! -e "$image_data/snapshots/overlay/created-smoke/merged/started-marker"
$runtime_bin start --data-root "$image_data" created-smoke
$runtime_bin state --data-root "$image_data" created-smoke | grep -q '"status": "stopped"'
test -e "$image_data/snapshots/overlay/created-smoke/merged/started-marker"
$runtime_bin delete --data-root "$image_data" created-smoke

echo "[e2e] external OCI bundle and pid file"
$runtime_bin create \
  --data-root "$image_data" \
  --image alpine:3.22 \
  --container bundle-source \
  /bin/echo external-bundle-ok
$runtime_bin create \
  --data-root "$external_data" \
  --bundle "$image_data/containers/bundle-source/bundle" \
  --pid-file "$test_root/external.pid" \
  external-smoke
test "$(cat "$test_root/external.pid")" -gt 0
$runtime_bin state --data-root "$external_data" external-smoke | grep -q '"status": "created"'
$runtime_bin start --data-root "$external_data" external-smoke
$runtime_bin state --data-root "$external_data" external-smoke | grep -q '"fish-container.io/exit-status": "0"'
$runtime_bin delete --data-root "$external_data" external-smoke
$runtime_bin kill --data-root "$image_data" bundle-source
$runtime_bin delete --data-root "$image_data" bundle-source

image_output="$($runtime_bin run \
  --data-root "$image_data" \
  --image alpine:3.22 \
  --container image-smoke \
  /bin/echo hello-oci-image 2>&1)"
echo "$image_output"
grep -q "hello-oci-image" <<<"$image_output"
$runtime_bin state --data-root "$image_data" image-smoke | grep -q '"status": "stopped"'
$runtime_bin delete --data-root "$image_data" image-smoke

echo "[e2e] PID namespace init kill and delete"
$runtime_bin run \
  --data-root "$image_data" \
  --image alpine:3.22 \
  --container kill-smoke \
  -d \
  /bin/sh -c 'while true; do sleep 1; done'
for _ in $(seq 1 50); do
  if $runtime_bin state --data-root "$image_data" kill-smoke 2>/dev/null | grep -q '"status": "running"'; then
    break
  fi
  sleep 0.1
done
$runtime_bin kill --data-root "$image_data" kill-smoke
$runtime_bin state --data-root "$image_data" kill-smoke | tee "$test_root/kill-state.json" | grep -q '"status": "stopped"'
grep -q '"fish-container.io/exit-status": "137"' "$test_root/kill-state.json"
$runtime_bin delete --data-root "$image_data" kill-smoke
sleep 0.1
test ! -e "$image_data/runtime/kill-smoke/state.json"

echo "[e2e] PASS: M1 lifecycle, OCI bundle, local rootfs, and OCI image paths"
