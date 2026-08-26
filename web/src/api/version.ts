import { request } from './client';

export type VersionInfo = {
  manager_version: string;
};

export function getManagerVersion() {
  return request<VersionInfo>('GET', '/version');
}
