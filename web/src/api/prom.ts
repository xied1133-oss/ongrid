import { request } from './client';

// Wire shape mirrors POST /v1/prometheus/query_range. The backend hands
// us the Prom matrix verbatim so the SPA can pivot however it needs —
// per-cpu, per-mountpoint, per-device, per-edge — without the manager
// caring about the panel-specific reshaping.
//
// Why the matrix entries are typed as `[number, string]` rather than
// `[number, number]`: Prom serializes the value as a JSON string so it
// can carry +Inf / -Inf / NaN losslessly. Callers must `parseFloat` (and
// treat NaN/Inf as gaps if they want recharts to break the line).
export type PromMatrixSample = [number, string];
export type PromMatrixSeries = {
  metric: Record<string, string>;
  values: PromMatrixSample[];
};
export type PromQueryRangeResp = {
  result_type: 'matrix';
  result: PromMatrixSeries[];
  from: string;
  to: string;
};

export type PromQueryRangeInput = {
  expr: string;
  // RFC3339 strings — easier for the backend to parse than unix-ms;
  // matches the LogQL / PromQL Explorer convention.
  start: string;
  end: string;
  // Go duration string ("30s", "1m", "5m", ...).
  step: string;
};

// queryRange is the single PromQL entry point used by Monitor's native
// PromQLPanel. It runs through /v1/prometheus/query_range, which is
// gated by the same auth middleware as the rest of /api/v1 (any logged-
// in user). The manager applies a 30s timeout + 4 KiB expr cap; errors
// surface as ApiError so PromQLPanel can show inline red copy without
// taking down the whole grid.
export function queryRange(input: PromQueryRangeInput): Promise<PromQueryRangeResp> {
  return request<PromQueryRangeResp>('POST', '/prometheus/query_range', input);
}
