"use client";

import { useEffect, useRef, useState } from "react";
import type { ClusterSnapshot } from "@/lib/types";

const empty: ClusterSnapshot = {
  nodes: [],
  models: [],
  live_requests: [],
  live_streams: [],
  failures: [],
  active_nodes: 0,
  total_nodes: 0,
  queue_depth: 0,
  active_models: 0,
  timestamp: new Date().toISOString(),
};

export function useCluster() {
  const [snap, setSnap] = useState<ClusterSnapshot>(empty);
  const [connected, setConnected] = useState(false);
  const [models, setModels] = useState(empty.models);
  const failCount = useRef(0);

  useEffect(() => {
    let alive = true;
    const poll = async () => {
      try {
        const res = await fetch("/api/cluster", { cache: "no-store" });
        if (!res.ok) throw new Error("cluster fetch failed");
        const data = (await res.json()) as ClusterSnapshot;
        if (alive) {
          setSnap(data);
          setConnected(true);
          failCount.current = 0;
        }
      } catch {
        failCount.current += 1;
        if (failCount.current > 2 && alive) setConnected(false);
      }
    };
    const modelsPoll = async () => {
      try {
        const res = await fetch("/api/models", { cache: "no-store" });
        if (!res.ok) return;
        const data = await res.json();
        if (alive) setModels(data);
      } catch {
        /* ignore */
      }
    };
    poll();
    modelsPoll();
    const a = setInterval(poll, 1000);
    const b = setInterval(modelsPoll, 5000);

    // Prefer websocket when available
    const proto = window.location.protocol === "https:" ? "wss" : "ws";
    const host = process.env.NEXT_PUBLIC_SCHEDULER_WS || `${window.location.hostname}:8082`;
    let ws: WebSocket | null = null;
    try {
      ws = new WebSocket(`${proto}://${host}/v1/ws/cluster`);
      ws.onopen = () => setConnected(true);
      ws.onmessage = (ev) => {
        try {
          const data = JSON.parse(ev.data) as ClusterSnapshot;
          setSnap(data);
          setConnected(true);
        } catch {
          /* ignore */
        }
      };
      ws.onclose = () => setConnected(false);
    } catch {
      /* polling fallback */
    }

    return () => {
      alive = false;
      clearInterval(a);
      clearInterval(b);
      ws?.close();
    };
  }, []);

  return { snap: { ...snap, models: models.length ? models : snap.models }, connected };
}
