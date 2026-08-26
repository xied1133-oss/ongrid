// Hosted-page (serve_page artifact) client. Pages are NOT public — in-app
// viewing is authed (GET /api/pages/<id>, fetched with the bearer and rendered
// via iframe srcdoc). Off-platform sharing is an explicit, TTL-bounded mint
// (POST /v1/pages/<id>/share -> public /api/p/<token>), mirroring reports.
import { request } from './client';
import { getToken } from '@/store/auth';

export type HostedPage = {
  id: string;
  title: string;
  created_at: string;
  url: string; // /api/pages/<id> (authed)
  size_bytes?: number;
  source?: string; // origin code: 'chat' | 'workflow' | '' (legacy)
};

export function listPages() {
  return request<{ items: HostedPage[]; total: number }>('GET', '/pages');
}

export function deletePage(id: string) {
  return request<void>('DELETE', `/pages/${encodeURIComponent(id)}`);
}

// fetchPageHTML pulls a page's HTML with the bearer (the route is authed, not
// public) for in-app rendering via iframe srcdoc.
export async function fetchPageHTML(id: string): Promise<string> {
  const token = getToken();
  const resp = await fetch(`/api/pages/${encodeURIComponent(id)}`, {
    headers: token ? { Authorization: `Bearer ${token}` } : {},
  });
  if (!resp.ok) throw new Error(`page ${id}: ${resp.status}`);
  return resp.text();
}

// sharePage mints a TTL-bounded public share link (login-free) for a page.
export function sharePage(id: string) {
  return request<{ share_token: string; path: string; expires_at: string }>(
    'POST',
    `/pages/${encodeURIComponent(id)}/share`,
  );
}
