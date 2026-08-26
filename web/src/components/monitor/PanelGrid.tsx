// PanelGrid — lays out PromQL-driven panels on Grafana's 24-column grid.
// Each panel's gridPos.{x,y,w,h} maps directly to CSS grid coordinates;
// we don't honor `y` strictly because CSS grid auto-flow handles
// vertical stacking and panels are rarely sparse-on-Y in practice.
// `h` is in Grafana grid units (~30px each); scaled to ~36px so the
// embedded view feels comfortable in our denser SPA chrome.
//
// On screens narrower than ~1200px we collapse to a single column so
// each panel stays readable on laptops/tablets — Grafana JSON's tight
// 6-col widths become unreadable below that.
import { useEffect, useState } from 'react';
import { type GrafanaPanel } from '@/api/grafana';
import { PromQLPanel } from '@/components/PromQLPanel';

export function PanelGrid({
  panels,
  range,
  fromMs,
  toMs,
  tick,
  grafanaBase,
  dashboardUid,
  orgId,
  onOpenPanel,
}: {
  panels: GrafanaPanel[];
  range: string;
  fromMs: number;
  toMs: number;
  tick: number;
  grafanaBase: string;
  dashboardUid: string;
  orgId: string;
  onOpenPanel: (panelId: number) => void;
}) {
  const [narrow, setNarrow] = useState<boolean>(
    typeof window !== 'undefined' ? window.innerWidth < 1200 : false,
  );
  useEffect(() => {
    const onResize = () => setNarrow(window.innerWidth < 1200);
    window.addEventListener('resize', onResize);
    return () => window.removeEventListener('resize', onResize);
  }, []);

  return (
    <div
      className="grid gap-3"
      style={{
        gridTemplateColumns: narrow ? '1fr' : 'repeat(24, minmax(0, 1fr))',
        gridAutoRows: '36px',
      }}
    >
      {panels.map((p) => {
        const w = Math.max(1, Math.min(24, p.gridPos?.w ?? 12));
        const h = Math.max(4, p.gridPos?.h ?? 8);
        const style = narrow
          ? { gridColumn: '1 / -1', gridRow: `span ${Math.max(6, h)}`, minHeight: 220 }
          : { gridColumn: `span ${w}`, gridRow: `span ${h}`, minHeight: 180 };
        return (
          <div key={p.id} style={style} className="min-w-0">
            <PromQLPanel
              panel={p}
              range={range}
              tick={tick}
              grafanaBase={grafanaBase}
              dashboardUid={dashboardUid}
              orgId={orgId}
              fromMs={fromMs}
              toMs={toMs}
              onOpenInGrafana={onOpenPanel}
            />
          </div>
        );
      })}
    </div>
  );
}
