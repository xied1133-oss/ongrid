import { Suspense, useEffect } from 'react';
import { Outlet } from 'react-router-dom';
import { Sidebar } from './Sidebar';
import { CommandPalette } from './CommandPalette';
import { AgentSidePanel } from './AgentSidePanel';
import { useUi } from '@/store/ui';
import { useIncidentBadge } from '@/store/incidentBadge';
import { tr as trInline } from '@/i18n/locale';

export function Layout() {
  // Start polling the unack'd incident counter on first authenticated mount;
  // useAuth gates RequireAuth before Layout, so we can assume there is a
  // token here. Stops on unmount (logout flips back to /login).
  useEffect(() => {
    const start = useIncidentBadge.getState().start;
    const stop = useIncidentBadge.getState().stop;
    start();
    return () => stop();
  }, []);

  const paletteOpen = useUi((s) => s.paletteOpen);
  const setPaletteOpen = useUi((s) => s.setPaletteOpen);
  const agentPanelOpen = useUi((s) => s.agentPanelOpen);
  const setAgentPanelOpen = useUi((s) => s.setAgentPanelOpen);

  // Global hotkeys: ⌘K opens the agent side panel, ⌘P opens the
  // command palette. We bind on window so the keystroke catches even
  // when no input has focus. Inputs that need to handle ⌘K/⌘P
  // themselves can stopPropagation, but none currently do.
  useEffect(() => {
    const handler = (e: KeyboardEvent) => {
      const ctrl = e.ctrlKey || e.metaKey;
      if (!ctrl) return;
      if (e.key === 'k' || e.key === 'K') {
        e.preventDefault();
        setAgentPanelOpen(!useUi.getState().agentPanelOpen);
      } else if (e.key === 'p' || e.key === 'P') {
        e.preventDefault();
        setPaletteOpen(!useUi.getState().paletteOpen);
      }
    };
    window.addEventListener('keydown', handler);
    return () => window.removeEventListener('keydown', handler);
  }, [setAgentPanelOpen, setPaletteOpen]);

  return (
    <div className="flex h-screen min-h-0 w-screen min-w-0 overflow-hidden bg-zinc-950 text-zinc-100">
      <Sidebar />
      <div className="flex min-h-0 min-w-0 flex-1 overflow-hidden">
        <Suspense fallback={<MainLoading />}>
          <Outlet />
        </Suspense>
      </div>
      <CommandPalette open={paletteOpen} onClose={() => setPaletteOpen(false)} />
      <AgentSidePanel open={agentPanelOpen} onClose={() => setAgentPanelOpen(false)} />
    </div>
  );
}

function MainLoading() {
  // No hook here — useI18n requires a render-time subscription and
  // this Suspense fallback flashes for a sub-second on first paint
  // for each lazy-loaded route. Inline tr() (the standalone, locale-
  // reading-from-localStorage version) keeps the fallback cheap.
  const text = trInline('加载中…', 'Loading…');
  return (
    <main className="flex flex-1 items-center justify-center text-sm text-zinc-500">
      {text}
    </main>
  );
}
