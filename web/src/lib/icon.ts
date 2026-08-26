import type { ComponentType, SVGProps } from 'react';

/**
 * IconType — broad enough to accept lucide-react icons as well as plain SVG components.
 * lucide icons are ForwardRefExoticComponent<LucideProps & RefAttributes<SVGSVGElement>>,
 * so we accept any component that takes SVG-ish props.
 */
export type IconType = ComponentType<SVGProps<SVGSVGElement> & { size?: number | string }>;
