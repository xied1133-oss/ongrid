// mode.ts — theme preference, 3 levels: system / light / dark.
// Adopts the liaison-cloud pattern (utils/theme.ts): localStorage +
// matchMedia resolve + `data-theme` attribute + theme-dark / theme-light
// classes on <html>. Keeps Tailwind's `dark:` utility working by also
// toggling the `dark` / `light` class — both worlds.
//
// API:
//   getThemePreference()            → 'system' | 'light' | 'dark'
//   setThemePreference(pref)        → persist + apply + dispatch event
//   resolveTheme(pref)              → 'light' | 'dark' (system → matchMedia)
//   useThemeMode()                  → reactive hook for components
//   applyThemeOnBoot()              → main.tsx call, sync before first paint

import { useCallback, useEffect, useState } from 'react';

export type ThemePreference = 'system' | 'light' | 'dark';
export type ResolvedTheme = 'light' | 'dark';

const KEY = 'ongrid-theme-preference';
const EVENT = 'ongrid-theme-change';

export function getThemePreference(): ThemePreference {
  if (typeof localStorage === 'undefined') return 'system';
  const v = localStorage.getItem(KEY);
  if (v === 'light' || v === 'dark' || v === 'system') return v;
  return 'system';
}

export function resolveTheme(pref: ThemePreference): ResolvedTheme {
  if (pref === 'light' || pref === 'dark') return pref;
  if (typeof window === 'undefined' || typeof window.matchMedia !== 'function') return 'dark';
  return window.matchMedia('(prefers-color-scheme: dark)').matches ? 'dark' : 'light';
}

export function applyResolvedTheme(theme: ResolvedTheme): void {
  if (typeof document === 'undefined') return;
  const root = document.documentElement;
  root.dataset.theme = theme;
  root.classList.toggle('theme-dark', theme === 'dark');
  root.classList.toggle('theme-light', theme === 'light');
  // Keep Tailwind's class-based dark mode working: dark: utilities
  // throughout the codebase key on the .dark class.
  root.classList.toggle('dark', theme === 'dark');
  root.classList.toggle('light', theme === 'light');
  root.style.colorScheme = theme;
}

export function setThemePreference(pref: ThemePreference): void {
  localStorage.setItem(KEY, pref);
  applyResolvedTheme(resolveTheme(pref));
  window.dispatchEvent(new CustomEvent<ThemePreference>(EVENT, { detail: pref }));
}

// applyThemeOnBoot reads the persisted preference + applies it before
// React mounts so we don't flash the wrong theme. Also wires a one-time
// matchMedia listener so `system` follows the OS toggle live.
export function applyThemeOnBoot(): void {
  if (typeof document === 'undefined') return;
  const pref = getThemePreference();
  applyResolvedTheme(resolveTheme(pref));
  if (typeof window !== 'undefined' && window.matchMedia) {
    const mq = window.matchMedia('(prefers-color-scheme: dark)');
    mq.addEventListener('change', () => {
      if (getThemePreference() === 'system') {
        applyResolvedTheme(resolveTheme('system'));
      }
    });
  }
}

export function useThemeMode() {
  const [pref, setPref] = useState<ThemePreference>(getThemePreference());
  useEffect(() => {
    const listener = (e: Event) => {
      const ce = e as CustomEvent<ThemePreference>;
      setPref(ce.detail || getThemePreference());
    };
    window.addEventListener(EVENT, listener as EventListener);
    return () => window.removeEventListener(EVENT, listener as EventListener);
  }, []);

  const setPreference = useCallback((p: ThemePreference) => setThemePreference(p), []);
  const cycle = useCallback(() => {
    // Cycle order: system → light → dark → system. Common pattern in
    // operator tools where 'system' means "I trust the OS".
    const order: ThemePreference[] = ['system', 'light', 'dark'];
    const i = order.indexOf(pref);
    setThemePreference(order[(i + 1) % order.length]);
  }, [pref]);

  return {
    preference: pref,
    resolved: resolveTheme(pref),
    setPreference,
    cycle,
  };
}
