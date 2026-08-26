// Wire shapes + fetch helper for the HLD-010 audit log read API.
// Admin-only; non-admins get a 403 (UI hides the nav entry but the
// API enforces).
import { request } from './client';

export type AuditStatus = 'success' | 'failure' | 'denied';

export interface AuditLog {
  id: number;
  occurred_at: string;
  user_id?: number;
  user_email: string;
  role: string;
  ip: string;
  user_agent: string;
  action: string;
  resource_type: string;
  resource_id: string;
  resource_name: string;
  status: AuditStatus;
  error_code?: string;
  error_message?: string;
  payload_json?: string;
  request_id?: string;
}

export interface AuditListResp {
  items: AuditLog[];
  total: number;
}

export interface AuditListFilters {
  user_email?: string;
  action?: string;
  resource_type?: string;
  status?: AuditStatus | '';
  from?: string; // RFC3339
  to?: string; // RFC3339
  limit?: number;
  offset?: number;
}

export async function listAuditLogs(f: AuditListFilters = {}): Promise<AuditListResp> {
  const qs = new URLSearchParams();
  if (f.user_email) qs.set('user_email', f.user_email);
  if (f.action) qs.set('action', f.action);
  if (f.resource_type) qs.set('resource_type', f.resource_type);
  if (f.status) qs.set('status', f.status);
  if (f.from) qs.set('from', f.from);
  if (f.to) qs.set('to', f.to);
  if (f.limit != null) qs.set('limit', String(f.limit));
  if (f.offset != null) qs.set('offset', String(f.offset));
  const path = `/admin/audit-logs${qs.toString() ? '?' + qs.toString() : ''}`;
  return request<AuditListResp>('GET', path);
}
