// theme.ts — user-pickable accent color, persisted across reloads.
//
// We override the CSS custom property `--accent` on :root so every place
// that uses the `accent` Tailwind utility (bg-accent / text-accent /
// border-accent / ring-accent) follows along without a single component
// re-render. Same trick we already use for the base accent in
// src/styles/index.css — the picker just writes a different RGB triplet.
//
// Presets are hand-picked from the logo's two pillar gradients so each
// option still feels on-brand:
//   - 品牌紫  #8C6DF0  ← left pillar middle (default)
//   - 玫粉    #F15BC7  ← left pillar top
//   - 海蓝    #5269F4  ← left pillar bottom
//   - 青色    #30A6D0  ← right pillar middle
//   - 蓝绿    #57D6D8  ← right pillar bottom
//   - 翡翠    #10b981  ← off-brand classic green, kept for users who
//                       want a high-contrast non-purple option
//
// Custom hex isn't supported yet — keeps the picker honest about which
// values land on a brand surface vs which would look out of place.

import { create } from 'zustand';
import { persist, createJSONStorage } from 'zustand/middleware';
import { tr as trInline } from '@/i18n/locale';

export type AccentPreset = {
  id: string;
  label: string;
  /** Bare RGB triplet (e.g. "140 109 240"); written into --accent. */
  rgb: string;
  /** Hex preview used in the picker swatch. */
  hex: string;
};

const ACCENT_DEFS: Array<{ id: string; zh: string; en: string; rgb: string; hex: string }> = [
  { id: 'brand-purple', zh: '品牌紫', en: 'Brand purple', rgb: '140 109 240', hex: '#8C6DF0' },
  { id: 'rose',         zh: '玫粉',   en: 'Rose',          rgb: '241 91 199',  hex: '#F15BC7' },
  { id: 'royal-blue',   zh: '海蓝',   en: 'Royal blue',    rgb: '82 105 244',  hex: '#5269F4' },
  { id: 'cyan',         zh: '青色',   en: 'Cyan',          rgb: '48 166 208',  hex: '#30A6D0' },
  { id: 'teal',         zh: '蓝绿',   en: 'Teal',          rgb: '87 214 216',  hex: '#57D6D8' },
  { id: 'emerald',      zh: '翡翠',   en: 'Emerald',       rgb: '16 185 129',  hex: '#10b981' },
];

export const ACCENT_PRESETS: AccentPreset[] = ACCENT_DEFS.map((d) => {
  const obj = { id: d.id, label: '', rgb: d.rgb, hex: d.hex } as AccentPreset;
  Object.defineProperty(obj, 'label', { get: () => trInline(d.zh, d.en), enumerable: true });
  return obj;
});

const DEFAULT_PRESET_ID = 'brand-purple';

type ThemeState = {
  accentId: string;
  setAccent(id: string): void;
};

export const useTheme = create<ThemeState>()(
  persist(
    (set) => ({
      accentId: DEFAULT_PRESET_ID,
      setAccent: (id) => {
        applyAccent(id);
        set({ accentId: id });
      },
    }),
    {
      name: 'ongrid.theme',
      storage: createJSONStorage(() => localStorage),
    },
  ),
);

// applyAccent writes the preset's RGB triplet into the CSS custom
// property the Tailwind theme reads from. Idempotent. No-op when the
// id doesn't match any preset (defends against stale localStorage from
// a future build that introduced new ids the user might still have).
export function applyAccent(id: string): void {
  if (typeof document === 'undefined') return;
  const preset = ACCENT_PRESETS.find((p) => p.id === id);
  if (!preset) return;
  document.documentElement.style.setProperty('--accent', preset.rgb);
}

// applyAccentOnBoot is called once from main.tsx so the persisted
// preference takes effect before first paint. Reads localStorage
// directly because the zustand store's persist plugin rehydrates
// asynchronously and we don't want a one-frame flash of the default.
export function applyAccentOnBoot(): void {
  try {
    const raw = localStorage.getItem('ongrid.theme');
    if (!raw) return;
    const parsed = JSON.parse(raw) as { state?: { accentId?: string } };
    if (parsed?.state?.accentId) applyAccent(parsed.state.accentId);
  } catch {
    // ignored — corrupt localStorage falls back to the CSS default.
  }
}
