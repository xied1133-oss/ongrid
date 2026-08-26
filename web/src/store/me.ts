// me.ts — small in-memory cache + hook for /v1/me.
//
// 只缓存当前会话；不持久化到 localStorage（auth token 已经持久了，重登
// 后这里会被组件的 useEffect 重拉一次）。组件用法：
//   const { me, loading, error, refresh } = useMe();
//   if (me?.role !== 'admin') return <EmptyState ... />
import { useCallback, useEffect, useState } from 'react';
import { create } from 'zustand';
import { ApiError } from '@/api/client';
import { getMe, type Me } from '@/api/users';
import { useAuth } from '@/store/auth';

type MeStore = {
  me: Me | null;
  loading: boolean;
  error: string | null;
  inflight: Promise<Me> | null;
  set(me: Me): void;
  clear(): void;
  setLoading(b: boolean): void;
  setError(s: string | null): void;
  setInflight(p: Promise<Me> | null): void;
};

const useMeStore = create<MeStore>((set) => ({
  me: null,
  loading: false,
  error: null,
  inflight: null,
  set: (me) => set({ me, error: null }),
  clear: () => set({ me: null, error: null, inflight: null, loading: false }),
  setLoading: (b) => set({ loading: b }),
  setError: (s) => set({ error: s }),
  setInflight: (p) => set({ inflight: p }),
}));

// loadMe — single-flight; 多个组件同时挂载只发一次请求。
export async function loadMe(force = false): Promise<Me | null> {
  const st = useMeStore.getState();
  if (!force && st.me) return st.me;
  if (st.inflight) return st.inflight;

  useMeStore.getState().setLoading(true);
  useMeStore.getState().setError(null);
  const p = getMe()
    .then((m) => {
      useMeStore.getState().set(m);
      return m;
    })
    .catch((e) => {
      const msg = e instanceof ApiError ? e.message : (e as Error).message;
      useMeStore.getState().setError(msg);
      throw e;
    })
    .finally(() => {
      useMeStore.getState().setLoading(false);
      useMeStore.getState().setInflight(null);
    });

  useMeStore.getState().setInflight(p);
  try {
    return await p;
  } catch {
    return null;
  }
}

// useMe — hook 接口。token 失效时自动清空，登录后 mount 自动拉一次。
export function useMe() {
  const me = useMeStore((s) => s.me);
  const loading = useMeStore((s) => s.loading);
  const error = useMeStore((s) => s.error);
  const token = useAuth((s) => s.token);
  const [, setTick] = useState(0);

  const refresh = useCallback(async () => {
    await loadMe(true);
    setTick((n) => n + 1);
  }, []);

  useEffect(() => {
    if (!token) {
      useMeStore.getState().clear();
      return;
    }
    if (!me && !loading) {
      void loadMe(false);
    }
  }, [token, me, loading]);

  return { me, loading, error, refresh };
}

// usePermissions — derived flags off `me.role`. Falls back
// to useAuth().role so the very first render after login isn't gated
// off (admin entries would otherwise flicker hidden until /v1/me
// resolves; that flash was reported as "我登录了 admin 但看不到用户
// 管理 / 设置"). useAuth().role comes straight from the JWT login
// response so it's available synchronously on first paint.
export function usePermissions() {
  const { me } = useMe();
  const authRole = useAuth((s) => s.role);
  const role = me?.role ?? authRole;
  return {
    role,
    isAdmin: role === 'admin',
    isUser: role === 'user',
    isViewer: role === 'viewer',
    // canMutate covers create / modify / delete of own resources +
    // running ClassMutating skills + ack-ing incidents. Excludes viewer.
    canMutate: role === 'admin' || role === 'user',
  };
}
