import type { HTMLAttributes } from 'react';
import { cn } from '@/lib/cn';

// Chip — small pill for inline metadata (tags, role labels, count chips).
// Default tone uses zinc-800/60 + zinc-400 text per the visual spec; add
// a `tone` for semantic colour (success / warning / danger / info).
type ChipTone = 'default' | 'success' | 'warning' | 'danger' | 'info' | 'accent';

type ChipProps = HTMLAttributes<HTMLSpanElement> & {
  tone?: ChipTone;
  /** Tightens spacing slightly for very dense rows. */
  dense?: boolean;
};

const TONE_CLASS: Record<ChipTone, string> = {
  default: 'bg-zinc-800/60 text-zinc-400',
  success: 'bg-emerald-500/10 text-emerald-300',
  warning: 'bg-amber-500/10 text-amber-300',
  danger: 'bg-red-500/10 text-red-300',
  info: 'bg-sky-500/10 text-sky-300',
  accent: 'bg-indigo-500/10 text-indigo-300',
};

export function Chip({ tone = 'default', dense, className, ...rest }: ChipProps) {
  return (
    <span
      className={cn(
        'inline-flex items-center gap-1 rounded text-[10px]',
        dense ? 'px-1 py-0' : 'px-1.5 py-0.5',
        TONE_CLASS[tone],
        className,
      )}
      {...rest}
    />
  );
}
