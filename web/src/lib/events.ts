// Lightweight cross-component events for state that lives outside React's
// tree (e.g. Sidebar's presentRoles, which has no shared store). Use this
// when:
//   - Two components need to react to the same mutation,
//   - One is the mutator (e.g. a modal) and the other a long-lived
//     ambient surface (e.g. the Sidebar) that won't naturally re-mount,
//   - Setting up a full zustand store would be overkill for a single flag.
//
// Pattern: mutator calls notifyDevicesChanged() after the PATCH succeeds;
// every subscriber refetches its own copy of the data it cares about.
// No payload — the listener already knows what to fetch.

const DEVICES_CHANGED = 'ongrid:devices-changed';

export function notifyDevicesChanged(): void {
  if (typeof window === 'undefined') return;
  window.dispatchEvent(new Event(DEVICES_CHANGED));
}

export function onDevicesChanged(listener: () => void): () => void {
  if (typeof window === 'undefined') return () => {};
  window.addEventListener(DEVICES_CHANGED, listener);
  return () => window.removeEventListener(DEVICES_CHANGED, listener);
}
