# lattice

Distributed LLM inference control plane. Clients hit one OpenAI-compatible endpoint. Lattice classifies the request, picks a model, chooses a worker, and serves the result — optimizing for latency, cost, quality, or throughput.

This is infrastructure, not a chatbot. It is meant to look like the internal serving layer a lab or cloud provider would run: routing, scheduling, model lifecycle, observability, and failover.

## What it does

1. Accepts chat, completion, embedding, batch, and streaming requests.
2. Classifies intent (coding, reasoning, summarization, translation, vision, chat, embedding).
3. Selects a model from a configurable policy, optionally downshifting to a cheaper one if quality still clears a threshold.
4. Schedules the job onto a healthy worker using cluster telemetry (queue depth, GPU load, estimated latency, cost).
5. Loads / warms / evicts models on workers (LRU + hour-of-day demand prediction).
6. Streams tokens back over SSE or WebSocket, with TTFT and tokens/sec attached.
7. If a worker dies, the gateway retries once on another node.

Without GPUs the stack still runs end-to-end on a **mock backend**, so you can demo routing, scheduling, and the dashboard on a laptop.

## Architecture

```
client
  │  REST / SSE / WebSocket
  ▼
┌──────────┐     ┌────────┐     ┌────────────┐
│ gateway  │────▶│ router │────▶│ scheduler  │
│ auth, API│     │ intent │     │ placement  │
└────┬─────┘     └────────┘     └─────┬──────┘
     │                                 │
     │           ┌──────────┐         │
     │           │ registry  │◀────────┤
     │           │ catalog  │         │
     │           └──────────┘         │
     ▼                                 ▼
┌─────────────────────────────────────────────┐
│ workers  (mock / vLLM / Ollama / SGLang /     │
│ llama.cpp) + batching + lifecycle            │
└─────────────────────────────────────────────┘
     │
     ▼
Prometheus · Grafana · OpenTelemetry · NATS · dashboard
```

| service     | port | role |
|-------------|------|------|
| gateway     | 8080 | OpenAI-compatible API, auth, streaming, retry |
| router      | 8081 | intent classification and model candidates |
| scheduler   | 8082 | cluster state, placement, live topology WS |
| registry    | 8083 | model catalog, versions, quantizations |
| worker      | 8084 | inference agent, backends, batching, load/unload |
| chaos       | 8085 | crash / delay / OOM / traffic spike injection |
| benchmark   | 8090 | TTFT / throughput / latency suite (Python) |
| dashboard    | 3000 | live cluster UI |
| grafana     | 3001 | metrics dashboards (compose only) |
| prometheus  | 9090 | scrape (compose only) |

Request path: **gateway → router → scheduler → worker → stream back to client**.

## Tech stack

**Control plane (Go 1.22)**
- HTTP services (`net/http`)
- JWT + API keys (`github.com/golang-jwt/jwt`)
- NATS for cluster events (in-process bus if NATS is down)
- Prometheus client + OpenTelemetry traces
- WebSockets (`gorilla/websocket`)

**Dashboard**
- Next.js 14, React 18, TypeScript
- Tailwind CSS
- React Flow (cluster topology)
- ECharts (GPU / throughput charts)

**Inference backends (pluggable)**
- mock (default, CPU-only demos)
- OpenAI-compatible HTTP: vLLM, TensorRT-LLM, SGLang, Ollama, llama.cpp

**Data / messaging**
- NATS JetStream, Redis, PostgreSQL (compose brings these up; services degrade without them)

**Benchmark**
- Python 3.11, FastAPI, httpx

**Deploy**
- Docker + Compose
- Kubernetes manifests + Helm chart stub
- GitHub Actions CI

## Requirements

Local (no Docker):
- Go 1.22+
- Node 20+
- Make, bash (Git Bash on Windows)

Compose:
- Docker with Compose v2

Optional for real inference:
- a running OpenAI-compatible server (`VLLM_URL`, `OLLAMA_URL`, `SGLANG_URL`, or `LLAMACPP_URL` on the worker)

## Quick start (local)

```bash
git clone https://github.com/wyvernis/lattice.git
cd lattice

make build
make run-local
```

In another terminal:

```bash
cd dashboard
npm install
npm run dev
```

| surface    | url |
|------------|-----|
| api        | http://127.0.0.1:8080 |
| dashboard  | http://127.0.0.1:3000 |
| api key    | `lattice-dev-key` |

Smoke test:

```bash
make infer
```

or:

```bash
curl http://127.0.0.1:8080/v1/chat/completions \
  -H 'Content-Type: application/json' \
  -H 'X-API-Key: lattice-dev-key' \
  -d '{
    "messages": [{"role": "user", "content": "Write a Go function to reverse a linked list."}],
    "max_tokens": 64,
    "policy": "balanced"
  }'
```

A coding prompt should classify as `coding` and land on a coder model (for example `qwen2.5-coder-7b` / `deepseek-coder-6.7b`). The JSON includes a `lattice` object: node, backend, policy, category, latency, TTFT.

NATS is optional locally. If nothing is listening on `:4222`, services log a warning and use an in-process event bus.

## Run with Docker

```bash
docker compose up --build
```

| surface     | url |
|-------------|-----|
| api         | http://localhost:8080 |
| dashboard    | http://localhost:3000 |
| grafana     | http://localhost:3001 (admin password `lattice`) |
| prometheus  | http://localhost:9090 |
| benchmark   | http://localhost:8090 |

Compose starts two workers, NATS, Redis, Postgres, OTel collector, Prometheus, and Grafana.

Stop:

```bash
docker compose down -v
```

## How to use the API

All `/v1/*` routes except health checks require auth:

```
X-API-Key: lattice-dev-key
```

or `Authorization: Bearer <jwt>` from `POST /v1/auth/token`.

### Chat

```bash
curl http://127.0.0.1:8080/v1/chat/completions \
  -H 'Content-Type: application/json' \
  -H 'X-API-Key: lattice-dev-key' \
  -d '{
    "messages": [{"role": "user", "content": "Summarize CAP theorem in two sentences."}],
    "max_tokens": 128,
    "stream": false,
    "policy": "cost_first"
  }'
```

Omit `model` to let the router choose. Set `model` to pin a specific one.

### Streaming (SSE)

Set `"stream": true`. Frames are OpenAI-style `data: {...}` with extra `lattice` fields (`ttft_ms`, `tokens_per_sec`, `node_id`). Ends with `data: [DONE]`.

WebSocket: `GET /v1/ws/stream` (send the same JSON body as the first message).

### Completions and embeddings

- `POST /v1/completions` — `{ "prompt": "...", "max_tokens": 64 }`
- `POST /v1/embeddings` — `{ "input": "..." }`
- `POST /v1/batch` — `{ "requests": [ { "messages": [...] }, ... ] }`

### Routing policies

Pass `"policy"` on the request:

| policy             | objective |
|------------------|------------|
| `balanced`         | mix of latency, cost, load, quality (default) |
| `latency_first`    | lowest estimated latency / TTFT |
| `cost_first`       | cheaper models if quality ≥ threshold |
| `quality_first`    | larger / higher-scored models |
| `throughput`       | tokens/sec and queue pressure |
| `energy_efficient` | estimated watts |
| `least_loaded`     | fewest in-flight requests |

Intent → default models (see `configs/routing.yaml`):

| category       | models |
|----------------|--------|
| coding         | qwen2.5-coder-7b, deepseek-coder-6.7b, codellama-7b |
| reasoning      | deepseek-r1-7b, qwen2.5-32b, llama3.1-8b |
| summarization  | mistral-7b, llama3.1-8b, phi-3-mini |
| translation    | aya-23-8b, mistral-7b |
| vision         | qwen2-vl-7b, llava-1.6-7b |
| chat           | llama3.1-8b, mistral-7b, phi-3-mini |
| embedding      | nomic-embed-text, bge-small-en |

Hot-reload routes:

```bash
curl http://127.0.0.1:8081/v1/policy          # GET current
curl -X PUT http://127.0.0.1:8081/v1/policy \
  -H 'Content-Type: application/json' \
  -d @configs/routing.yaml
```

### Control-plane endpoints

| method | path | service |
|--------|------|---------|
| POST   | `/v1/classify` | router |
| GET/PUT | `/v1/policy` | router |
| POST   | `/v1/schedule` | scheduler |
| GET    | `/v1/cluster` · `/v1/nodes` | scheduler |
| WS     | `/v1/ws/cluster` | scheduler |
| GET/POST | `/v1/models` | registry |
| POST   | `/v1/infer` · `/v1/infer/stream` | worker |
| POST   | `/v1/chaos/experiments` | chaos |
| POST   | `/v1/benchmark` | benchmark |
| GET    | `/healthz` · `/metrics` | every service |

## Dashboard

The dashboard polls `/api/cluster` (proxied to the scheduler) and can open `ws://localhost:8082/v1/ws/cluster`. It shows:

- node health, GPU util, queue depth, tokens/sec
- loaded models and registry catalog
- live topology (gateway → router → scheduler → workers)
- inference playground (policy + stream)
- recent failures

## Point workers at real GPUs

Default backend is `mock`. On a worker (compose env or process env):

```bash
VLLM_URL=http://127.0.0.1:8000
# or
OLLAMA_URL=http://127.0.0.1:11434
SGLANG_URL=http://127.0.0.1:30000
LLAMACPP_URL=http://127.0.0.1:8080
```

The worker speaks OpenAI-compatible `/v1/chat/completions` to those servers.

## Chaos and benchmarks

Crash a worker, then recover:

```bash
curl -X POST http://127.0.0.1:8085/v1/chaos/experiments \
  -H 'Content-Type: application/json' \
  -d '{"action":"crash","target":"http://127.0.0.1:8084"}'

curl -X POST http://127.0.0.1:8085/v1/chaos/experiments \
  -H 'Content-Type: application/json' \
  -d '{"action":"recover","target":"http://127.0.0.1:8084"}'
```

Traffic spike: `{"action":"spike","rps":50,"duration":"10s"}`.

Benchmark (compose service on 8090):

```bash
curl -X POST http://127.0.0.1:8090/v1/benchmark \
  -H 'Content-Type: application/json' \
  -d '{"concurrency":4,"max_tokens":64,"policy":"balanced"}'
```

## Kubernetes

Build and push images (`lattice/gateway:latest`, `lattice/router:latest`, …), then:

```bash
kubectl apply -f deploy/kubernetes/lattice.yaml
```

Gateway is a `LoadBalancer` on port 80. Helm values live in `deploy/helm/lattice`. Change `API_KEYS` and `JWT_SECRET` in the ConfigMap before any shared cluster.

Worker pods advertise via `POD_IP`. HPA and a KEDA ScaledObject (queue depth) are in the same manifest.

## Configuration

Common env vars (`pkg/config`):

| variable | default | purpose |
|----------|---------|---------|
| `HTTP_ADDR` | `:8080` | listen address |
| `API_KEYS` | `lattice-dev-key` | comma-separated keys (`key:roles:tenant:rpm`) |
| `JWT_SECRET` | `lattice-dev-secret-change-me` | JWT signing |
| `NATS_URL` | `nats://127.0.0.1:4222` | event bus |
| `ROUTER_URL` / `SCHEDULER_URL` / `REGISTRY_URL` | localhost ports | gateway wiring |
| `CLUSTER_NAME` | `local` | node grouping |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | `127.0.0.1:4318` | traces |
| `NODE_ID` / `ADVERTISE_URL` | generated | worker identity |

Change `API_KEYS` and `JWT_SECRET` before exposing the API.

## Repository layout

```
pkg/            shared types, auth, metrics, tracing, events, batching, lifecycle, cost
services/       gateway, router, scheduler, worker, registry, chaos, benchmark
dashboard/      Next.js live UI
configs/        routing policy YAML
deploy/         docker, k8s, helm, prometheus, grafana, otel
scripts/        local process supervisor
```

Plugins (`pkg/plugins`): router, scheduler, backend, auth, metrics, storage, benchmark. Add a type that implements the interface and register it.

## Tests and CI

```bash
make test          # go test ./...
make build         # binaries in bin/
```

GitHub Actions (`.github/workflows/ci.yml`) runs Go tests + service builds, dashboard `npm run build`, and a Python import check for the benchmark service.

## License

Apache-2.0. See [LICENSE](LICENSE).
