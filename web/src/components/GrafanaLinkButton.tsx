import { ExternalLink } from 'lucide-react';
import { cn } from '@/lib/cn';

// GrafanaLinkButton renders the universal "open in Grafana" header
// button used by Monitor / Logs / Traces. Same shape, same icon, same
// label across pages so users don't have to learn three styles for
// what's effectively the same action ("show me the deep-analysis view
// of whatever I'm currently looking at").
//
// Each page wires its own onClick (build the right deep-link, mint the
// promTicket cookie, popup handling) — only the visual chrome lives
// here.
export function GrafanaLinkButton({
  onClick,
  title,
  label = '在 Grafana 中打开',
  disabled,
  className,
}: {
  onClick: () => void;
  title?: string;
  label?: string;
  disabled?: boolean;
  className?: string;
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      disabled={disabled}
      title={title}
      className={cn(
        'inline-flex items-center gap-1.5 rounded-lg border border-zinc-700 px-3 py-1.5 text-xs text-zinc-200 hover:border-zinc-500 hover:bg-zinc-800 disabled:opacity-50',
        className,
      )}
    >
      <ExternalLink size={12} />
      <span>{label}</span>
    </button>
  );
}
