import type { ReactNode } from 'react';
import type { IconType } from '@/lib/icon';

// EmptyState — vertical centered icon + message + optional CTA. Used in
// every list page that can be empty (Knowledge / Agents / AlertRules /
// Logs / IncidentDetail).
type Props = {
  icon?: IconType;
  title: string;
  hint?: string;
  /** Renders below the title; pass a button or link styled consistently
   *  with the rest of the page (typically the accent button). */
  action?: ReactNode;
  /** Override the default min-height (h-60) for cramped contexts. */
  className?: string;
};

export function EmptyState({ icon: Icon, title, hint, action, className }: Props) {
  return (
    <div
      className={
        className ??
        'flex h-60 flex-col items-center justify-center gap-2 text-center'
      }
    >
      {Icon && <Icon size={28} className="text-zinc-600" />}
      <div className="text-sm text-zinc-500">{title}</div>
      {hint && <div className="text-xs text-zinc-600">{hint}</div>}
      {action && <div className="mt-3">{action}</div>}
    </div>
  );
}
