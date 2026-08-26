import { act, render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter } from 'react-router-dom';
import { http, HttpResponse } from 'msw';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import { Sidebar } from './Sidebar';
import { useAuth } from '@/store/auth';
import { useUi } from '@/store/ui';
import { server } from '@/test/msw-server';

vi.mock('@/store/me', () => ({
  useMe: () => ({ me: null, loading: false, error: null, refresh: vi.fn() }),
  usePermissions: () => ({ isAdmin: false, canMutate: true, role: 'user' }),
}));

describe('Sidebar configurable sections', () => {
  beforeEach(() => {
    localStorage.clear();
    localStorage.setItem('ongrid-locale', 'zh-CN');
    useAuth.setState({ token: null, refreshToken: null, email: 'user@example.com', role: 'user' });
    useUi.getState().setSidebarCollapsed(false);
    server.use(
      http.get('/api/v1/chat/sessions', () => HttpResponse.json({ items: [], total: 0 })),
      http.get('/api/v1/system-settings', () => HttpResponse.json({ items: [], total: 0 })),
    );
  });

  it('隐藏子菜单并从父菜单管理入口恢复', async () => {
    const user = userEvent.setup();
    render(
      <MemoryRouter>
        <Sidebar />
      </MemoryRouter>,
    );

    expect(await screen.findByRole('link', { name: '网络设备' })).toBeInTheDocument();
    await act(async () => {
      await user.click(screen.getByRole('button', { name: '从侧栏取消固定网络设备' }));
    });

    expect(screen.queryByRole('link', { name: '网络设备' })).not.toBeInTheDocument();
    expect(localStorage.getItem('sidebar.section.resources.hidden')).toContain('network-devices');

    await act(async () => {
      await user.click(screen.getByRole('button', { name: '管理基础设施菜单' }));
    });
    const checkbox = screen.getByRole('checkbox', { name: '网络设备' });
    expect(checkbox).not.toBeChecked();
    await act(async () => {
      await user.click(checkbox);
    });

    await waitFor(() => {
      expect(screen.getByRole('link', { name: '网络设备' })).toBeInTheDocument();
    });
    expect(localStorage.getItem('sidebar.section.resources.hidden')).toBe('[]');
  });

  it('所有可折叠分组都提供菜单管理入口', () => {
    render(
      <MemoryRouter>
        <Sidebar />
      </MemoryRouter>,
    );

    expect(screen.getByRole('button', { name: '管理Agent菜单' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: '管理知识库菜单' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: '管理基础设施菜单' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: '管理监控告警菜单' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: '管理日常菜单' })).toBeInTheDocument();
  });

  it('可选升级菜单设置读取失败时仍保持默认导航', async () => {
    server.use(
      http.get('/api/v1/system-settings', () => HttpResponse.error()),
    );

    render(
      <MemoryRouter>
        <Sidebar />
      </MemoryRouter>,
    );

    expect(await screen.findByRole('link', { name: '网络设备' })).toBeInTheDocument();
  });
});
