"use client";

import { useState } from "react";

const API_KEY = process.env.NEXT_PUBLIC_API_KEY || "lattice-dev-key";

export function Playground() {
  const [prompt, setPrompt] = useState("Refactor this Go scheduler for lower latency.");
  const [policy, setPolicy] = useState("balanced");
  const [stream, setStream] = useState(true);
  const [output, setOutput] = useState("");
  const [meta, setMeta] = useState<Record<string, unknown> | null>(null);
  const [busy, setBusy] = useState(false);

  async function run() {
    setBusy(true);
    setOutput("");
    setMeta(null);
    try {
      const res = await fetch("/api/infer", {
        method: "POST",
        headers: {
          "Content-Type": "application/json",
          "X-API-Key": API_KEY,
        },
        body: JSON.stringify({
          messages: [{ role: "user", content: prompt }],
          max_tokens: 128,
          stream,
          policy,
        }),
      });
      if (stream) {
        const reader = res.body?.getReader();
        const dec = new TextDecoder();
        let buf = "";
        let text = "";
        while (reader) {
          const { done, value } = await reader.read();
          if (done) break;
          buf += dec.decode(value, { stream: true });
          const lines = buf.split("\n");
          buf = lines.pop() || "";
          for (const line of lines) {
            if (!line.startsWith("data:")) continue;
            const raw = line.slice(5).trim();
            if (raw === "[DONE]") continue;
            try {
              const frame = JSON.parse(raw);
              const delta = frame.choices?.[0]?.delta?.content || "";
              text += delta;
              setOutput(text);
              if (frame.lattice) setMeta(frame.lattice);
            } catch {
              /* ignore */
            }
          }
        }
      } else {
        const data = await res.json();
        setOutput(data.choices?.[0]?.message?.content || JSON.stringify(data));
        setMeta(data.lattice || null);
      }
    } catch (e) {
      setOutput(String(e));
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="panel p-4 space-y-3">
      <div className="flex items-end justify-between gap-3 flex-wrap">
        <div>
          <p className="text-xs uppercase tracking-[0.18em] text-signal-mist font-mono">Inference playground</p>
          <h3 className="font-display text-xl text-white mt-1">Route a request</h3>
        </div>
        <div className="flex gap-2 items-center">
          <select
            value={policy}
            onChange={(e) => setPolicy(e.target.value)}
            className="bg-ink-800 border border-ink-600 text-sm px-2 py-1.5 font-mono"
          >
            <option value="balanced">balanced</option>
            <option value="latency_first">latency_first</option>
            <option value="cost_first">cost_first</option>
            <option value="quality_first">quality_first</option>
            <option value="throughput">throughput</option>
            <option value="energy_efficient">energy_efficient</option>
            <option value="least_loaded">least_loaded</option>
          </select>
          <label className="text-xs font-mono text-signal-mist flex items-center gap-1.5">
            <input type="checkbox" checked={stream} onChange={(e) => setStream(e.target.checked)} />
            stream
          </label>
          <button
            onClick={run}
            disabled={busy}
            className="bg-signal-cyan text-ink-950 px-3 py-1.5 text-sm font-semibold disabled:opacity-50 hover:brightness-110 transition"
          >
            {busy ? "routing…" : "Send"}
          </button>
        </div>
      </div>
      <textarea
        value={prompt}
        onChange={(e) => setPrompt(e.target.value)}
        rows={3}
        className="w-full bg-ink-900 border border-ink-600 p-3 text-sm font-mono focus:outline-none focus:border-signal-cyan"
      />
      <pre className="min-h-[100px] whitespace-pre-wrap text-sm font-mono text-signal-cyan/90 bg-ink-950/60 p-3 border border-ink-700">
        {output || "—"}
      </pre>
      {meta && (
        <p className="text-xs font-mono text-signal-mist">
          node={String(meta.node_id || "—")} · ttft={String(meta.ttft_ms ?? "—")}ms · tps={String(meta.tokens_per_sec ?? "—")} · category={String(meta.category || "—")}
        </p>
      )}
    </div>
  );
}
