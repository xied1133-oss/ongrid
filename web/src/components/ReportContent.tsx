import { Fragment, useEffect, useRef, useState } from 'react';
import { Link } from 'react-router-dom';
import { cn } from '@/lib/cn';
import { useI18n } from '@/i18n/locale';
import type {
  ChangeFact,
  HeroStat,
  KeyIncident,
  Paragraph,
  ReportContent as ReportContentT,
  ResourceFacts,
} from '@/api/reports';

// ReportContent renders a ContentJSON report body as four colour-coded
// thematic rows (HLD-014, 2026-06-06 redesign): cluster posture, alerts
// & response, new assets, usage — with the LLM narrative between the
// first two, and changes / advice below. Zero chart deps (rAF count-up,
// inline SVG sparkline).

// --- count-up (rAF, no deps) ---
function useCountUp(target: number, durationMs = 800): number {
  const [val, setVal] = useState(0);
  const startRef = useRef<number | null>(null);
  useEffect(() => {
    startRef.current = null;
    let raf = 0;
    const step = (ts: number) => {
      if (startRef.current === null) startRef.current = ts;
      const p = Math.min(1, (ts - startRef.current) / durationMs);
      setVal(target * (1 - Math.pow(1 - p, 3)));
      if (p < 1) raf = requestAnimationFrame(step);
      else setVal(target);
    };
    raf = requestAnimationFrame(step);
    return () => cancelAnimationFrame(raf);
  }, [target, durationMs]);
  return val;
}

function fmtNum(v: number): string {
  return Number.isInteger(v) ? String(v) : v.toFixed(1);
}

function fmtTokens(n: number): string {
  if (n >= 1_000_000) return (n / 1_000_000).toFixed(1) + 'M';
  if (n >= 1_000) return (n / 1_000).toFixed(1) + 'K';
  return String(n);
}

function Sparkline({ points, className }: { points: number[]; className?: string }) {
  if (!points || points.length < 2) return null;
  const w = 56;
  const h = 14;
  const max = Math.max(...points, 1);
  const min = Math.min(...points, 0);
  const span = max - min || 1;
  const step = w / (points.length - 1);
  const d = points
    .map((p, i) => `${i === 0 ? 'M' : 'L'}${(i * step).toFixed(1)},${(h - ((p - min) / span) * h).toFixed(1)}`)
    .join(' ');
  return (
    <svg width={w} height={h} viewBox={`0 0 ${w} ${h}`} className={className} aria-hidden="true">
      <path d={d} fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round" />
    </svg>
  );
}

// --- colour themes per row ---
type Tone = 'indigo' | 'rose' | 'violet' | 'cyan';

// Flat, Apple-ish palette: colour shows only in the number + a small
// dot beside the row title. Cards are flat (no gradient, no raised
// edge); the single accent keeps it calm.
const TONE: Record<Tone, { num: string; dot: string; bar: string }> = {
  indigo: { num: 'text-indigo-400', dot: 'bg-indigo-400', bar: 'bg-indigo-400' },
  rose: { num: 'text-rose-400', dot: 'bg-rose-400', bar: 'bg-rose-400' },
  violet: { num: 'text-violet-400', dot: 'bg-violet-400', bar: 'bg-violet-400' },
  cyan: { num: 'text-cyan-400', dot: 'bg-cyan-400', bar: 'bg-cyan-400' },
};

// StatCard — flat card, one big colour-toned number. No gradient / bevel.
function StatCard({
  tone,
  label,
  value,
  unit,
  sub,
  sparkline,
  bar,
}: {
  tone: Tone;
  label: string;
  value: number;
  unit?: string;
  sub?: string;
  sparkline?: number[];
  bar?: number; // 0..100 utilisation
}) {
  const t = TONE[tone];
  const animated = useCountUp(value);
  return (
    <div className="rounded-xl border border-zinc-800 bg-zinc-900/40 p-4">
      <div className="text-xs text-zinc-500">{label}</div>
      <div className="mt-1.5 flex items-baseline gap-1">
        <span className={cn('text-3xl font-semibold tabular-nums', t.num)}>{fmtNum(animated)}</span>
        {unit && <span className="text-xs text-zinc-500">{unit}</span>}
      </div>
      {sub && <div className="mt-1 text-[11px] text-zinc-500">{sub}</div>}
      {sparkline && sparkline.length >= 2 && <Sparkline points={sparkline} className={cn('mt-2', t.num)} />}
      {typeof bar === 'number' && (
        <div className="mt-2.5 h-1 overflow-hidden rounded-full bg-zinc-800">
          <div
            className={cn('h-full rounded-full', bar >= 85 ? 'bg-red-400' : bar >= 60 ? 'bg-amber-400' : t.bar)}
            style={{ width: `${Math.min(100, Math.max(3, bar))}%` }}
          />
        </div>
      )}
    </div>
  );
}

function Row({
  tone,
  title,
  desc,
  children,
}: {
  tone: Tone;
  title: string;
  desc?: string;
  children: React.ReactNode;
}) {
  const t = TONE[tone];
  return (
    <section>
      <div className="mb-2.5 flex items-center gap-2">
        <span className={cn('h-1.5 w-1.5 rounded-full', t.dot)} />
        <h3 className="text-sm font-medium text-zinc-200">{title}</h3>
        {desc && <span className="text-[11px] text-zinc-600">{desc}</span>}
      </div>
      {children}
    </section>
  );
}

// EntityText parses {{entity:kind:id|name}} → clickable chips.
const ENTITY_RE = /\{\{entity:([a-z]+):(\d+)\|([^}]*)\}\}/g;
function entityHref(kind: string, id: string): string | null {
  if (kind === 'edge') return `/devices/${id}`;
  if (kind === 'incident') return `/alerts/incidents/${id}`;
  return null;
}
function EntityText({ text }: { text: string }) {
  const parts: React.ReactNode[] = [];
  let last = 0;
  let m: RegExpExecArray | null;
  ENTITY_RE.lastIndex = 0;
  let i = 0;
  while ((m = ENTITY_RE.exec(text)) !== null) {
    if (m.index > last) parts.push(<Fragment key={`t${i}`}>{text.slice(last, m.index)}</Fragment>);
    const [, kind, id, name] = m;
    const href = entityHref(kind, id);
    parts.push(
      href ? (
        <Link key={`e${i}`} to={href} className="mx-0.5 inline-flex items-center rounded border border-indigo-500/40 bg-indigo-500/10 px-1 py-0.5 text-[12px] text-indigo-300 hover:bg-indigo-500/20">
          {name}
        </Link>
      ) : (
        <span key={`e${i}`} className="mx-0.5 rounded bg-zinc-800 px-1 text-[12px] text-zinc-300">{name}</span>
      ),
    );
    last = m.index + m[0].length;
    i++;
  }
  if (last < text.length) parts.push(<Fragment key="tail">{text.slice(last)}</Fragment>);
  return <>{parts}</>;
}

const SEV_DOT: Record<string, string> = { critical: 'bg-red-500', warning: 'bg-amber-500', info: 'bg-sky-500' };
function IncidentRow({ ki }: { ki: KeyIncident }) {
  return (
    <Link to={`/alerts/incidents/${ki.id}`} className="flex items-center gap-2 rounded-md border border-zinc-800 bg-zinc-900/40 px-3 py-2 text-sm hover:border-zinc-700">
      <span className={cn('h-2 w-2 shrink-0 rounded-full', SEV_DOT[ki.severity] ?? 'bg-zinc-600')} />
      <span className="text-zinc-400">I-{ki.id}</span>
      <span className="flex-1 truncate text-zinc-200">{ki.title}</span>
      {ki.root_cause_snippet && <span className="hidden truncate text-xs text-zinc-500 md:inline">{ki.root_cause_snippet}</span>}
      <span className="shrink-0 text-xs text-zinc-500">{ki.duration_min}m · {ki.status}</span>
    </Link>
  );
}

function ChangeRow({ c }: { c: ChangeFact }) {
  return (
    <div className="flex items-center gap-2 rounded-md border border-zinc-800 bg-zinc-900/30 px-3 py-1.5 text-xs text-zinc-400">
      <span className="tabular-nums text-zinc-500">{c.at.slice(5, 16).replace('T', ' ')}</span>
      <span className="rounded bg-zinc-800 px-1.5 py-0.5 text-zinc-300">{c.action}</span>
      {c.resource_name && <span className="truncate text-zinc-400">{c.resource_name}</span>}
      {c.actor && <span className="ml-auto shrink-0 text-zinc-600">{c.actor}</span>}
    </div>
  );
}

export function ReportContentView({ content }: { content: ReportContentT }) {
  const { tr } = useI18n();
  const paras: Paragraph[] = content.narrative?.paragraphs ?? [];
  const incidents = content.key_incidents ?? [];
  const changes = content.changes ?? [];
  const advice = content.advice ?? [];
  const res = content.resource;
  const fleet = content.fleet ?? { total: 0, online: 0 };
  const a = content.actions_summary ?? { mutating_total: 0, mutating_approved: 0, safe_total: 0 };
  const assets = content.assets ?? { new_agents: 0, new_skills: 0, new_repos: 0 };
  const usage = content.usage ?? { sessions: 0, prompt_tokens: 0, completion_tokens: 0 };

  const onlinePct = fleet.total > 0 ? Math.round((fleet.online / fleet.total) * 100) : 0;
  const resolved = incidents.filter((i) => i.status === 'resolved').length;
  const mttr = (() => {
    const r = incidents.filter((i) => i.status === 'resolved');
    if (r.length === 0) return 0;
    return Math.round(r.reduce((s, i) => s + i.duration_min, 0) / r.length);
  })();

  return (
    <div className="space-y-7">
      {/* ROW 1 — 集群态势 */}
      <Row tone="indigo" title={tr('集群态势', 'Cluster posture')} desc={tr('资源水位与监控覆盖', 'resource & coverage')}>
        <div className="grid grid-cols-2 gap-3 sm:grid-cols-4">
          {res?.available ? (
            <>
              <StatCard tone="indigo" label="CPU" value={round1(res.cpu_avg)} unit={tr('% 均', '% avg')} sub={tr(`峰 ${res.cpu_peak.toFixed(1)}%`, `peak ${res.cpu_peak.toFixed(1)}%`)} bar={res.cpu_peak} />
              <StatCard tone="indigo" label={tr('内存', 'Memory')} value={round1(res.mem_avg)} unit={tr('% 均', '% avg')} sub={tr(`峰 ${res.mem_peak.toFixed(1)}%`, `peak ${res.mem_peak.toFixed(1)}%`)} bar={res.mem_peak} />
              <StatCard tone="indigo" label={tr('磁盘', 'Disk')} value={round1(res.disk_avg)} unit={tr('% 均', '% avg')} sub={tr(`峰 ${res.disk_peak.toFixed(1)}%`, `peak ${res.disk_peak.toFixed(1)}%`)} bar={res.disk_peak} />
              <StatCard tone="indigo" label={tr('在线设备', 'Online')} value={fleet.online} sub={tr(`共 ${fleet.total} 台 · ${onlinePct}%`, `of ${fleet.total} · ${onlinePct}%`)} />
            </>
          ) : (
            <>
              <StatCard tone="indigo" label={tr('监控设备', 'Devices')} value={fleet.total} />
              <StatCard tone="indigo" label={tr('在线', 'Online')} value={fleet.online} sub={`${onlinePct}%`} />
              <div className="col-span-2 flex items-center rounded-lg border border-dashed border-zinc-800 px-3 text-xs text-zinc-600">
                {tr('本周期资源指标暂无数据（监控刚接入或超出保留期）', 'No resource metrics for this period (recently onboarded or past retention)')}
              </div>
            </>
          )}
        </div>
      </Row>

      {/* Narrative — the "中间说明" */}
      {content.narrative?.headline && (
        <section className="rounded-lg border border-zinc-800 bg-zinc-900/30 p-4">
          <h2 className="mb-1.5 text-base font-semibold text-zinc-100">{content.narrative.headline}</h2>
          {paras.length > 0 && (
            <div className="space-y-2 text-sm leading-relaxed text-zinc-300">
              {paras.map((p, i) => (
                <p key={i}><EntityText text={p.text} /></p>
              ))}
            </div>
          )}
        </section>
      )}

      {/* ROW 2 — 告警与处理 */}
      <Row tone="rose" title={tr('告警与处理', 'Alerts & response')} desc={tr('事件、处置与 agent 动作', 'incidents & agent actions')}>
        <div className="grid grid-cols-2 gap-3 sm:grid-cols-4">
          <StatCard tone="rose" label={tr('告警', 'Incidents')} value={incidents.length} />
          <StatCard tone="rose" label={tr('已解决', 'Resolved')} value={resolved} />
          <StatCard tone="rose" label="MTTR" value={mttr} unit="min" />
          <StatCard tone="rose" label={tr('Agent 动作', 'Agent actions')} value={a.mutating_total + a.safe_total} sub={tr(`变更 ${a.mutating_total} · 只读 ${a.safe_total}`, `mut ${a.mutating_total} · ro ${a.safe_total}`)} />
        </div>
        {incidents.length > 0 && (
          <div className="mt-2 space-y-1.5">
            {incidents.map((ki) => <IncidentRow key={ki.id} ki={ki} />)}
          </div>
        )}
      </Row>

      {/* ROW 3 — 知识资产新增 */}
      <Row tone="violet" title={tr('知识资产新增', 'New assets')} desc={tr('本周期新建的助理 / 技能 / 仓库', 'assistants / skills / repos added')}>
        <div className="grid grid-cols-3 gap-3">
          <StatCard tone="violet" label={tr('新增助理', 'Assistants')} value={assets.new_agents} />
          <StatCard tone="violet" label={tr('新增技能', 'Skills')} value={assets.new_skills} />
          <StatCard tone="violet" label={tr('新增仓库', 'Repos')} value={assets.new_repos} />
        </div>
      </Row>

      {/* ROW 4 — 使用情况 */}
      <Row tone="cyan" title={tr('使用情况', 'Usage')} desc={tr('会话与 LLM token 消耗', 'sessions & LLM tokens')}>
        <div className="grid grid-cols-3 gap-3">
          <StatCard tone="cyan" label={tr('会话数', 'Sessions')} value={usage.sessions} />
          <StatCard tone="cyan" label={tr('输入 token', 'Prompt tokens')} value={usage.prompt_tokens} sub={fmtTokens(usage.prompt_tokens)} />
          <StatCard tone="cyan" label={tr('输出 token', 'Completion tokens')} value={usage.completion_tokens} sub={fmtTokens(usage.completion_tokens)} />
        </div>
      </Row>

      {/* Changes */}
      {changes.length > 0 && (
        <Row tone="cyan" title={tr('变更记录', 'Changes')}>
          <div className="space-y-1">{changes.map((ch, i) => <ChangeRow key={i} c={ch} />)}</div>
        </Row>
      )}

      {/* Advice */}
      {advice.length > 0 && (
        <section>
          <h3 className="mb-2 text-sm font-semibold text-zinc-300">{tr('建议', 'Recommendations')}</h3>
          <ul className="space-y-1.5 text-sm text-zinc-300">
            {advice.map((ad, i) => (
              <li key={i} className="flex gap-2">
                <span className="text-indigo-400">•</span>
                <span><EntityText text={ad.text} /></span>
              </li>
            ))}
          </ul>
        </section>
      )}
    </div>
  );
}

function round1(f: number): number {
  return Math.round(f * 10) / 10;
}
