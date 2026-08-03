#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"
mkdir -p bin logs

export API_KEYS=lattice-dev-key
export JWT_SECRET=lattice-dev-secret-change-me
export NATS_URL="${NATS_URL:-nats://127.0.0.1:4222}"
export ROUTER_URL=http://127.0.0.1:8081
export SCHEDULER_URL=http://127.0.0.1:8082
export REGISTRY_URL=http://127.0.0.1:8083
export WORKER_URL=http://127.0.0.1:8084
export GATEWAY_URL=http://127.0.0.1:8080

PIDS=()
cleanup() {
  echo "shutting down..."
  for pid in "${PIDS[@]:-}"; do kill "$pid" 2>/dev/null || true; done
}
trap cleanup EXIT INT TERM

start() {
  local name=$1 addr=$2
  shift 2
  echo "→ $name on $addr"
  HTTP_ADDR="$addr" "$@" >"logs/$name.log" 2>&1 &
  PIDS+=($!)
}

start registry :8083 ./bin/registry
start router   :8081 ./bin/router
start scheduler :8082 ./bin/scheduler
sleep 0.5
NODE_ID=node-local-1 ADVERTISE_URL=http://127.0.0.1:8084 HTTP_ADDR=:8084 \
  ./bin/worker >logs/worker.log 2>&1 &
PIDS+=($!)
start chaos :8085 ./bin/chaos
start gateway :8080 ./bin/gateway

echo ""
echo "lattice control plane is up"
echo "  gateway    http://127.0.0.1:8080"
echo "  router     http://127.0.0.1:8081"
echo "  scheduler  http://127.0.0.1:8082"
echo "  registry   http://127.0.0.1:8083"
echo "  worker     http://127.0.0.1:8084"
echo "  chaos      http://127.0.0.1:8085"
echo ""
echo "try: make infer"
echo "dashboard: cd dashboard && npm run dev"
echo ""
wait
