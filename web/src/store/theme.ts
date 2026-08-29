// theme.ts — user-pickable accent color, persisted across reloads.
//
// We override the CSS custom property `--accent` on :root so every place
// that uses the `accent` Tailwind utility (bg-accent / text-accent /
// border-accent / ring-accent) follows along without a single component
// re-render. Same trick we already use for the base accent in
// src/styles/index.css — the picker just writes a different RGB triplet.
//
// Presets lead with the DeepWay brand blue (default); the rest are
// hand-picked alternates kept for users who want a different highlight:
//   - 品牌蓝  #3B76F0  ← DeepWay brand blue, lightened for dark UI (default)
//   - 玫粉    #F15BC7
//   - 海蓝    #5269F4
//   - 青色    #30A6D0
//   - 蓝绿    #57D6D8
//   - 翡翠    #10b981  ← classic green, high-contrast non-blue option
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
  { id: 'brand-blue', zh: '品牌蓝', en: 'Brand blue',    rgb: '59 118 240',  hex: '#3B76F0' },
  { id: 'rose',       zh: '玫粉',   en: 'Rose',          rgb: '241 91 199',  hex: '#F15BC7' },
  { id: 'royal-blue', zh: '海蓝',   en: 'Royal blue',    rgb: '82 105 244',  hex: '#5269F4' },
  { id: 'cyan',       zh: '青色',   en: 'Cyan',          rgb: '48 166 208',  hex: '#30A6D0' },
  { id: 'teal',       zh: '蓝绿',   en: 'Teal',          rgb: '87 214 216',  hex: '#57D6D8' },
  { id: 'emerald',    zh: '翡翠',   en: 'Emerald',       rgb: '16 185 129',  hex: '#10b981' },
];

export const ACCENT_PRESETS: AccentPreset[] = ACCENT_DEFS.map((d) => {
  const obj = { id: d.id, label: '', rgb: d.rgb, hex: d.hex } as AccentPreset;
  Object.defineProperty(obj, 'label', { get: () => trInline(d.zh, d.en), enumerable: true });
  return obj;
});

const DEFAULT_PRESET_ID = 'brand-blue';

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
