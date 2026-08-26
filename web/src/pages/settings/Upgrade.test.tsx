import { render, screen } from '@testing-library/react';
import { http, HttpResponse } from 'msw';
import { beforeEach, describe, expect, it } from 'vitest';

import SettingsUpgrade from './Upgrade';
import { server } from '@/test/msw-server';

const amd64Command = [
  'curl -fL -O https://ongrid.cloud/dl/ongrid-v0.11.1-linux-amd64.tar.xz || wget https://ongrid.cloud/dl/ongrid-v0.11.1-linux-amd64.tar.xz',
  'tar xf ongrid-v0.11.1-linux-amd64.tar.xz && cd ongrid-v0.11.1-linux-amd64',
  'sudo ./upgrade.sh',
].join('\n');

const arm64Command = amd64Command.replace(/amd64/g, 'arm64');
const autoCommand = 'auto-detect command';

describe('SettingsUpgrade', () => {
  beforeEach(() => {
    localStorage.setItem('ongrid-locale', 'zh-CN');
    server.use(
      http.post('/api/v1/system/upgrade/check', () =>
        HttpResponse.json({
          current_version: 'v0.11.0',
          latest_version: 'v0.11.1',
          update_available: true,
          comparison_supported: true,
          checked_at: '2026-08-04T08:00:00Z',
          commands: [
            {
              id: 'linux-amd64',
              label: 'Linux amd64',
              arch: 'linux-amd64',
              command: amd64Command,
            },
            {
              id: 'linux-arm64',
              label: 'Linux arm64',
              arch: 'linux-arm64',
              command: arm64Command,
            },
            {
              id: 'auto',
              label: 'Auto-detect Linux arch',
              arch: 'linux',
              command: autoCommand,
            },
          ],
        }),
      ),
    );
  });

  it('按照原页面展示 AMD64、ARM64 和自动识别命令', async () => {
    render(<SettingsUpgrade />);

    expect(await screen.findByText('Linux amd64 服务器')).toBeInTheDocument();
    expect(screen.getByText('Linux arm64 服务器')).toBeInTheDocument();
    expect(screen.getByText('自动识别 Linux 架构')).toBeInTheDocument();
    expect(screen.getByText((_, element) => (
      element?.tagName === 'CODE' && element.textContent === amd64Command
    ))).toBeInTheDocument();
    expect(screen.getAllByRole('button', { name: '复制命令' })).toHaveLength(3);
    const labels = screen.getAllByText(
      /Linux (amd64|arm64) 服务器|自动识别 Linux 架构/,
    );
    expect(labels.map((node) => node.textContent)).toEqual([
      'Linux amd64 服务器',
      'Linux arm64 服务器',
      '自动识别 Linux 架构',
    ]);
  });
});
