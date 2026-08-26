import type { HTMLAttributes } from 'react';
import { cn } from '@/lib/cn';

// Card — unified card surface used across pages (DocCard / AgentCard /
// IncidentCard / etc.). Visual rules per HLD-style guide:
//   rounded-xl border border-zinc-800/60 bg-zinc-900/40
//   default p-4, compact p-3.5
//   hover (when interactive) hover:border-zinc-700 hover:bg-zinc-900/60
type CardProps = HTMLAttributes<HTMLDivElement> & {
  /** When true, the card is clickable / hover affordances kick in. */
  interactive?: boolean;
  /** Tighter padding for dense rows (e.g. AgentSessionCard). */
  compact?: boolean;
  as?: 'div' | 'section' | 'article';
};

export function Card({
  className,
  interactive,
  compact,
  as: Tag = 'section',
  ...rest
}: CardProps) {
  return (
    <Tag
      className={cn(
        'rounded-xl border border-zinc-800/60 bg-zinc-900/40',
        compact ? 'p-3.5' : 'p-4',
        interactive && 'transition-colors hover:border-zinc-700 hover:bg-zinc-900/60',
        className,
      )}
      {...rest}
    />
  );
}
