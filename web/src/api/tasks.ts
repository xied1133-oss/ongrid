// Unified tasks API (HLD-022 Phase 2). One surface over two sources: recurring
// report tasks (report_schedules) + stored oneoff tasks. The id is a string ref
// ("report-schedule:<n>" | "oneoff:<uuid>") that's also the artifact task_id, so
// a task's reports are fetched via listReports({ task_id: task.id }).
import { request } from './client';

export type UnifiedTask = {
  id: string; // "report-schedule:<n>" | "oneoff:<uuid>"
  kind: 'recurring_report' | 'oneoff';
  title: string;
  report_kind: string; // daily | weekly | monthly | custom
  trigger: string; // "cron · tz" for recurring; "oneoff" for one-shot
  enabled: boolean;
  status: string;
  next_fire_at?: string;
  schedule_id?: number; // recurring only — numeric id for schedule CRUD
  created_at: string;
};

export function listTasks() {
  return request<{ tasks: UnifiedTask[] }>('GET', '/tasks');
}

export function getTask(id: string) {
  return request<UnifiedTask>('GET', `/tasks/${encodeURIComponent(id)}`);
}

// createOneoffTask creates a one-shot task and immediately generates its report.
export function createOneoffTask(body: { kind?: string; title?: string; timezone?: string; scope_json?: string }) {
  return request<UnifiedTask>('POST', '/tasks/oneoff', body);
}

// rerunTask re-generates a oneoff task's report.
export function rerunTask(id: string) {
  return request<UnifiedTask>('POST', `/tasks/${encodeURIComponent(id)}/run`, {});
}

export function deleteTask(id: string) {
  return request<void>('DELETE', `/tasks/${encodeURIComponent(id)}`);
}
