import { expect, test } from '@playwright/test';
import { login } from './helpers';

const capture = {
  id: 42,
  created_by: 1,
  source: 'api',
  state: 'ready',
  edge_id: 1,
  device_id: 2,
  interface_name: 'eth0',
  canonical_filter: '',
  direction: 'both',
  format: 'pcap',
  promiscuous: false,
  duration_seconds: 10,
  max_bytes: 1_048_576,
  max_packets: 1_000,
  snaplen: 262_144,
  title: 'Small-screen capture',
  description: '',
  captured_bytes: 64,
  captured_packets: 1,
  raw_available: true,
  artifact_id: 'pcap-42',
  analysis: {
    summary: { packets_seen: 80, packets_returned: 80, bytes_seen: 5_120 },
    packets: Array.from({ length: 80 }, (_, index) => ({
      number: index + 1,
      source: '10.0.0.1',
      destination: '10.0.0.2',
      protocol: 'tcp',
      length: 64,
      info: `test packet ${index + 1}`,
      protocol_tree: [{ name: 'Ethernet II', fields: [{ name: 'eth.type', value: 'IPv4' }] }],
      hex: [{ offset: 0, data: '00112233445566778899aabbccddeeff' }],
    })),
  },
  created_at: '2026-08-20T00:00:00Z',
  updated_at: '2026-08-20T00:00:00Z',
};

test('short viewport can scroll from packet rows to the hex viewer', async ({ page }) => {
  await page.setViewportSize({ width: 1280, height: 640 });
  await page.addInitScript(() => {
    localStorage.setItem('ongrid-theme-preference', 'dark');
    localStorage.setItem('ongrid.auth', JSON.stringify({
      state: { token: 'e2e-token', refreshToken: null, email: 'e2e@example.com', role: 'admin' },
      version: 0,
    }));
  });
  await page.route('**/api/v1/**', async (route) => {
    const path = new URL(route.request().url()).pathname;
    if (path === '/api/v1/packet-captures/artifacts/pcap-42') {
      await route.fulfill({ json: capture });
      return;
    }
    await route.fulfill({ json: { items: [], total: 0 } });
  });

  await page.goto('/artifacts/packets/pcap-42');
  const analysis = page.getByTestId('packet-analysis-view');
  await expect(analysis).toBeVisible({ timeout: 20_000 });
  await expect(page.getByText('OFFSET')).not.toBeInViewport();

  const dimensions = await analysis.evaluate((element) => ({
    clientHeight: element.clientHeight,
    scrollHeight: element.scrollHeight,
  }));
  expect(dimensions.scrollHeight).toBeGreaterThan(dimensions.clientHeight);

  await analysis.evaluate((element) => element.scrollTo({ top: element.scrollHeight }));
  await expect(page.getByText('OFFSET')).toBeInViewport();
  await page.screenshot({ path: '/tmp/packet-viewer-mobile-dark.png', fullPage: false });

  await page.evaluate(() => {
    const root = document.documentElement;
    root.dataset.theme = 'light';
    root.classList.remove('dark', 'theme-dark');
    root.classList.add('light', 'theme-light');
    root.style.colorScheme = 'light';
  });
  await page.screenshot({ path: '/tmp/packet-viewer-mobile-light.png', fullPage: false });
});

test('live short viewport can reach the hex viewer', async ({ page }) => {
  const artifactID = process.env.E2E_LIVE_PACKET_ARTIFACT;
  test.skip(!artifactID, 'E2E_LIVE_PACKET_ARTIFACT is required');

  await page.setViewportSize({ width: 1280, height: 640 });
  await login(page);
  await page.goto(`/artifacts/packets/${artifactID}`);

  const analysis = page.getByTestId('packet-analysis-view');
  await expect(analysis).toBeVisible({ timeout: 20_000 });
  const dimensions = await analysis.evaluate((element) => ({
    clientHeight: element.clientHeight,
    scrollHeight: element.scrollHeight,
  }));
  expect(dimensions.scrollHeight).toBeGreaterThan(dimensions.clientHeight);

  await analysis.evaluate((element) => element.scrollTo({ top: element.scrollHeight }));
  await expect(page.getByText('OFFSET', { exact: true })).toBeInViewport();
  await expect(page.getByText('0000', { exact: true })).toBeInViewport();
  await page.screenshot({ path: '/tmp/packet-viewer-live-short.png', fullPage: false });
});
