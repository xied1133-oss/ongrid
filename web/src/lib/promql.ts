// PromQL string-level helpers shared between Monitor and any other page
// that needs to splice device-scoped matchers into operator-authored or
// user-authored queries.
//
// We deliberately don't pull a real PromQL parser — the queries we touch
// are short, the surface is "splice into every {…}", and a 50-line
// regex utility ships in 1 KB instead of 80 KB.

// injectDeviceIDFilter splices `device_id=~"a|b|c"` into every {…}
// selector in the expression. PromQL accepts comma-joined matchers, so
// we just append.
//
// Empty deviceIDs short-circuits with a sentinel that yields no series,
// so panels render an honest "no data" instead of unfiltered fleet
// metrics. Don't pass `null` here — pass it only when the caller has
// decided a filter is active.
export function injectDeviceIDFilter(expr: string, deviceIDs: number[]): string {
  if (deviceIDs.length === 0) {
    return expr.replace(/\{([^}]*)\}/g, (_m, inner: string) => {
      const trimmed = inner.trim();
      const sentinel = 'device_id=~"__none__"';
      return trimmed ? `{${trimmed},${sentinel}}` : `{${sentinel}}`;
    });
  }
  const filter = `device_id=~"${deviceIDs.join('|')}"`;
  return expr.replace(/\{([^}]*)\}/g, (_m, inner: string) => {
    const trimmed = inner.trim();
    return trimmed ? `{${trimmed},${filter}}` : `{${filter}}`;
  });
}

// referencesDeviceID returns true when an expression already mentions
// the `device_id` label — either as a matcher (`device_id="..."`,
// `device_id=~"..."`) or in a grouping clause (`by (device_id)`).
//
// User-authored panels that don't reference device_id at all are likely
// service- or fleet-aggregate views; injecting a device matcher would
// silently change their meaning, so callers should skip them and surface
// a "全集群" badge instead.
export function referencesDeviceID(expr: string): boolean {
  return /\bdevice_id\b/.test(expr);
}
