import { create } from 'zustand';
import { listSessions, type ChatSession } from '@/api/chat';

// chatSessionsStore is the single source of truth for the sidebar's
// session list. Components that mutate sessions (creating a new one in
// Home, sending the first message that bumps `updated_at`, etc.) call
// invalidateChatSessions() to trigger a refetch so the sidebar updates
// without a full page reload.
type ChatSessionsState = {
  sessions: ChatSession[];
  loading: boolean;
  refresh(): Promise<void>;
};

export const useChatSessions = create<ChatSessionsState>((set) => ({
  sessions: [],
  loading: false,
  async refresh() {
    set({ loading: true });
    try {
      const r = await listSessions();
      set({ sessions: r.items ?? [], loading: false });
    } catch {
      set({ loading: false });
    }
  },
}));

// invalidateChatSessions triggers a refetch from anywhere in the app.
// Cheap to call; the store dedupes via React's batched re-renders.
export function invalidateChatSessions() {
  void useChatSessions.getState().refresh();
}
