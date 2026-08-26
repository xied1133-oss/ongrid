// OngridLogo — inline SVG so we get sharpness at any size + can theme on
// dark/light without an extra HTTP fetch. Source of truth is the same
// gradient pillars shipped at public/ongrid-logo.svg (used for favicon).
//
// Color stops match the design:
//   left pillar: #F15BC7 → #8C6DF0 → #5269F4
//   right pillar: #1456B5 → #30A6D0 → #57D6D8
//   back rails:  #7182AD (cool slate; toned down to 0.6 alpha on dark UI
//                so the foreground pillars carry the brand energy)

type Props = {
  /** Pixel size (square). Default 28 — sidebar / header scale. */
  size?: number;
  className?: string;
  /** title for screen readers (default "ongrid"). */
  title?: string;
};

export function OngridLogo({ size = 28, className, title = 'Ongrid' }: Props) {
  return (
    <svg
      width={size}
      height={size}
      viewBox="0 0 512 512"
      fill="none"
      xmlns="http://www.w3.org/2000/svg"
      className={className}
      role="img"
      aria-label={title}
    >
      <title>{title}</title>
      <defs>
        <linearGradient id="ongridLeftPillar" x1="154" y1="88" x2="225" y2="432" gradientUnits="userSpaceOnUse">
          <stop offset="0" stopColor="#F15BC7" />
          <stop offset="0.52" stopColor="#8C6DF0" />
          <stop offset="1" stopColor="#5269F4" />
        </linearGradient>
        <linearGradient id="ongridRightPillar" x1="291" y1="69" x2="362" y2="413" gradientUnits="userSpaceOnUse">
          <stop offset="0" stopColor="#1456B5" />
          <stop offset="0.56" stopColor="#30A6D0" />
          <stop offset="1" stopColor="#57D6D8" />
        </linearGradient>
      </defs>
      <rect x="54" y="178" width="354" height="74" rx="37" transform="rotate(-14 54 178)" fill="#7182AD" fillOpacity="0.65" />
      <rect x="102" y="307" width="354" height="74" rx="37" transform="rotate(-14 102 307)" fill="#7182AD" fillOpacity="0.65" />
      <rect x="115" y="93" width="76" height="346" rx="38" transform="rotate(-14 115 93)" fill="url(#ongridLeftPillar)" />
      <rect x="252" y="74" width="76" height="346" rx="38" transform="rotate(-14 252 74)" fill="url(#ongridRightPillar)" />
    </svg>
  );
}
