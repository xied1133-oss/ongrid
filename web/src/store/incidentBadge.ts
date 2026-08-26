// Lightweight badge counter for unacknowledged incidents. Polls every
// 30 s and exposes a single number (`open` count) — Sidebar's 告警 row
// renders a red dot + count when > 0, so operators on any tab see new
// pages without having to re-open Alerts.
//
// Why a store and not a hook with useState in Sidebar: multiple call
// sites (collapsed-icon-rail badge, expanded sub-item badge) want the
// same number, and the polling cost is one /alerts/incidents?status=open
// request per 30 s regardless of consumer count.
//
// 30 s cadence chosen to be much faster than rule eval cadence (1 m
// default) so a freshly fired incident shows up in <60 s wall-clock.
// We piggy-back on auth: if the auth store has no token, polling stops
// to avoid a 401 storm.
import { create } from 'zustand';
import { listIncidents } from '@/api/alerts';
import { useAuth } from './auth';

type State = {
  openCount: number;
  // The poll handle is kept so test code (and HMR) can reset it.
  _timer: number | null;
  start(): void;
  stop(): void;
  refresh(): Promise<void>;
};

const POLL_INTERVAL_MS = 30_000;

export const useIncidentBadge = create<State>((set, get) => ({
  openCount: 0,
  _timer: null,
  refresh: async () => {
    if (!useAuth.getState().token) return;
    try {
      const r = await listIncidents({ status: 'open', pageSize: 1 });
      set({ openCount: r.total ?? 0 });
    } catch {
      // Silent — sidebar badge is best-effort. Don't surface here; the
      // /alerts page itself will show the real error if the user clicks.
    }
  },
  start: () => {
    if (get()._timer != null) return;
    void get().refresh();
    const id = window.setInterval(() => {
      void get().refresh();
    }, POLL_INTERVAL_MS);
    set({ _timer: id });
  },
  stop: () => {
    const id = get()._timer;
    if (id != null) {
      window.clearInterval(id);
      set({ _timer: null });
    }
  },
}));
