"use client";

import ReactECharts from "echarts-for-react";
import type { ClusterSnapshot } from "@/lib/types";

export function MetricsCharts({ snap }: { snap: ClusterSnapshot }) {
  const gpuOption = {
    backgroundColor: "transparent",
    textStyle: { color: "#8ba3b8", fontFamily: "IBM Plex Mono" },
    grid: { left: 40, right: 16, top: 28, bottom: 28 },
    tooltip: { trigger: "axis" },
    xAxis: {
      type: "category",
      data: snap.nodes.map((n) => n.id.slice(0, 10)),
      axisLine: { lineStyle: { color: "#2a3644" } },
    },
    yAxis: {
      type: "value",
      max: 1,
      axisLabel: { formatter: (v: number) => `${Math.round(v * 100)}%` },
      splitLine: { lineStyle: { color: "#1e2833" } },
    },
    series: [
      {
        name: "GPU util",
        type: "bar",
        data: snap.nodes.map((n) => n.gpus?.[0]?.utilization ?? n.cpu_utilization),
        itemStyle: {
          color: {
            type: "linear",
            x: 0, y: 0, x2: 0, y2: 1,
            colorStops: [
              { offset: 0, color: "#3dd6c6" },
              { offset: 1, color: "#1a4a45" },
            ],
          },
        },
        barWidth: 18,
      },
    ],
  };

  const tpsOption = {
    backgroundColor: "transparent",
    textStyle: { color: "#8ba3b8", fontFamily: "IBM Plex Mono" },
    grid: { left: 48, right: 16, top: 28, bottom: 28 },
    tooltip: { trigger: "axis" },
    xAxis: {
      type: "category",
      data: snap.nodes.map((n) => n.id.slice(0, 10)),
      axisLine: { lineStyle: { color: "#2a3644" } },
    },
    yAxis: {
      type: "value",
      splitLine: { lineStyle: { color: "#1e2833" } },
    },
    series: [
      {
        name: "tokens/sec",
        type: "line",
        smooth: true,
        data: snap.nodes.map((n) => n.tokens_per_sec),
        areaStyle: { color: "rgba(232,168,56,0.15)" },
        lineStyle: { color: "#e8a838", width: 2 },
        itemStyle: { color: "#e8a838" },
      },
      {
        name: "queue",
        type: "line",
        smooth: true,
        data: snap.nodes.map((n) => n.queue_depth),
        lineStyle: { color: "#e85d4c", width: 2, type: "dashed" },
        itemStyle: { color: "#e85d4c" },
      },
    ],
  };

  return (
    <div className="grid grid-cols-1 lg:grid-cols-2 gap-4">
      <div className="panel p-3">
        <p className="text-xs uppercase tracking-[0.18em] text-signal-mist mb-1 font-mono">GPU utilization</p>
        <ReactECharts option={gpuOption} style={{ height: 220 }} opts={{ renderer: "canvas" }} />
      </div>
      <div className="panel p-3">
        <p className="text-xs uppercase tracking-[0.18em] text-signal-mist mb-1 font-mono">Throughput & queue</p>
        <ReactECharts option={tpsOption} style={{ height: 220 }} opts={{ renderer: "canvas" }} />
      </div>
    </div>
  );
}
