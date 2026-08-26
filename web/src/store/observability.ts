import { create } from 'zustand';
import { createJSONStorage, persist } from 'zustand/middleware';

// Per-browser drilldown config used by lib/drilldown.ts to build the
// "open in Grafana" deep-link URL. The Grafana root URL itself is the
// admin-configured system_settings.grafana.root_url — we only keep
// dashboard UID + orgId here because those are user-customizable per
// browser (different teams may pin different default dashboards).
//
// TODO: route grafanaBaseUrl through system_settings too so we have a
// single source of truth. For now drilldown.ts reads it from this store
// AND falls back to same-origin /grafana, which works for the embedded
// case and was the pre-PR-2 behavior.
export type ObservabilityState = {
  grafanaBaseUrl: string;
  grafanaDatasourceUid: string;
  grafanaDashboardUid: string;
  grafanaOrgId: string;
  setConfig(patch: Partial<Pick<ObservabilityState, 'grafanaBaseUrl' | 'grafanaDatasourceUid' | 'grafanaDashboardUid' | 'grafanaOrgId'>>): void;
  reset(): void;
};

const DEFAULTS = {
  grafanaBaseUrl: '',
  grafanaDatasourceUid: 'ongrid-prometheus',
  grafanaDashboardUid: 'ongrid-server-detail',
  grafanaOrgId: '1',
};

export const useObservability = create<ObservabilityState>()(
  persist(
    (set) => ({
      ...DEFAULTS,
      setConfig: (patch) => set((state) => ({ ...state, ...patch })),
      reset: () => set({ ...DEFAULTS }),
    }),
    {
      name: 'ongrid.observability',
      storage: createJSONStorage(() => localStorage),
    }
  )
);
