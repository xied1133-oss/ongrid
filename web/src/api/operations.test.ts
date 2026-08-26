import { describe, expect, it } from 'vitest';
import { HttpResponse, http } from 'msw';

import { server } from '@/test/msw-server';

import { executeOperationAction, getOperation } from './operations';

describe('operations api', () => {
  it('normalizes legacy PascalCase operation detail responses', async () => {
    server.use(
      http.get('/api/v1/operations/op-123', () => HttpResponse.json({
        operation: {
          ID: 'op-123',
          Kind: 'packet_capture_session',
          State: 'succeeded',
          Title: 'HTTPS capture',
          Summary: '1/1 ready',
          DetailURL: '/artifacts/packet-sessions/pcap-session-1',
          ActionsJSON: '[]',
        },
        artifacts: [{
          ID: 'artifact-1',
          OperationID: 'op-123',
          Kind: 'analysis',
          Title: 'HTTPS capture',
          URL: '/artifacts/packet-sessions/pcap-session-1',
          MetadataJSON: '{}',
          CreatedAt: '2026-08-15T09:30:00Z',
        }],
      })),
    );

    const detail = await getOperation('op-123');

    expect(detail.operation).toMatchObject({
      id: 'op-123',
      kind: 'packet_capture_session',
      state: 'succeeded',
      title: 'HTTPS capture',
      summary: '1/1 ready',
      detail_url: '/artifacts/packet-sessions/pcap-session-1',
      actions_json: '[]',
    });
    expect(detail.artifacts?.[0]).toMatchObject({
      id: 'artifact-1',
      operation_id: 'op-123',
      url: '/artifacts/packet-sessions/pcap-session-1',
    });
  });

  it('normalizes operation action responses', async () => {
    server.use(
      http.post('/api/v1/operations/op-123/actions/cancel', () => HttpResponse.json({
        ID: 'op-123',
        Kind: 'packet_capture_session',
        State: 'cancelled',
        Title: 'HTTPS capture',
        ActionsJSON: '[]',
      })),
    );

    const operation = await executeOperationAction('op-123', 'cancel');

    expect(operation).toMatchObject({
      id: 'op-123',
      kind: 'packet_capture_session',
      state: 'cancelled',
      title: 'HTTPS capture',
      actions_json: '[]',
    });
  });
});
