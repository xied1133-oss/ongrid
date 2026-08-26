// webshell.ts — thin WebSocket factory for the WebSSH endpoint, plus
// typed wrappers for the audit + kill endpoints.
//
// Backend contract (manager/server/webshell):
//   wss://<host>/api/v1/devices/{device_id}/shell        (WS upgrade)
//   GET    /api/v1/webshell/sessions                     (audit list)
//   DELETE /api/v1/webshell/sessions/{id}                (admin kill)
//
// Auth: the manager auth.Middleware reads `Authorization: Bearer <jwt>`,
// falling back to `?token=<jwt>` for native browser WebSockets — confirmed
// by reading internal/pkg/auth/middleware.go:extractBearer. So we just
// append the token as a query string. We still negotiate the
// `ongrid.shell.v1` subprotocol because the gorilla server-side upgrader
// declares it; without a matching client subprotocol the server upgrade
// still succeeds (gorilla treats subprotocol as optional), but echoing it
// back keeps things tidy and lets the server log a stable name.
//
// The caller is responsible for setting `binaryType = 'arraybuffer'`,
// sending the first `ShellOpen` text frame, and tearing down on close.

import { request } from './client';
import { getToken } from '@/store/auth';

export type OpenShellParams = {
  // Optional WS path override (defaults to same-origin /api/v1).
  baseUrl?: string;
};

const SUBPROTOCOL = 'ongrid.shell.v1';

export function openShellSocket(
  deviceId: number | string,
  token: string,
  params: OpenShellParams = {},
): WebSocket {
  const id = encodeURIComponent(String(deviceId));
  const qs = new URLSearchParams({ token }).toString();

  let url: string;
  if (params.baseUrl) {
    url = `${params.baseUrl.replace(/\/$/, '')}/api/v1/devices/${id}/shell?${qs}`;
  } else {
    // Derive ws/wss from the current page so dev (vite proxy) and prod
    // (nginx) both work without config.
    const proto = window.location.protocol === 'https:' ? 'wss:' : 'ws:';
    url = `${proto}//${window.location.host}/api/v1/devices/${id}/shell?${qs}`;
  }

  const ws = new WebSocket(url, [SUBPROTOCOL]);
  ws.binaryType = 'arraybuffer';
  return ws;
}

// ShellOpen is the first text frame the browser must send post-upgrade.
// `ssh_host` defaults to "127.0.0.1:22" when empty — the edge agent runs
// on the device's host network so localhost loops back to the OS sshd.
export type ShellOpenFrame = {
  type: 'open';
  cols: number;
  rows: number;
  term: string;
  ssh_user: string;
  ssh_pass: string;
  ssh_host?: string;
};

export type ShellResizeFrame = {
  type: 'resize';
  cols: number;
  rows: number;
};

export type ShellCloseFrame = {
  type: 'close';
};

export type ShellControlFrameOut =
  | ShellOpenFrame
  | ShellResizeFrame
  | ShellCloseFrame;

// ShellControlFrameIn is what the manager pushes back over text frames.
// `ready` confirms the SSH session is up; `auth_error` / `exit` are
// terminal — the UI should print and let the WS close naturally.
export type ShellControlFrameIn =
  | { type: 'ready' }
  | { type: 'auth_error'; message: string }
  | { type: 'exit'; exit_code: number; message?: string };

export function sendControl(ws: WebSocket, frame: ShellControlFrameOut): void {
  if (ws.readyState !== WebSocket.OPEN) return;
  ws.send(JSON.stringify(frame));
}

// ---------------------------------------------------------------------------
// Audit + kill endpoints (used by /settings/webshell)
// ---------------------------------------------------------------------------

// TerminatedBy mirrors backend wsmodel.TerminatedBy* — kept as a string
// union so callers can switch exhaustively. New backend values land here
// without breaking the build because the field is also typed as `string`
// inside ShellSession (defensive widening).
export type WebshellTerminatedBy =
  | 'user'
  | 'idle'
  | 'disconnect'
  | 'admin_kill'
  | 'ssh_auth_fail'
  | 'ssh_exit'
  | 'device_offline';

export type ShellSession = {
  id: string;
  ongrid_user_id: number;
  ssh_user: string;
  device_id: number;
  edge_id: number;
  started_at: string;
  ended_at?: string | null;
  bytes_stdin: number;
  bytes_stdout: number;
  exit_code: number;
  terminated_by?: WebshellTerminatedBy | string;
  is_active: boolean;
};

export type ShellSessionListResp = {
  items: ShellSession[];
  total: number;
};

export function listShellSessions(): Promise<ShellSessionListResp> {
  return request<ShellSessionListResp>('GET', '/webshell/sessions');
}

export function killShellSession(id: string): Promise<void> {
  return request<void>('DELETE', `/webshell/sessions/${encodeURIComponent(id)}`);
}

// probeShellPreflight — issue a plain GET against the same WS path so we
// can surface the HTTP status (429 / 503 / 403) before the WS upgrade
// errors out. Browsers don't expose upgrade-time HTTP status to JS, so a
// GET probe is the cheapest way to translate "WS closed 1006 immediately"
// into a meaningful error. Returns { status, message } on success; null
// when the probe itself fails (e.g. network).
//
// Backend GET on the WS path without an Upgrade header returns
// http.Error(...) with status 400 ("Bad Request" — gorilla rejects),
// after the auth + concurrency checks have already run. So 429/503/403
// are returned authoritatively before the upgrade test.
export async function probeShellPreflight(
  deviceId: number | string,
): Promise<{ status: number; message: string } | null> {
  const id = encodeURIComponent(String(deviceId));
  try {
    const res = await fetch(`/api/v1/devices/${id}/shell`, {
      method: 'GET',
      headers: {
        Accept: 'text/plain',
        // Reuse the same bearer the WS uses; can't set headers on
        // native WebSocket so this is the only place auth is in a
        // header rather than ?token=.
        ...buildAuthHeader(),
      },
    });
    let message = '';
    try {
      message = (await res.text()).trim();
    } catch {
      /* noop */
    }
    return { status: res.status, message };
  } catch {
    return null;
  }
}

function buildAuthHeader(): Record<string, string> {
  const t = getToken();
  return t ? { Authorization: `Bearer ${t}` } : {};
}
