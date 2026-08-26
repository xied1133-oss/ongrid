import { useMemo } from 'react';
import { cn } from '@/lib/cn';

type Variant = 'cpu-mem-pct' | 'plain';

type Props = {
  data: ReadonlyArray<number | null | undefined>;
  width?: number;
  height?: number;
  /**
   * If 'cpu-mem-pct', values are interpreted as percentages (0–100):
   * any value > 80 turns the line red, otherwise an upward-trending series
   * (last quarter avg significantly higher than first quarter avg) is amber.
   * 'plain' always uses the neutral zinc stroke.
   */
  variant?: Variant;
  className?: string;
  ariaLabel?: string;
};

/**
 * Tiny axis-less line chart for KPI cards and table rows.
 * Renders pure SVG (no recharts overhead) so it stays cheap even with 50+
 * instances on a single page.
 */
export function Sparkline({
  data,
  width = 60,
  height = 24,
  variant = 'plain',
  className,
  ariaLabel,
}: Props) {
  const cleaned = useMemo<number[]>(
    () =>
      data
        .map((v) => (typeof v === 'number' && Number.isFinite(v) ? v : null))
        .filter((v): v is number => v !== null),
    [data],
  );

  const stroke = useMemo(() => {
    if (variant !== 'cpu-mem-pct' || cleaned.length === 0) {
      return 'stroke-zinc-400';
    }
    const max = Math.max(...cleaned);
    if (max > 80) return 'stroke-red-400';
    if (cleaned.length >= 4) {
      const q = Math.max(1, Math.floor(cleaned.length / 4));
      const head = cleaned.slice(0, q);
      const tail = cleaned.slice(-q);
      const avg = (xs: number[]) => xs.reduce((a, b) => a + b, 0) / xs.length;
      const headAvg = avg(head);
      const tailAvg = avg(tail);
      if (headAvg > 0 && tailAvg - headAvg > 15) return 'stroke-amber-400';
    }
    return 'stroke-zinc-400';
  }, [cleaned, variant]);

  if (cleaned.length < 2) {
    return (
      <svg
        width={width}
        height={height}
        viewBox={`0 0 ${width} ${height}`}
        className={cn('overflow-visible', className)}
        aria-label={ariaLabel}
        role={ariaLabel ? 'img' : undefined}
      >
        <line
          x1={0}
          x2={width}
          y1={height / 2}
          y2={height / 2}
          className="stroke-zinc-700"
          strokeWidth={1}
          strokeDasharray="2 3"
        />
      </svg>
    );
  }

  const min = Math.min(...cleaned);
  const max = Math.max(...cleaned);
  const range = max - min || 1;
  const stepX = width / (cleaned.length - 1);
  const padY = 2;
  const innerH = height - padY * 2;

  const points = cleaned
    .map((v, i) => {
      const x = i * stepX;
      const y = padY + innerH - ((v - min) / range) * innerH;
      return `${x.toFixed(2)},${y.toFixed(2)}`;
    })
    .join(' ');

  return (
    <svg
      width={width}
      height={height}
      viewBox={`0 0 ${width} ${height}`}
      className={cn('overflow-visible', className)}
      aria-label={ariaLabel}
      role={ariaLabel ? 'img' : undefined}
    >
      <polyline
        points={points}
        fill="none"
        strokeWidth={1.4}
        strokeLinecap="round"
        strokeLinejoin="round"
        className={stroke}
      />
    </svg>
  );
}
