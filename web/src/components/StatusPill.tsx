import { cn } from '@/lib/cn';
import { tr } from '@/i18n/locale';

type Props = {
  status: 'online' | 'offline' | 'unknown' | string;
  className?: string;
};

export function StatusPill({ status, className }: Props) {
  const isOnline = status === 'online';
  const isOffline = status === 'offline';
  const label = isOnline
    ? tr('在线', 'Online')
    : isOffline
      ? tr('离线', 'Offline')
      : tr('未知', 'Unknown');
  return (
    <span
      className={cn(
        'inline-flex items-center gap-1.5 rounded-full px-2 py-0.5 text-xs font-medium ring-1 ring-inset',
        isOnline && 'bg-emerald-500/10 text-emerald-400 ring-emerald-500/30',
        isOffline && 'bg-zinc-800 text-zinc-400 ring-zinc-700',
        !isOnline && !isOffline && 'bg-amber-500/10 text-amber-400 ring-amber-500/30',
        className
      )}
    >
      <span
        className={cn(
          'h-1.5 w-1.5 rounded-full',
          isOnline ? 'bg-emerald-400' : isOffline ? 'bg-zinc-500' : 'bg-amber-400',
          isOnline && 'animate-pulse'
        )}
      />
      {label}
    </span>
  );
}
