import { request } from './client';

// Grafana dashboard JSON, walked at runtime by Monitor.tsx /
// PromQLPanel.tsx to render panels natively (no iframe).
//
// We model only the fields the renderer actually uses. Grafana's full
// dashboard schema is enormous and changes every minor release — keep
// the SPA loose so Grafana 10 / 11 / 12 panels all decode without the
// SPA needing per-version branches. Unknown fields are ignored.

// GrafanaThreshold is one step in `fieldConfig.defaults.thresholds.steps`.
// `value` may be null for the lowest band ("from -∞").
export type GrafanaThreshold = {
  color: string;
  value: number | null;
};

export type GrafanaThresholdsConfig = {
  mode: 'absolute' | 'percentage' | string;
  steps: GrafanaThreshold[];
};

export type GrafanaFieldConfig = {
  defaults?: {
    unit?: string;
    min?: number;
    max?: number;
    decimals?: number;
    thresholds?: GrafanaThresholdsConfig;
    [k: string]: unknown;
  };
  overrides?: unknown[];
};

export type GrafanaTarget = {
  expr?: string;
  refId?: string;
  legendFormat?: string;
  // Some panel types (e.g. logs) carry a Loki/Tempo query in `expr`
  // alongside a `datasource.type` field; we don't render those natively
  // but pass them through so the panel knows to fall back to deep-link.
  datasource?: { type?: string; uid?: string } | null;
};

export type GrafanaPanel = {
  id: number;
  type: string;
  title: string;
  description?: string;
  gridPos: { x: number; y: number; w: number; h: number };
  targets?: GrafanaTarget[];
  fieldConfig?: GrafanaFieldConfig;
  options?: Record<string, unknown>;
  // Row panels carry nested panels[]. We flatten before rendering — the
  // PromQLPanel renderer never sees a row.
  panels?: GrafanaPanel[];
  collapsed?: boolean;
};

export type GrafanaDashboard = {
  uid: string;
  title: string;
  panels: GrafanaPanel[];
  // templating.list[] holds dashboard variables ($device_id, $datasource).
  // We don't render the picker UI in v1; lib/grafanaVars.ts substitutes
  // a small allowlist directly from URL query params.
  templating?: { list?: Array<{ name: string; current?: { value?: unknown } }> };
};

export type GrafanaDashboardResp = {
  dashboard: GrafanaDashboard;
  meta?: Record<string, unknown>;
};

// fetchDashboard goes through the manager (NOT directly to Grafana) so
// the external-Grafana case works without CORS / cookie-sharing — the
// manager holds the api_key / sa_token credentials and does the
// authenticated GET on the SPA's behalf.
//
// Returns the parsed `{ dashboard, meta }` envelope. Throws ApiError on
// non-2xx so callers can show the "Grafana 暂不可达" empty state.
export async function fetchDashboard(uid: string): Promise<GrafanaDashboardResp> {
  return request<GrafanaDashboardResp>(
    'GET',
    `/observability/dashboards/${encodeURIComponent(uid)}`,
  );
}
