import { render, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';

import type { PacketCapture } from '@/api/packetCaptures';
import { PacketArtifactViewer } from '@/pages/Pages';

const capture: PacketCapture = {
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
    summary: { packets_seen: 1, packets_returned: 1, bytes_seen: 64 },
    packets: [{
      number: 1,
      source: '10.0.0.1',
      destination: '10.0.0.2',
      protocol: 'tcp',
      length: 64,
      info: 'test packet',
      protocol_tree: [{ name: 'Ethernet II' }],
      hex: [{ offset: 0, data: '00112233445566778899aabbccddeeff' }],
    }],
  },
  created_at: '2026-08-20T00:00:00Z',
  updated_at: '2026-08-20T00:00:00Z',
};

describe('PacketArtifactViewer', () => {
  it('provides a vertical scroll container so short screens can reach the hex view', () => {
    render(
      <PacketArtifactViewer
        capture={capture}
        downloading={false}
        onDownload={vi.fn()}
        tr={(zh) => zh}
      />,
    );

    const analysis = screen.getByTestId('packet-analysis-view');
    expect(analysis).toHaveClass('overflow-y-auto', 'overscroll-contain');
    expect(analysis.className).toContain('[@media(min-height:900px)]:overflow-hidden');
    expect(screen.getByText('OFFSET')).toBeInTheDocument();
  });
});
