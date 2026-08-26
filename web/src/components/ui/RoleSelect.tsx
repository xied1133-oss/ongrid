// RoleSelect — single dropdown reused across Monitor / Edges / Logs /
// Traces for "filter by device role". The contract is intentionally
// shaped around the EdgeRole enum + two synthetic options:
//   - ''        → all roles
//   - 'unknown' → devices whose roles array is empty (未分类)
// Callers translate '' to "no filter" and 'unknown' to their own bucket
// logic; no role string overlaps with EdgeRole values, so a string-typed
// value is safe.
import { EDGE_ROLES, EDGE_ROLE_LABELS, EDGE_ROLE_LABELS_EN } from '@/api/edges';
import { useI18n } from '@/i18n/locale';

export type RoleFilterValue = '' | 'unknown' | (typeof EDGE_ROLES)[number];

// `chip` (default): inline pill — caption + select on one line. Used in
//                   toolbars (Monitor) where horizontal density matters.
// `block`         : caption above, full-width select below. Used in
//                   form-column layouts (Logs query form) where the
//                   column itself supplies the spacing.
type Variant = 'chip' | 'block';

export function RoleSelect({
  value,
  onChange,
  className,
  showLabel = true,
  variant = 'chip',
  omitUnknown = false,
}: {
  value: RoleFilterValue;
  onChange(value: RoleFilterValue): void;
  className?: string;
  showLabel?: boolean;
  variant?: Variant;
  // Drop the 未分类 entry — appropriate for callers like Logs/Traces
  // whose downstream query path can't represent "no role" sensibly
  // (the data source itself is keyed by edge, not by role bitmap).
  omitUnknown?: boolean;
}) {
  const { tr } = useI18n();
  const BASE_OPTIONS: { value: RoleFilterValue; label: string }[] = [
    { value: '', label: tr('所有角色', 'All roles') },
    ...EDGE_ROLES.map((r) => ({ value: r as RoleFilterValue, label: tr(EDGE_ROLE_LABELS[r], EDGE_ROLE_LABELS_EN[r]) })),
  ];
  const UNKNOWN_OPTION = { value: 'unknown' as RoleFilterValue, label: tr('未分类', 'Uncategorized') };
  const options = omitUnknown ? BASE_OPTIONS : [...BASE_OPTIONS, UNKNOWN_OPTION];
  if (variant === 'block') {
    return (
      <label className={'block ' + (className ?? '')}>
        {showLabel && <span className="mb-1 block text-[11px] text-zinc-500">{tr('角色', 'Role')}</span>}
        <select
          value={value}
          onChange={(e) => onChange(e.target.value as RoleFilterValue)}
          className="w-full rounded-md border border-zinc-800 bg-zinc-950 px-2 py-1.5 text-xs text-zinc-100 focus:border-zinc-600 focus:outline-none"
        >
          {options.map((o) => (
            <option key={o.value} value={o.value} className="bg-zinc-900">
              {o.label}
            </option>
          ))}
        </select>
      </label>
    );
  }

  return (
    <label
      className={
        'inline-flex items-center gap-1 rounded-md border border-zinc-800/60 bg-zinc-950/40 pl-2 pr-1 py-1 text-zinc-300 hover:border-zinc-700 ' +
        (className ?? '')
      }
    >
      {showLabel && <span className="text-[11px] text-zinc-500">{tr('角色', 'Role')}</span>}
      <select
        value={value}
        onChange={(e) => onChange(e.target.value as RoleFilterValue)}
        className="appearance-none border-none bg-transparent pl-1 pr-4 text-[12px] text-zinc-100 focus:outline-none"
      >
        {options.map((o) => (
          <option key={o.value} value={o.value} className="bg-zinc-900">
            {o.label}
          </option>
        ))}
      </select>
    </label>
  );
}
