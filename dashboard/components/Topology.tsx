"use client";

import { useMemo } from "react";
import ReactFlow, {
  Background,
  BackgroundVariant,
  MarkerType,
  type Edge,
  type Node,
} from "reactflow";
import "reactflow/dist/style.css";
import type { ClusterSnapshot } from "@/lib/types";

export function Topology({ snap }: { snap: ClusterSnapshot }) {
  const { nodes, edges } = useMemo(() => {
    const ns: Node[] = [
      {
        id: "gateway",
        position: { x: 40, y: 140 },
        data: { label: "API Gateway" },
        style: nodeStyle("#3dd6c6"),
      },
      {
        id: "router",
        position: { x: 220, y: 60 },
        data: { label: "Router" },
        style: nodeStyle("#8ba3b8"),
      },
      {
        id: "scheduler",
        position: { x: 220, y: 220 },
        data: { label: "Scheduler" },
        style: nodeStyle("#e8a838"),
      },
    ];
    const es: Edge[] = [
      edge("gateway", "router"),
      edge("gateway", "scheduler"),
      edge("router", "scheduler"),
    ];
    snap.nodes.forEach((n, i) => {
      const id = n.id;
      ns.push({
        id,
        position: { x: 460, y: 40 + i * 110 },
        data: {
          label: `${n.id.slice(0, 14)}\n${n.healthy ? "healthy" : "down"} · q=${n.queue_depth}`,
        },
        style: nodeStyle(n.healthy ? "#3dd6c6" : "#e85d4c"),
      });
      es.push(edge("scheduler", id));
    });
    if (snap.nodes.length === 0) {
      ns.push({
        id: "empty",
        position: { x: 460, y: 140 },
        data: { label: "awaiting workers…" },
        style: nodeStyle("#2a3644"),
      });
    }
    return { nodes: ns, edges: es };
  }, [snap.nodes]);

  return (
    <div className="h-[320px] w-full rounded-sm overflow-hidden panel">
      <ReactFlow
        nodes={nodes}
        edges={edges}
        fitView
        proOptions={{ hideAttribution: true }}
        nodesDraggable={false}
        nodesConnectable={false}
        elementsSelectable={false}
        zoomOnScroll={false}
        panOnDrag
      >
        <Background variant={BackgroundVariant.Dots} gap={18} size={1} color="rgba(139,163,184,0.2)" />
      </ReactFlow>
    </div>
  );
}

function nodeStyle(accent: string): React.CSSProperties {
  return {
    background: "#161d25",
    border: `1px solid ${accent}`,
    color: "#e8eef4",
    borderRadius: 2,
    fontSize: 11,
    fontFamily: "var(--font-mono)",
    padding: 8,
    whiteSpace: "pre-line",
    minWidth: 120,
    boxShadow: `0 0 24px ${accent}22`,
  };
}

function edge(source: string, target: string): Edge {
  return {
    id: `${source}-${target}`,
    source,
    target,
    animated: true,
    style: { stroke: "rgba(61,214,198,0.45)", strokeWidth: 1.5 },
    markerEnd: { type: MarkerType.ArrowClosed, color: "rgba(61,214,198,0.7)" },
  };
}
