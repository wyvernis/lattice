export type GPUInfo = {
  index: number;
  name: string;
  utilization: number;
  memory_used_mb: number;
  memory_total_mb: number;
  temperature?: number;
  power_watts?: number;
};

export type LoadedModel = {
  name: string;
  quantization: string;
  backend: string;
  vram_mb: number;
  warm: boolean;
};

export type NodeStatus = {
  id: string;
  cluster: string;
  address: string;
  healthy: boolean;
  last_heartbeat: string;
  gpus: GPUInfo[];
  cpu_utilization: number;
  memory_used_mb: number;
  memory_total_mb: number;
  active_requests: number;
  queue_depth: number;
  tokens_per_sec: number;
  est_latency_ms: number;
  loaded_models: LoadedModel[];
  backends: string[];
  labels?: Record<string, string>;
  cost_per_million: number;
  energy_watts?: number;
};

export type ModelRecord = {
  id: string;
  name: string;
  version: string;
  provider: string;
  quantizations: string[];
  capabilities: string[];
  download_status: string;
  cost_per_million: number;
  quality_score: number;
  tags?: string[];
};

export type LiveRequest = {
  id: string;
  model: string;
  node_id: string;
  tenant: string;
  status: string;
  started_at: string;
  category?: string;
};

export type FailureEvent = {
  id: string;
  node_id: string;
  type: string;
  message: string;
  timestamp: string;
};

export type ClusterSnapshot = {
  nodes: NodeStatus[];
  models: ModelRecord[];
  live_requests: LiveRequest[];
  live_streams: { id: string; node_id: string; model: string; ttft_ms: number; tokens_per_sec: number }[];
  failures: FailureEvent[];
  active_nodes: number;
  total_nodes: number;
  queue_depth: number;
  active_models: number;
  timestamp: string;
};
