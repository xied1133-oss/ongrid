// Grafana template-variable substitution for the native PromQLPanel
// renderer (Monitor.tsx). Grafana itself supports a rich variable
// system (datasource queries, regex captures, multi-value with custom
// formatting, ...). We don't try to match that. Instead we substitute
// a small well-known allowlist that covers every variable ongrid ships
// in its embedded dashboards (server-detail.json, cluster-overview.json):
//
//   $__rate_interval / $__interval — derived from the user-selected
//     range so PromQL `rate(metric[$__rate_interval])` works without
//     us needing to render a step picker in the panel chrome
//   $device_id — pulled from the URL query string (?device_id=7) when the
//     user navigated from EdgeDetail; empty string otherwise (which
//     PromQL's `label="$device_id"` treats as "match anything that
//     doesn't explicitly carry the label" — in practice means
//     match-all for our metrics that always carry device_id, but it's
//     not a true match-all; sufficient for v1 since the dashboards
//     can also be rendered with a real device_id from EdgeDetail).
//   ${VAR_NAME} — generic fallback, looks up VAR_NAME in URL query;
//     defaults to empty so syntactically-valid PromQL still parses
//
// Anything outside this set is left alone (Prom will reject and the
// panel shows the error inline). When operators ask for a specific
// variable we'll add it here — explicit is better than implicit.

// rateIntervalForRange picks a `[$__rate_interval]` value sized to the
// user's selected range. Grafana's own rule is "max(1m, 4 * step)" —
// we follow the same shape with bucket-based defaults so the panels
// render correctly without us emulating Grafana's intervalCalculator.
export function rateIntervalForRange(range: string): string {
  const ms = rangeToMs(range);
  if (ms <= 15 * 60_000) return '1m';
  if (ms <= 6 * 3600_000) return '5m';
  if (ms <= 24 * 3600_000) return '5m';
  if (ms <= 7 * 86400_000) return '1h';
  return '6h';
}

// stepForRange picks the query_range `step` so we get ~360 buckets
// across the range (matches recharts' practical sweet spot — 720+
// buckets visibly lag on dense panels). Returned as a Go duration
// string the backend's time.ParseDuration accepts.
export function stepForRange(range: string): string {
  const ms = rangeToMs(range);
  // bucket = max(15s, ms / 360)
  const bucketMs = Math.max(15_000, Math.floor(ms / 360));
  return msToDurationString(bucketMs);
}

function msToDurationString(ms: number): string {
  if (ms >= 3600_000 && ms % 3600_000 === 0) return `${ms / 3600_000}h`;
  if (ms >= 60_000 && ms % 60_000 === 0) return `${ms / 60_000}m`;
  return `${Math.max(1, Math.round(ms / 1000))}s`;
}

function rangeToMs(range: string): number {
  const m = /^(\d+)([smhdw])$/.exec(range.trim());
  if (!m) return 3600_000;
  const n = parseInt(m[1], 10);
  const mult: Record<string, number> = {
    s: 1000,
    m: 60_000,
    h: 3600_000,
    d: 86400_000,
    w: 604800_000,
  };
  return n * (mult[m[2]] ?? 3600_000);
}

// VarContext is the minimal lookup the substitutor needs. Tests pass a
// plain object; the runtime path constructs it from URLSearchParams +
// the active range.
export type VarContext = {
  range: string;
  query: URLSearchParams; // typically location.search
};

// substitute replaces `$varname` and `${varname}` tokens in `expr`. The
// match is greedy on `[A-Za-z_][A-Za-z0-9_]*` — Grafana's actual
// variable syntax also supports `[[varname]]` (legacy) and pipe-format
// (`${var:csv}`); we don't see those in our shipped dashboards, so we
// leave them un-substituted (Prom will surface the syntax error).
export function substitute(expr: string, ctx: VarContext): string {
  if (!expr) return expr;

  // Resolve known special vars first so the regex pass below can fall
  // back to URL params for everything else.
  const special: Record<string, string> = {
    __rate_interval: rateIntervalForRange(ctx.range),
    __interval: rateIntervalForRange(ctx.range),
    __range: ctx.range,
  };

  // ${name} form first so it doesn't get eaten by the bare-$ pass.
  let out = expr.replace(/\$\{([A-Za-z_][A-Za-z0-9_]*)(?::[^}]*)?\}/g, (_, name: string) => {
    return resolveVar(name, special, ctx);
  });
  // Bare `$name` form. We DON'T substitute pure-numeric tokens like
  // `$1` (those are Prom-side anchors in some legacy expressions) —
  // the [A-Za-z_] anchor handles that automatically.
  out = out.replace(/\$([A-Za-z_][A-Za-z0-9_]*)/g, (_, name: string) => {
    return resolveVar(name, special, ctx);
  });
  return out;
}

function resolveVar(name: string, special: Record<string, string>, ctx: VarContext): string {
  if (Object.prototype.hasOwnProperty.call(special, name)) {
    return special[name];
  }
  // URL query string is the v1 hand-off path for $device_id and friends.
  // EdgeDetail's drilldown links carry ?device_id=7 so the panel renders
  // a single edge's metrics; on a generic Monitor visit the param is
  // absent and we substitute "" (PromQL `label=""` selector means
  // "label not set" — for ongrid metrics that always carry device_id
  // this matches no series, so the panel reads empty rather than
  // accidentally aggregating across all edges). Add explicit URL
  // hand-off for device_id when this becomes annoying.
  const v = ctx.query.get(name);
  return v ?? '';
}

// flattenPanels walks the dashboard's panels[] depth-first, splicing
// row panels' nested children into the top-level list. Grafana's row
// type is purely organizational — a header strip with collapsibles —
// and we render every panel inline for v1 (no row UI). Non-renderable
// chrome (rows themselves) is filtered out of the result.
export function flattenPanels<T extends { type: string; panels?: T[] }>(panels: T[]): T[] {
  const out: T[] = [];
  for (const p of panels) {
    if (p.type === 'row') {
      if (Array.isArray(p.panels)) {
        out.push(...flattenPanels(p.panels));
      }
      continue;
    }
    out.push(p);
  }
  return out;
}
