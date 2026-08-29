// BrandLogo — DeepWay 品牌标识的内联 SVG：四个 45° 菱形块（左菱、长斜
// 杠、右菱、下菱）。几何轮廓与 public/ongrid-logo.svg / public/favicon.svg
// 同源（viewBox 裁掉空白边，让图形撑满图标槽位）。
//
// 填色走 currentColor：light 用纯品牌蓝 #00339D；dark 底（zinc-900/950）
// 上纯品牌蓝对比不足，提亮到 #3B76F0（与 --accent 暗色档同源），保证
// 品牌块在两种主题下都跳得出来。

type Props = {
  /** Pixel size (square). Default 28 — sidebar / header scale. */
  size?: number;
  className?: string;
  /** title for screen readers (default "DeepWay"). */
  title?: string;
};

const TONE = 'text-[#00339D] dark:text-[#3B76F0]';

export function BrandLogo({ size = 28, className, title = 'DeepWay' }: Props) {
  return (
    <svg
      width={size}
      height={size}
      viewBox="18 105 924 612"
      fill="none"
      xmlns="http://www.w3.org/2000/svg"
      className={className ? `${TONE} ${className}` : TONE}
      role="img"
      aria-label={title}
    >
      <title>{title}</title>
      {/* left diamond */}
      <path d="M306 105 L444 243 L153 537 L18 396 Z" fill="currentColor" />
      {/* long diagonal bar */}
      <path d="M639 108 L777 243 L321 705 L183 564 Z" fill="currentColor" />
      {/* right diamond */}
      <path d="M807 273 L942 408 L804 549 L669 411 Z" fill="currentColor" />
      {/* bottom diamond */}
      <path d="M639 441 L777 579 L639 717 L501 579 Z" fill="currentColor" />
    </svg>
  );
}
