import { ShieldCheck, ShieldAlert, ShieldX } from 'lucide-react';
import { cn } from '@/lib/cn';
import type { SignatureState } from '@/api/marketplace';

// SignatureBadge surfaces the trust state v1
// almost everything will be `unsigned` (we don't ship cosign yet), so
// the styling is deliberately calm — it's information, not a blocker.
export function SignatureBadge({
  state,
  className,
}: {
  state: SignatureState | string;
  className?: string;
}) {
  if (state === 'verified') {
    return (
      <span
        className={cn(
          'inline-flex items-center gap-1 rounded-md border border-emerald-500/30 bg-emerald-500/10 px-1.5 py-0.5 text-[11px] text-emerald-300',
          className,
        )}
      >
        <ShieldCheck size={11} />
        verified
      </span>
    );
  }
  if (state === 'failed') {
    return (
      <span
        className={cn(
          'inline-flex items-center gap-1 rounded-md border border-red-500/40 bg-red-500/10 px-1.5 py-0.5 text-[11px] text-red-300',
          className,
        )}
      >
        <ShieldX size={11} />
        signature failed
      </span>
    );
  }
  return (
    <span
      className={cn(
        'inline-flex items-center gap-1 rounded-md border border-amber-500/30 bg-amber-500/10 px-1.5 py-0.5 text-[11px] text-amber-300',
        className,
      )}
    >
      <ShieldAlert size={11} />
      unsigned
    </span>
  );
}
