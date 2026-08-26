import { cn } from '@/lib/cn';
import type { IconType } from '@/lib/icon';

type Props = {
  title: string;
  description: string;
  icon?: IconType;
  onClick(): void;
  className?: string;
};

export function PromptCard({ title, description, icon: Icon, onClick, className }: Props) {
  return (
    <button
      type="button"
      onClick={onClick}
      aria-label={title}
      className={cn(
        'group flex flex-col items-start gap-1.5 rounded-xl border border-zinc-800/60 bg-zinc-900/40 p-3.5 text-left transition-colors',
        'hover:border-zinc-700 hover:bg-zinc-900/60',
        className
      )}
    >
      <div className="flex w-full items-center gap-2">
        {Icon && (
          <Icon size={14} className="text-zinc-400 transition-colors group-hover:text-zinc-200" />
        )}
        <span className="text-sm font-semibold text-zinc-100">{title}</span>
      </div>
      <span className="text-xs leading-relaxed text-zinc-400">{description}</span>
    </button>
  );
}
