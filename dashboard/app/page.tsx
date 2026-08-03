"use client";

import { Topology } from "@/components/Topology";
import { MetricsCharts } from "@/components/MetricsCharts";
import { Playground } from "@/components/Playground";
import { Failures, ModelRegistry, NodeTable } from "@/components/Panels";
import { useCluster } from "@/lib/useCluster";

function Stat({ label, value, hint }: { label: string; value: string; hint?: string }) {
  return (
    <div className="panel px-4 py-3 animate-fade-up">
      <p className="text-[10px] uppercase tracking-[0.2em] text-signal-mist font-mono">{label}</p>
      <p className="font-display text-2xl mt-1 text-white">{value}</p>
      {hint && <p className="text-[11px] text-signal-mist font-mono mt-0.5">{hint}</p>}
    </div>
  );
}

export default function HomePage() {
  const { snap, connected } = useCluster();

  return (
    <main className="min-h-screen">
      <header className="border-b border-ink-600/80 bg-ink-950/50 backdrop-blur sticky top-0 z-20">
        <div className="max-w-7xl mx-auto px-5 py-4 flex items-center justify-between gap-4">
          <div>
            <p className="font-display text-3xl tracking-tight text-white">
              lattice
            </p>
            <p className="text-xs font-mono text-signal-mist mt-0.5">
              distributed LLM inference control plane
            </p>
          </div>
          <div className="flex items-center gap-3 text-xs font-mono">
            <span className="flex items-center gap-2">
              <span className={`h-2 w-2 rounded-full live-dot ${connected ? "bg-signal-cyan" : "bg-signal-coral"}`} />
              {connected ? "live" : "reconnecting"}
            </span>
            <span className="text-signal-mist hidden sm:inline">
              {new Date(snap.timestamp).toLocaleTimeString()}
            </span>
          </div>
        </div>
      </header>

      <div className="max-w-7xl mx-auto px-5 py-6 space-y-5">
        <section className="grid grid-cols-2 md:grid-cols-4 gap-3">
          <Stat label="Active nodes" value={`${snap.active_nodes}`} hint={`of ${snap.total_nodes}`} />
          <Stat label="Queue depth" value={`${snap.queue_depth}`} hint="cluster-wide" />
          <Stat label="Active models" value={`${snap.active_models}`} hint="resident in VRAM" />
          <Stat
            label="Streams"
            value={`${snap.live_streams?.length ?? 0}`}
            hint={`${snap.live_requests?.length ?? 0} live reqs`}
          />
        </section>

        <section className="grid grid-cols-1 lg:grid-cols-5 gap-4">
          <div className="lg:col-span-3 space-y-2">
            <p className="text-xs uppercase tracking-[0.18em] text-signal-mist font-mono">Cluster topology</p>
            <Topology snap={snap} />
          </div>
          <div className="lg:col-span-2">
            <Playground />
          </div>
        </section>

        <MetricsCharts snap={snap} />

        <section className="grid grid-cols-1 lg:grid-cols-3 gap-4">
          <div className="lg:col-span-2">
            <NodeTable snap={snap} />
          </div>
          <div className="space-y-4">
            <ModelRegistry snap={snap} />
            <Failures snap={snap} />
          </div>
        </section>

        <footer className="pt-4 pb-8 text-center text-[11px] font-mono text-signal-mist/70">
          lattice · gateway → router → scheduler → worker · OpenAI-compatible API
        </footer>
      </div>
    </main>
  );
}
