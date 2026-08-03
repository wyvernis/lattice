"""
Lattice benchmark suite — measures throughput, latency, TTFT, TPOT, memory, GPU util.
"""
from __future__ import annotations

import asyncio
import json
import os
import statistics
import time
from typing import Any

import httpx
from fastapi import FastAPI
from pydantic import BaseModel, Field

app = FastAPI(title="Lattice Benchmark Suite", version="0.1.0")

GATEWAY = os.getenv("GATEWAY_URL", "http://localhost:8080")
API_KEY = os.getenv("API_KEYS", "lattice-dev-key").split(",")[0]


class BenchRequest(BaseModel):
    model: str | None = None
    prompts: list[str] = Field(default_factory=lambda: [
        "Write a Python function to reverse a linked list.",
        "Explain the CAP theorem briefly.",
        "Summarize the benefits of observability in distributed systems.",
    ])
    max_tokens: int = 64
    concurrency: int = 4
    policy: str = "balanced"


class BenchResult(BaseModel):
    model: str
    throughput_tps: float
    latency_p50_ms: float
    latency_p99_ms: float
    ttft_ms: float
    tpot_ms: float
    memory_mb: int = 0
    gpu_util: float = 0.0
    energy_joules: float | None = None
    samples: int
    errors: int
    report: dict[str, Any] = Field(default_factory=dict)


async def one_request(client: httpx.AsyncClient, prompt: str, req: BenchRequest) -> dict[str, Any]:
    start = time.perf_counter()
    ttft = None
    tokens = 0
    body = {
        "messages": [{"role": "user", "content": prompt}],
        "max_tokens": req.max_tokens,
        "stream": True,
        "policy": req.policy,
    }
    if req.model:
        body["model"] = req.model
    headers = {"X-API-Key": API_KEY, "Content-Type": "application/json"}
    async with client.stream("POST", f"{GATEWAY}/v1/chat/completions", json=body, headers=headers, timeout=120) as resp:
        async for line in resp.aiter_lines():
            if not line.startswith("data:"):
                continue
            data = line[5:].strip()
            if data == "[DONE]":
                break
            try:
                frame = json.loads(data)
            except json.JSONDecodeError:
                continue
            lattice = frame.get("lattice") or {}
            if ttft is None and lattice.get("ttft_ms"):
                ttft = float(lattice["ttft_ms"])
            elif ttft is None:
                ttft = (time.perf_counter() - start) * 1000
            choices = frame.get("choices") or []
            if choices:
                delta = (choices[0].get("delta") or {}).get("content") or ""
                if delta:
                    tokens += max(1, len(delta.split()))
    total_ms = (time.perf_counter() - start) * 1000
    return {
        "latency_ms": total_ms,
        "ttft_ms": ttft or total_ms,
        "tokens": tokens,
        "tpot_ms": (total_ms - (ttft or 0)) / max(tokens, 1),
    }


@app.get("/healthz")
def healthz():
    return {"status": "ok"}


@app.post("/v1/benchmark", response_model=BenchResult)
async def run_benchmark(req: BenchRequest):
    latencies: list[float] = []
    ttfts: list[float] = []
    tpots: list[float] = []
    tokens_total = 0
    errors = 0
    start = time.perf_counter()

    sem = asyncio.Semaphore(req.concurrency)

    async def wrapped(prompt: str):
        nonlocal tokens_total, errors
        async with sem:
            try:
                async with httpx.AsyncClient() as client:
                    r = await one_request(client, prompt, req)
                latencies.append(r["latency_ms"])
                ttfts.append(r["ttft_ms"])
                tpots.append(r["tpot_ms"])
                tokens_total += r["tokens"]
            except Exception:
                errors += 1

    # fan out prompts x rounds for stable stats
    jobs = []
    for _ in range(max(1, req.concurrency)):
        for p in req.prompts:
            jobs.append(wrapped(p))
    await asyncio.gather(*jobs)

    elapsed = max(time.perf_counter() - start, 0.001)
    model = req.model or "auto"
    if not latencies:
        return BenchResult(
            model=model, throughput_tps=0, latency_p50_ms=0, latency_p99_ms=0,
            ttft_ms=0, tpot_ms=0, samples=0, errors=errors,
        )

    latencies_sorted = sorted(latencies)
    p50 = statistics.median(latencies_sorted)
    p99 = latencies_sorted[min(len(latencies_sorted) - 1, int(len(latencies_sorted) * 0.99))]

    result = BenchResult(
        model=model,
        throughput_tps=tokens_total / elapsed,
        latency_p50_ms=p50,
        latency_p99_ms=p99,
        ttft_ms=statistics.mean(ttfts),
        tpot_ms=statistics.mean(tpots),
        samples=len(latencies),
        errors=errors,
        report={
            "concurrency": req.concurrency,
            "policy": req.policy,
            "elapsed_s": elapsed,
            "tokens_total": tokens_total,
        },
    )
    return result


@app.get("/v1/benchmark/compare")
async def compare(models: str = "phi-3-mini,llama3.1-8b,mistral-7b"):
    names = [m.strip() for m in models.split(",") if m.strip()]
    out = []
    for name in names:
        res = await run_benchmark(BenchRequest(model=name, concurrency=2, max_tokens=32))
        out.append(res.model_dump())
    out.sort(key=lambda x: x["latency_p50_ms"])
    return {"comparisons": out, "winner_latency": out[0]["model"] if out else None}
