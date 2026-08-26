import { cn } from '@/lib/cn';

type Props = {
  name?: string | null;
  email?: string | null;
  size?: number;
  className?: string;
};

function initialsOf(s: string): string {
  const trimmed = s.trim();
  if (!trimmed) return '?';
  const parts = trimmed.split(/[\s@._-]+/).filter(Boolean);
  if (parts.length === 0) return trimmed[0]!.toUpperCase();
  if (parts.length === 1) return parts[0]!.slice(0, 2).toUpperCase();
  return (parts[0]![0]! + parts[1]![0]!).toUpperCase();
}

export function Avatar({ name, email, size = 32, className }: Props) {
  const seed = name ?? email ?? '';
  const initials = initialsOf(seed);
  return (
    <div
      className={cn(
        // Mode-aware via dark: variants so the avatar mirrors the
        // surrounding theme: light bg + dark initials in light mode,
        // dark gradient + light initials in dark mode. Pinned via
        // dark: prefixes (not via the zinc → semantic remap) so neither
        // bg nor text are affected by the broad light-mode zinc
        // overrides defined in styles/index.css.
        'inline-flex items-center justify-center rounded-full font-medium select-none',
        'bg-gradient-to-br from-slate-200 to-slate-300 text-slate-700 ring-1 ring-slate-300/60',
        'dark:from-zinc-700 dark:to-zinc-800 dark:text-zinc-100 dark:ring-zinc-700/50',
        className
      )}
      style={{ width: size, height: size, fontSize: Math.max(11, Math.floor(size * 0.4)) }}
      aria-hidden
    >
      {initials}
    </div>
  );
}
