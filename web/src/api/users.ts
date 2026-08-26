// users.ts — typed wrappers for the user / membership / self endpoints.
//
// Backend routes (manager v1):
//   GET /api/v1/me
//   GET /api/v1/users
//   POST /api/v1/users
//   PATCH /api/v1/users/{id} (display_name? phone? status?)
//   PATCH /api/v1/users/{id}/role ("admin" | "user")
//   PATCH /api/v1/users/{id}/password (password)
//   DELETE /api/v1/users/{id}
//
// 角色拆分（2026-05 收敛）：
//   role = 系统级（"admin" | "user"），admin 等价于过去的 superuser
//   memberships[].role = 组织级（"org_admin" | "member" | "viewer"）
import { request } from './client';

// SystemRole is the platform privilege tier.
//   admin = full system + user/org management
//   user = uses all functional features (chat, mutating tools), no platform config
//   viewer = read-only + restricted chat (LLM gets read-only toolbag)
export type SystemRole = 'admin' | 'user' | 'viewer';
export type UserStatus = 'active' | 'disabled';

// OrgMembership — me.memberships[] 用，描述当前用户在某个组织里的角色。
export type OrgMembership = {
  org_id: number;
  org_name: string;
  role: 'org_admin' | 'member' | 'viewer';
};

// Me — /v1/me 返回；前端用来分歧 admin-only UI / 默认组织拼接。
export type Me = {
  id: number;
  email: string;
  display_name: string;
  phone: string;
  role: SystemRole;
  status: UserStatus;
  memberships: OrgMembership[];
};

// User — /v1/users list/create 返回的整行；管理列表用。
export type User = {
  id: number;
  email: string;
  display_name: string;
  phone: string;
  role: SystemRole;
  status: UserStatus;
  created_at: string;
  updated_at: string;
};

// UserListItem 别名 — 保留语义，未来若 list 返回精简 view 可分裂。
export type UserListItem = User;

export type UserListResp = { items: UserListItem[]; total: number };

export function getMe(): Promise<Me> {
  return request<Me>('GET', '/me');
}

export function listUsers(): Promise<UserListResp> {
  return request<UserListResp>('GET', '/users');
}

export type CreateUserBody = {
  email: string;
  password: string;
  display_name?: string;
  phone?: string;
  role?: SystemRole;
};

export function createUser(body: CreateUserBody): Promise<User> {
  return request<User>('POST', '/users', body);
}

// PATCH 部分字段：只发 caller 提供的；后端按 zero-value-aware 处理。
export type PatchUserBody = {
  display_name?: string;
  phone?: string;
  status?: UserStatus;
};

export function patchUser(id: number, body: PatchUserBody): Promise<User> {
  return request<User>('PATCH', `/users/${id}`, body);
}

export function setUserRole(id: number, role: SystemRole): Promise<User> {
  return request<User>('PATCH', `/users/${id}/role`, { role });
}

export function setUserPassword(id: number, password: string): Promise<{ ok: boolean }> {
  return request<{ ok: boolean }>('PATCH', `/users/${id}/password`, { password });
}

export function deleteUser(id: number): Promise<void> {
  return request<void>('DELETE', `/users/${id}`);
}
