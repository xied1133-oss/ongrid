import { create } from 'zustand';
import { persist, createJSONStorage } from 'zustand/middleware';

export type Session = {
  access_token: string;
  refresh_token?: string;
  role: string;
  email: string;
};

export type AuthState = {
  token: string | null;
  refreshToken: string | null;
  email: string | null;
  role: string | null;
  setSession(s: Session): void;
  logout(): void;
};

export const useAuth = create<AuthState>()(
  persist(
    (set) => ({
      token: null,
      refreshToken: null,
      email: null,
      role: null,
      setSession: (s) =>
        set({
          token: s.access_token,
          refreshToken: s.refresh_token ?? null,
          role: s.role,
          email: s.email,
        }),
      logout: () => set({ token: null, refreshToken: null, email: null, role: null }),
    }),
    {
      name: 'ongrid.auth',
      storage: createJSONStorage(() => localStorage),
    }
  )
);

export function getToken(): string | null {
  return useAuth.getState().token;
}

export function getRefreshToken(): string | null {
  return useAuth.getState().refreshToken;
}
