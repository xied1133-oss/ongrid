import { AlertTriangle } from 'lucide-react';
import type { CapabilityDeclaration, LoadWarning } from '@/api/marketplace';
import { cn } from '@/lib/cn';

// CapabilitySummaryView is the shared bullet-list rendering used by both
// the install-confirm modal and the row "[详情]" expander on the installed
// list. Field priority — what the user reads first — is documented in
// the report:
//   1) skill / agent counts (scope of the install)
//   2) tool classes (read / write / destructive — risk class)
//   3) binaries (host-side dependency footprint)
//   4) config keys (operator surface)
// destructive triggers a dedicated warning band (see 二审 in the
// agent runtime story).

export function CapabilitySummaryView({
  decl,
  warnings,
  compact,
}: {
  decl: CapabilityDeclaration;
  warnings?: LoadWarning[];
  compact?: boolean;
}) {
  const skills = decl.skills?.length ?? 0;
  const agents = decl.agent_count ?? 0;
  const toolClasses = decl.summary?.tool_classes ?? [];
  const bins = decl.summary?.bins ?? [];
  const configKeys = decl.summary?.config_keys ?? [];
  const hasDestructive = toolClasses.includes('destructive');

  return (
    <div className={cn('space-y-3 text-xs text-zinc-300', compact && 'space-y-2')}>
      <div className="flex flex-wrap gap-x-4 gap-y-1">
        <SummaryStat label="skill" n={skills} />
        <SummaryStat label="agent" n={agents} />
      </div>

      {toolClasses.length > 0 && (
        <Row label="工具类">
          <span className="flex flex-wrap gap-1.5">
            {toolClasses.map((c) => (
              <ClassPill key={c} kind={c} />
            ))}
          </span>
        </Row>
      )}

      {bins.length > 0 && (
        <Row label="binaries">
          <span className="font-mono text-zinc-200">{bins.join(', ')}</span>
        </Row>
      )}

      {configKeys.length > 0 && (
        <Row label="config keys">
          <span className="font-mono text-zinc-200">{configKeys.join(', ')}</span>
        </Row>
      )}

      {hasDestructive && (
        <div className="flex items-start gap-2 rounded-md border border-amber-500/30 bg-amber-500/10 px-2.5 py-2 text-[11px] text-amber-200">
          <AlertTriangle size={12} className="mt-0.5 shrink-0" />
          <span>包含 destructive 类工具 — 调用时会经过 reviewer 二审</span>
        </div>
      )}

      {warnings && warnings.length > 0 && (
        <div className="rounded-md border border-zinc-800 bg-zinc-950/40 px-2.5 py-2">
          <div className="mb-1 text-[11px] font-medium text-zinc-400">
            Warnings ({warnings.length})
          </div>
          <ul className="space-y-0.5 text-[11px] text-zinc-400">
            {warnings.map((w, i) => (
              <li key={i}>
                <span className="font-mono text-zinc-300">{w.path}</span>: {w.reason}
                <span className="ml-1 text-zinc-600">({w.code})</span>
              </li>
            ))}
          </ul>
        </div>
      )}
    </div>
  );
}

function Row({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div className="flex items-baseline gap-2">
      <span className="shrink-0 text-[11px] uppercase tracking-wide text-zinc-500">
        {label}
      </span>
      <span className="min-w-0 flex-1 break-words">{children}</span>
    </div>
  );
}

function SummaryStat({ label, n }: { label: string; n: number }) {
  return (
    <span className="text-zinc-400">
      <span className="font-mono text-zinc-100">{n}</span> 个 {label}
    </span>
  );
}

function ClassPill({ kind }: { kind: string }) {
  const tone =
    kind === 'destructive'
      ? 'border-red-500/40 bg-red-500/10 text-red-300'
      : kind === 'write'
        ? 'border-amber-500/30 bg-amber-500/10 text-amber-300'
        : 'border-zinc-700 bg-zinc-800/60 text-zinc-300';
  return (
    <span
      className={cn(
        'inline-flex items-center rounded-md border px-1.5 py-0.5 text-[11px]',
        tone,
      )}
    >
      {kind}
    </span>
  );
}
