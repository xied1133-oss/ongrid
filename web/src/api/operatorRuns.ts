import { getToken } from '@/store/auth';
import { request } from './client';

export type OperatorCommand = 'ping' | 'dig' | 'tcp' | 'http';
export type OperatorRunStatus = 'running' | 'success' | 'partial' | 'error' | 'cancelled';

export type OperatorRunEvent = {
  id: string;
  type: string;
  ts: string;
  run_id: string;
  edge_id?: number;
  stream?: 'stdout' | 'stderr' | string;
  message?: string;
  status?: OperatorRunStatus | string;
  exit_code?: number;
  duration_ms?: number;
};

export type OperatorEdgeResult = {
  edge_id: number;
  status: OperatorRunStatus | string;
  allowed: boolean;
  reason?: string;
  stdout?: string;
  stderr?: string;
  exit_code: number;
  truncated?: boolean;
  duration_ms?: number;
  error?: string;
};

export type OperatorRun = {
  id: string;
  command: OperatorCommand;
  title: string;
  status: OperatorRunStatus;
  edge_ids: number[];
  started_at: string;
  finished_at?: string;
  events?: OperatorRunEvent[];
  results?: OperatorEdgeResult[];
};

export type CreateOperatorRunInput = {
  edge_ids: number[];
  command: OperatorCommand;
  args: Record<string, unknown>;
  timeout_ms?: number;
};

export type OperatorNetNSList = {
  edge_id: number;
  namespaces: string[];
};

export function createOperatorRun(input: CreateOperatorRunInput) {
  return request<OperatorRun>('POST', '/operator-runs', input);
}

export function getOperatorRun(id: string) {
  return request<OperatorRun>('GET', `/operator-runs/${encodeURIComponent(id)}`);
}

export function cancelOperatorRun(id: string) {
  return request<OperatorRun>('POST', `/operator-runs/${encodeURIComponent(id)}/cancel`, {});
}

export function listOperatorNetNS(edgeID: number, signal?: AbortSignal) {
  return request<OperatorNetNSList>('GET', `/operator-runs/netns?edge_id=${encodeURIComponent(String(edgeID))}`, undefined, { signal });
}

export async function streamOperatorRunEvents(
  id: string,
  onEvent: (event: OperatorRunEvent) => void,
  signal?: AbortSignal,
) {
  const headers: Record<string, string> = { Accept: 'text/event-stream' };
  const token = getToken();
  if (token) headers.Authorization = `Bearer ${token}`;
  const res = await fetch(`/api/v1/operator-runs/${encodeURIComponent(id)}/events`, { headers, signal });
  if (!res.ok) {
    throw new Error(`operator events HTTP ${res.status}`);
  }
  if (!res.body) return;
  const reader = res.body.getReader();
  const decoder = new TextDecoder();
  let buffer = '';
  for (;;) {
    const { value, done } = await reader.read();
    if (done) break;
    buffer += decoder.decode(value, { stream: true });
    let idx = buffer.indexOf('\n\n');
    while (idx >= 0) {
      const frame = buffer.slice(0, idx);
      buffer = buffer.slice(idx + 2);
      dispatchFrame(frame, onEvent);
      idx = buffer.indexOf('\n\n');
    }
  }
  if (buffer.trim()) dispatchFrame(buffer, onEvent);
}

function dispatchFrame(frame: string, onEvent: (event: OperatorRunEvent) => void) {
  const data = frame.split('\n').filter((line) => line.startsWith('data:')).map((line) => line.slice(5).trimStart()).join('\n');
  if (!data) return;
  try {
    onEvent(JSON.parse(data) as OperatorRunEvent);
  } catch {
    // Bad event frame means this one update is unusable; the stream can continue.
  }
}
