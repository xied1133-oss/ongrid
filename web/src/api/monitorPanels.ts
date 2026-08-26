import { request } from './client';

// MonitorPanel mirrors the wire shape of the manager-side
// monitor_panels row (see internal/manager/model/monitor/model.go).
//
// The Monitor page reads the list and renders each panel via
// PromQLPanel — the wire shape is intentionally a superset of
// GrafanaPanel (id + type + title + targets[].expr + fieldConfig.unit)
// so the existing renderer just works.
export type MonitorPanelType = 'timeseries' | 'stat' | 'gauge';

export type MonitorPanel = {
  id: number;
  title: string;
  type: MonitorPanelType;
  promql: string;
  legend: string;
  unit: string;
  ordinal: number;
  last_sync_error?: string;
  last_sync_at?: string;
  created_at: string;
  updated_at: string;
};

export type MonitorPanelInput = {
  title: string;
  type: MonitorPanelType;
  promql: string;
  legend?: string;
  unit?: string;
  ordinal?: number;
};

export type MonitorPanelPatch = Partial<MonitorPanelInput>;

type ListResp = { panels: MonitorPanel[] };

// listMonitorPanels fetches every user-managed panel ordered by ordinal.
// Returns [] when the table is empty (fresh install).
export async function listMonitorPanels(): Promise<MonitorPanel[]> {
  const resp = await request<ListResp>('GET', '/monitor/panels');
  return resp.panels ?? [];
}

export async function createMonitorPanel(input: MonitorPanelInput): Promise<MonitorPanel> {
  return request<MonitorPanel>('POST', '/monitor/panels', input);
}

export async function updateMonitorPanel(
  id: number,
  patch: MonitorPanelPatch,
): Promise<MonitorPanel> {
  return request<MonitorPanel>('PATCH', `/monitor/panels/${id}`, patch);
}

export async function deleteMonitorPanel(id: number): Promise<void> {
  await request<unknown>('DELETE', `/monitor/panels/${id}`);
}
