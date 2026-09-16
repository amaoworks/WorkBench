#!/bin/sh
set -eu

repo_root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
scratch=${SCRATCH:-$(mktemp -d /tmp/workbench-external-rejected.XXXXXX)}
mkdir -p "$scratch"
host_bin=$scratch/workbench-a01
example_bin=$scratch/example-a01
log=$scratch/a01-a02-process.log

cd "$repo_root"
# URL registration must stay unavailable; existing-module compatibility is covered by Go tests.
go build -o "$host_bin" ./cmd/workbench
go build -C examples/external-module -o "$example_bin" .
host_hash=$(sha256sum "$host_bin" | awk '{print $1}')
ui_hash=$(find internal/webui/dist -type f | sort | xargs sha256sum | sha256sum | awk '{print $1}')
{
  echo "host_bin=$host_bin"
  echo "host_hash=$host_hash"
  echo "ui_hash=$ui_hash"
} | tee "$log"

wait_http() {
  url=$1
  i=0
  while [ "$i" -lt 80 ]; do
    if curl -sf "$url" >/dev/null 2>&1; then
      return 0
    fi
    i=$((i + 1))
    sleep 0.1
  done
  return 1
}

run_once() {
  run=$1
  data=$scratch/a01-run$run
  mkdir -p "$data"
  example_addr=127.0.0.1:$((18190 + run))
  host_addr=127.0.0.1:$((18090 + run))
  "$example_bin" -listen "$example_addr" -token process-token -data "$data/example.json" -title "run-$run" >"$data/example.out" 2>&1 &
  example_pid=$!
  "$host_bin" -listen "$host_addr" -data "$data/data.db" >"$data/host.out" 2>&1 &
  host_pid=$!
  if ! wait_http "http://$host_addr/health/live"; then
    echo "host failed to become live" | tee -a "$log"
    cat "$data/host.out" | tee -a "$log" || true
    kill "$host_pid" "$example_pid" 2>/dev/null || true
    wait "$host_pid" "$example_pid" 2>/dev/null || true
    return 1
  fi
  csrf=$(curl -sS -c "$data/cookies" "http://$host_addr/api/auth/csrf")
  csrf_token=$(printf '%s' "$csrf" | sed -n 's/.*"token":"\([^"]*\)".*/\1/p')
  attach=$(curl -sS -o "$data/attach.json" -w "%{http_code}" -b "$data/cookies" -c "$data/cookies" \
    -H "Origin: http://$host_addr" -H "X-CSRF-Token: $csrf_token" -H "Content-Type: application/json" \
    -d "{\"baseUrl\":\"http://$example_addr\",\"serviceToken\":\"process-token\"}" \
    "http://$host_addr/api/modules/external")
  list=$(curl -sS -b "$data/cookies" "http://$host_addr/api/modules")
  after_hash=$(sha256sum "$host_bin" | awk '{print $1}')
  after_ui=$(find internal/webui/dist -type f | sort | xargs sha256sum | sha256sum | awk '{print $1}')
  {
    echo "run=$run host_pid=$host_pid example_pid=$example_pid attach=$attach host_addr=$host_addr"
    echo "host_hash_after=$after_hash ui_hash_after=$after_ui"
    echo "list=$list"
  } | tee -a "$log"
  if [ "$attach" != "404" ]; then
    echo "URL registration was not rejected" | tee -a "$log"
    cat "$data/attach.json" 2>/dev/null | tee -a "$log" || true
    kill "$host_pid" "$example_pid" 2>/dev/null || true
    wait "$host_pid" "$example_pid" 2>/dev/null || true
    return 1
  fi
  if [ "$after_hash" != "$host_hash" ] || [ "$after_ui" != "$ui_hash" ]; then
    echo "host artifacts changed" | tee -a "$log"
    kill "$host_pid" "$example_pid" 2>/dev/null || true
    wait "$host_pid" "$example_pid" 2>/dev/null || true
    return 1
  fi
  if printf '%s' "$list" | grep -q '"kind":"external"'; then
    echo "unregistered module appeared in list" | tee -a "$log"
    kill "$host_pid" "$example_pid" 2>/dev/null || true
    wait "$host_pid" "$example_pid" 2>/dev/null || true
    return 1
  fi
  kill "$host_pid" "$example_pid" 2>/dev/null || true
  wait "$host_pid" "$example_pid" 2>/dev/null || true
}

run_once 1
run_once 2
echo "URL registration rejection check passed" | tee -a "$log"
