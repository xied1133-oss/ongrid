import { describe, expect, it } from 'vitest';
import { injectDeviceIDFilter, referencesDeviceID } from './promql';

describe('injectDeviceIDFilter', () => {
  it('appends matcher into a populated selector', () => {
    expect(
      injectDeviceIDFilter('rate(node_cpu_seconds_total{mode="idle"}[5m])', [1, 2]),
    ).toBe('rate(node_cpu_seconds_total{mode="idle",device_id=~"1|2"}[5m])');
  });

  it('fills an empty selector cleanly', () => {
    expect(injectDeviceIDFilter('node_load1{}', [7])).toBe('node_load1{device_id=~"7"}');
  });

  it('leaves bare metric names alone (caller must include {} as anchor)', () => {
    // Documented limitation: PromQL like `node_memory_MemAvailable_bytes`
    // (no `{}`) is unaffected by the filter. Built-in panels work
    // around this by always emitting `{}` even when empty; user-authored
    // PromQL gets the "全集群" badge in UserPanelGrid instead.
    expect(injectDeviceIDFilter('node_memory_MemAvailable_bytes', [3])).toBe(
      'node_memory_MemAvailable_bytes',
    );
  });

  it('rewrites every {} when the expression has multiple selectors', () => {
    expect(
      injectDeviceIDFilter(
        'sum by (device_id) (rate(node_network_receive_bytes_total{device!~"lo"}[5m])) + sum by (device_id) (rate(node_network_transmit_bytes_total{device!~"lo"}[5m]))',
        [3],
      ),
    ).toBe(
      'sum by (device_id) (rate(node_network_receive_bytes_total{device!~"lo",device_id=~"3"}[5m])) + sum by (device_id) (rate(node_network_transmit_bytes_total{device!~"lo",device_id=~"3"}[5m]))',
    );
  });

  it('uses a sentinel that yields no series when ids is empty', () => {
    // Empty deviceIDs is "filter is active but matched no devices" — the
    // panel should render "no data", not the unfiltered fleet.
    expect(injectDeviceIDFilter('node_load1{mode="idle"}', [])).toBe(
      'node_load1{mode="idle",device_id=~"__none__"}',
    );
    expect(injectDeviceIDFilter('node_load1{}', [])).toBe('node_load1{device_id=~"__none__"}');
  });

  it('joins multiple ids with pipe for regex matcher', () => {
    expect(injectDeviceIDFilter('up{}', [10, 20, 30])).toBe('up{device_id=~"10|20|30"}');
  });
});

describe('referencesDeviceID', () => {
  it('detects label matcher form', () => {
    expect(referencesDeviceID('rate(metric{device_id="1"}[1m])')).toBe(true);
    expect(referencesDeviceID('rate(metric{device_id=~"1|2"}[1m])')).toBe(true);
  });

  it('detects by-grouping form', () => {
    expect(referencesDeviceID('sum by (device_id) (rate(metric[1m]))')).toBe(true);
  });

  it('returns false when device_id is absent', () => {
    expect(referencesDeviceID('rate(node_load1[1m])')).toBe(false);
    expect(referencesDeviceID('sum by (instance) (rate(metric[1m]))')).toBe(false);
  });

  it('matches as a whole word — substrings of other labels do not count', () => {
    expect(referencesDeviceID('rate(metric{my_device_idx="1"}[1m])')).toBe(false);
  });
});
