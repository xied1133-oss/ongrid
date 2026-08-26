import { request } from './client';

// Reports API (HLD-014). Scheduled operational reports — list + detail
// + manual generate + schedule CRUD. Backend routes under /v1/reports
// and /v1/report-schedules.

export type ReportStatus = 'pending' | 'generating' | 'ready' | 'failed';
export type ReportKind = 'daily' | 'weekly' | 'monthly' | 'custom';

export type ReportListItem = {
  id: string;
  title: string;
  kind: ReportKind;
  status: ReportStatus;
  summary: string;
  period_start: string;
  period_end: string;
  generated_at?: string;
  created_at: string;
  schedule_id?: number; // cron dedup key; absent for run-now/manual reports
  task_id?: string; // owning-task back-ref (HLD-022), e.g. 'report-schedule:42'; set on scheduled + run-now
};

// --- ContentJSON shapes (mirror biz/report/content.go) ---

export type HeroStat = {
  key: string;
  label: string;
  value: number;
  unit?: string;
  delta_pct?: number;
  sparkline?: number[];
};

export type EntityRef = { key: string; name: string };

export type Paragraph = { text: string; entities?: EntityRef[] };

export type Narrative = { headline: string; paragraphs?: Paragraph[] };

export type KeyIncident = {
  id: number;
  title: string;
  severity: string;
  duration_min: number;
  status: string;
  root_cause_snippet?: string;
};

export type ToolCount = { tool: string; count: number };

export type ActionsSummary = {
  mutating_total: number;
  mutating_approved: number;
  safe_total: number;
  by_tool?: ToolCount[];
};

export type Advice = { text: string };

export type ResourceFacts = {
  available: boolean;
  cpu_avg: number;
  cpu_peak: number;
  mem_avg: number;
  mem_peak: number;
  disk_avg: number;
  disk_peak: number;
};

export type FleetFacts = {
  total: number;
  online: number;
  roles?: Record<string, number>;
};

export type ChangeFact = {
  at: string;
  action: string;
  resource_type: string;
  resource_name?: string;
  actor?: string;
};

export type AssetFacts = {
  new_agents: number;
  new_skills: number;
  new_repos: number;
};

export type UsageFacts = {
  sessions: number;
  prompt_tokens: number;
  completion_tokens: number;
};

export type ReportContent = {
  version: string;
  hero: HeroStat[];
  narrative: Narrative;
  resource: ResourceFacts;
  fleet: FleetFacts;
  key_incidents?: KeyIncident[];
  actions_summary: ActionsSummary;
  changes?: ChangeFact[];
  assets: AssetFacts;
  usage: UsageFacts;
  advice?: Advice[];
};

export type DeliveryResult = {
  channel_id: number;
  channel_type?: string;
  status: string;
  sent_at?: string;
  error?: string;
  fallback_used?: boolean;
};

export type ReportDetail = ReportListItem & {
  content?: ReportContent;
  content_md: string;
  timezone: string;
  schedule_id?: number;
  error_msg?: string;
  share_token?: string;
  delivery?: DeliveryResult[];
};

export type ReportSchedule = {
  id: number;
  name: string;
  description: string;
  kind: ReportKind;
  cron_spec: string;
  timezone: string;
  scope_json: string;
  channel_ids: number[];
  in_app_visible: boolean;
  agent_persona: string;
  prompt_override?: string;
  enabled: boolean;
  next_fire_at?: string;
  last_fire_at?: string;
  last_report_id?: string;
  created_at: string;
};

export type ScheduleInput = {
  name: string;
  description?: string;
  kind: ReportKind;
  cron_spec?: string;
  timezone?: string;
  scope_json?: string;
  channel_ids?: number[];
  in_app_visible?: boolean;
  prompt_override?: string;
};

// --- reports ---

export function listReports(params?: { status?: string; kind?: string; schedule_id?: number; task_id?: string; limit?: number; offset?: number }) {
  const q = new URLSearchParams();
  if (params?.status) q.set('status', params.status);
  if (params?.kind) q.set('kind', params.kind);
  if (params?.schedule_id != null) q.set('schedule_id', String(params.schedule_id));
  if (params?.task_id) q.set('task_id', params.task_id);
  if (params?.limit) q.set('limit', String(params.limit));
  if (params?.offset) q.set('offset', String(params.offset));
  const qs = q.toString();
  return request<{ reports: ReportListItem[]; total?: number }>('GET', `/reports${qs ? `?${qs}` : ''}`);
}

export function getReport(id: string) {
  return request<ReportDetail>('GET', `/reports/${id}`);
}

export function deleteReport(id: string) {
  return request<void>('DELETE', `/reports/${id}`);
}

export function generateNow(body: { kind?: ReportKind; timezone?: string; scope_json?: string }) {
  return request<ReportDetail>('POST', '/reports', body);
}

export function shareReport(id: string) {
  return request<{ share_token: string; path: string }>('POST', `/reports/${id}/share`, {});
}

// --- schedules ---

export function listSchedules() {
  return request<{ schedules: ReportSchedule[] }>('GET', '/report-schedules');
}

export function getSchedule(id: number) {
  return request<ReportSchedule>('GET', `/report-schedules/${id}`);
}

export function createSchedule(body: ScheduleInput) {
  return request<ReportSchedule>('POST', '/report-schedules', body);
}

export function updateSchedule(id: number, body: ScheduleInput) {
  return request<ReportSchedule>('PUT', `/report-schedules/${id}`, body);
}

export function deleteSchedule(id: number) {
  return request<void>('DELETE', `/report-schedules/${id}`);
}

export function toggleSchedule(id: number, enabled: boolean) {
  return request<ReportSchedule>('POST', `/report-schedules/${id}/toggle`, { enabled });
}

export function runScheduleNow(id: number) {
  return request<ReportDetail>('POST', `/report-schedules/${id}/run-now`, {});
}
