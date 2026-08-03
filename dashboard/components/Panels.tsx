"use client";

import type { ClusterSnapshot } from "@/lib/types";

export function NodeTable({ snap }: { snap: ClusterSnapshot }) {
  return (
    <div className="panel overflow-hidden">
      <div className="px-4 py-3 border-b border-ink-600 flex justify-between">
        <p className="text-xs uppercase tracking-[0.18em] text-signal-mist font-mono">Nodes</p>
        <p className="text-xs font-mono text-signal-cyan">{snap.active_nodes}/{snap.total_nodes} active</p>
      </div>
      <div className="overflow-x-auto">
        <table className="w-full text-left text-sm font-mono">
          <thead className="text-signal-mist text-xs uppercase tracking-wider">
            <tr className="border-b border-ink-700">
              <th className="px-4 py-2 font-medium">Node</th>
              <th className="px-4 py-2 font-medium">Health</th>
              <th className="px-4 py-2 font-medium">GPU</th>
              <th className="px-4 py-2 font-medium">Queue</th>
              <th className="px-4 py-2 font-medium">tok/s</th>
              <th className="px-4 py-2 font-medium">Models</th>
              <th className="px-4 py-2 font-medium">Backends</th>
            </tr>
          </thead>
          <tbody>
            {snap.nodes.length === 0 && (
              <tr>
                <td colSpan={7} className="px-4 py-6 text-signal-mist">
                  No workers heartbeating yet.
                </td>
              </tr>
            )}
            {snap.nodes.map((n) => (
              <tr key={n.id} className="border-b border-ink-800/80 hover:bg-ink-800/40 transition-colors">
                <td className="px-4 py-2.5">
                  <div className="text-white">{n.id}</div>
                  <div className="text-[11px] text-signal-mist">{n.cluster} · {n.address}</div>
                </td>
                <td className="px-4 py-2.5">
                  <span className={n.healthy ? "text-signal-cyan" : "text-signal-coral"}>
                    {n.healthy ? "healthy" : "down"}
                  </span>
                </td>
                <td className="px-4 py-2.5">
                  {Math.round((n.gpus?.[0]?.utilization ?? 0) * 100)}% ·{" "}
                  {n.gpus?.[0]?.memory_used_mb ?? 0}/{n.gpus?.[0]?.memory_total_mb ?? 0} MB
                </td>
                <td className="px-4 py-2.5">{n.queue_depth}</td>
                <td className="px-4 py-2.5">{n.tokens_per_sec.toFixed(1)}</td>
                <td className="px-4 py-2.5 text-xs">
                  {(n.loaded_models || []).map((m) => m.name).join(", ") || "—"}
                </td>
                <td className="px-4 py-2.5 text-xs">{(n.backends || []).join(", ")}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </div>
  );
}

export function ModelRegistry({ snap }: { snap: ClusterSnapshot }) {
  return (
    <div className="panel overflow-hidden">
      <div className="px-4 py-3 border-b border-ink-600">
        <p className="text-xs uppercase tracking-[0.18em] text-signal-mist font-mono">Model registry</p>
      </div>
      <div className="divide-y divide-ink-800 max-h-[320px] overflow-y-auto">
        {snap.models.map((m) => (
          <div key={m.id} className="px-4 py-3 flex justify-between gap-3">
            <div>
              <p className="font-mono text-sm text-white">{m.name}</p>
              <p className="text-[11px] text-signal-mist font-mono">
                {m.provider} · v{m.version} · {(m.capabilities || []).join(", ")}
              </p>
            </div>
            <div className="text-right text-[11px] font-mono">
              <p className="text-signal-amber">${m.cost_per_million}/M</p>
              <p className="text-signal-mist">q={m.quality_score} · {m.download_status}</p>
            </div>
          </div>
        ))}
      </div>
    </div>
  );
}

export function Failures({ snap }: { snap: ClusterSnapshot }) {
  return (
    <div className="panel overflow-hidden">
      <div className="px-4 py-3 border-b border-ink-600">
        <p className="text-xs uppercase tracking-[0.18em] text-signal-mist font-mono">Failures</p>
      </div>
      <div className="max-h-[200px] overflow-y-auto divide-y divide-ink-800">
        {(snap.failures || []).length === 0 && (
          <p className="px-4 py-4 text-sm text-signal-mist font-mono">No failures recorded.</p>
        )}
        {(snap.failures || []).slice().reverse().map((f) => (
          <div key={f.id + f.timestamp} className="px-4 py-2.5 text-xs font-mono">
            <span className="text-signal-coral">{f.type}</span>
            <span className="text-signal-mist"> · {f.node_id} · {f.message}</span>
          </div>
        ))}
      </div>
    </div>
  );
}
