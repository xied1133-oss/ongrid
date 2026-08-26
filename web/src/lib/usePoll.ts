import { useEffect, useRef } from 'react';

// usePoll calls `fn` every `intervalMs` while the page tab is visible.
// Pauses (skips the interval body) when document.visibilityState
// becomes 'hidden' and resumes — including a one-shot immediate
// invocation so the UI catches up when the user returns instead of
// showing stale state until the next natural tick.
//
// Existing polling pages set up raw setInterval and (with the sole
// exception of ChatThread) kept hitting the backend forever when the
// tab was backgrounded. Switching them to usePoll eliminates that
// silent cost without changing visible behaviour for the active tab.
//
// Pass `enabled=false` to disable the loop entirely (e.g. when the
// user toggles a "live" switch off, or when the page hasn't loaded
// dependencies yet). Changing `enabled` or `intervalMs` re-arms.
//
// Callers MUST keep `fn` stable (useCallback) or expect re-arming on
// every render.
export function usePoll(fn: () => void | Promise<void>, intervalMs: number, enabled = true) {
  const fnRef = useRef(fn);
  useEffect(() => {
    fnRef.current = fn;
  }, [fn]);

  useEffect(() => {
    if (!enabled || intervalMs <= 0) return;
    let cancelled = false;
    const tick = () => {
      if (cancelled) return;
      if (typeof document !== 'undefined' && document.visibilityState !== 'visible') return;
      void fnRef.current();
    };
    const id = window.setInterval(tick, intervalMs);
    const onVisible = () => {
      if (document.visibilityState === 'visible') void fnRef.current();
    };
    document.addEventListener('visibilitychange', onVisible);
    return () => {
      cancelled = true;
      window.clearInterval(id);
      document.removeEventListener('visibilitychange', onVisible);
    };
  }, [enabled, intervalMs]);
}
