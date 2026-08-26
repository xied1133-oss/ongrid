import { useMemo, useState } from 'react';
import ReactMarkdown from 'react-markdown';
import remarkGfm from 'remark-gfm';
import { Brain, ChevronDown } from 'lucide-react';
import { cn } from '@/lib/cn';
import { useI18n } from '@/i18n/locale';

// ThinkingMarkdown renders markdown while wrapping reasoning-model
// thinking tags (MiniMax <mm:think>, generic  tag) into
// collapsible muted blocks instead of stripping them — the reasoning
// stays available but doesn't drown the actual answer.
//
// An unterminated opening tag (assistant still streaming) swallows the
// rest of the content as thinking so partial output renders cleanly.

type Segment = { kind: 'think' | 'text'; content: string };

// Backreference across alternatives: <mm:think>...</mm:think> or
//  ... ; second alternative matches an opening tag with no
// closing pair (streaming tail).
const THINK_RE = /<(mm:think|think)>([\s\S]*?)<\/\1>|<(mm:think|think)>([\s\S]*)$/g;

function splitSegments(content: string): Segment[] {
  const segs: Segment[] = [];
  let last = 0;
  for (const m of content.matchAll(THINK_RE)) {
    const idx = m.index ?? 0;
    if (idx > last) segs.push({ kind: 'text', content: content.slice(last, idx) });
    segs.push({ kind: 'think', content: (m[2] ?? m[4] ?? '').trim() });
    last = idx + m[0].length;
  }
  if (last < content.length) segs.push({ kind: 'text', content: content.slice(last) });
  return segs;
}

function ThinkBlock({ content }: { content: string }) {
  const { tr } = useI18n();
  const [open, setOpen] = useState(false);
  if (!content) return null;
  return (
    <div className="my-2 overflow-hidden rounded-md border border-zinc-800 bg-zinc-950/40">
      <button
        type="button"
        onClick={() => setOpen((v) => !v)}
        className="flex w-full items-center gap-1.5 px-3 py-1.5 text-left text-[12px] text-zinc-500 hover:bg-zinc-900/40 hover:text-zinc-400"
      >
        <Brain size={12} />
        <span>{tr('思考过程', 'Reasoning')}</span>
        <ChevronDown size={12} className={cn('ml-auto transition-transform', open && 'rotate-180')} />
      </button>
      {open && (
        <div className="md-body border-t border-zinc-800/60 px-3 py-2 text-[13px] leading-relaxed text-zinc-400">
          <ReactMarkdown remarkPlugins={[remarkGfm]}>{content}</ReactMarkdown>
        </div>
      )}
    </div>
  );
}

export function ThinkingMarkdown({
  content,
  className,
}: {
  content: string | undefined;
  className?: string;
}) {
  const segments = useMemo(() => splitSegments(content ?? ''), [content]);
  return (
    <div className={className}>
      {segments.map((seg, i) =>
        seg.kind === 'think' ? (
          <ThinkBlock key={i} content={seg.content} />
        ) : (
          seg.content.trim() !== '' && (
            <ReactMarkdown key={i} remarkPlugins={[remarkGfm]}>
              {seg.content}
            </ReactMarkdown>
          )
        )
      )}
    </div>
  );
}
