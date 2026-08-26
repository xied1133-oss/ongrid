import { render, screen } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { http, HttpResponse } from 'msw';
import { beforeEach, describe, expect, it } from 'vitest';

import { server } from '@/test/msw-server';
import { StatusRow } from './StatusRow';

describe('StatusRow', () => {
  beforeEach(() => {
    localStorage.setItem('ongrid-locale', 'zh-CN');
    server.use(
      http.get('/api/v1/devices', () =>
        HttpResponse.json({
          items: [
            {
              id: 33,
              name: 'kind-local-control-plane',
              online: true,
              last_seen_at: '2026-07-10T09:00:00Z',
            },
          ],
          total: 1,
        }),
      ),
      http.get('/api/v1/chat/sessions', () => HttpResponse.json({ items: [], total: 0 })),
      http.get('/api/v1/usage/today', () => HttpResponse.json({ total_tokens: 0 })),
    );
  });

  it('只统计已经完成注册的 Device', async () => {
    render(
      <MemoryRouter>
        <StatusRow />
      </MemoryRouter>,
    );

    expect(await screen.findByText('1/1 在线设备')).toBeInTheDocument();
    expect(screen.queryByText('2/2 在线设备')).not.toBeInTheDocument();
  });

  it('将 SNMP 网络设备的可达性计入可用设备', async () => {
    server.use(
      http.get('/api/v1/devices', () =>
        HttpResponse.json({
          items: [
            { id: 33, name: 'host-a', online: true },
            {
              id: 34,
              name: 'switch-a',
              os: 'network',
              online: false,
              reachability_status: 'reachable',
            },
          ],
          total: 2,
        }),
      ),
    );

    render(
      <MemoryRouter>
        <StatusRow />
      </MemoryRouter>,
    );

    expect(await screen.findByText('2/2 在线设备')).toBeInTheDocument();
  });
});
