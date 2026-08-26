// AdminLayout — top-level "管理" section. Mirrors SettingsLayout but
// holds platform-governance pages (who has access, who did what)
// instead of product-behavior config (LLM provider, integrations).
//
// Why split out of /settings:
//   - Settings answers "how does the platform behave" (deploy-time)
//   - Admin answers "who uses it + what did they do" (operations)
//   - Different visitors, different cadence; mixing made /settings
//     a junk drawer as RBAC + audit landed.
//
// Routes mounted under /admin/*:
//   /admin/users — system users (superuser-only writes)
//   /admin/orgs — orgs + memberships
//   /admin/audit — audit log (who did what, when)
//   /admin/webshell — WebSSH session audit + admin-kill
//
// Future additions: /admin/roles, /admin/security.
import { Suspense } from 'react';
import { NavLink, Outlet } from 'react-router-dom';
import {
  Building2,
  Loader2,
  ScrollText,
  Shield,
  Users as UsersIcon,
} from 'lucide-react';
import { cn } from '@/lib/cn';
import type { IconType } from '@/lib/icon';
import { Card, EmptyState, PageHeader } from '@/components/ui';
import { tr } from '@/i18n/locale';
import { usePermissions } from '@/store/me';

type RailItem = {
  to: string;
  icon: IconType;
  label: string;
  hint: string;
};

function railItems(): RailItem[] {
  return [
    { to: 'users', icon: UsersIcon, label: tr('用户', 'Users'), hint: tr('系统用户与系统角色', 'System users and system role') },
    { to: 'orgs', icon: Building2, label: tr('组织', 'Orgs'), hint: tr('组织与组织成员管理', 'Orgs and org memberships') },
    { to: 'audit', icon: ScrollText, label: tr('审计日志', 'Audit log'), hint: tr('谁在何时做了什么', 'Who did what, when') },
  ];
}

export default function AdminLayout() {
  const items = railItems();
  const { isAdmin } = usePermissions();
  // route-level gate. The sidebar already hides /admin/* for
  // non-admins, but a stale deep-link / typed URL still lands here —
  // show an EmptyState rather than rendering the rail + outlet (which
  // would just stack child EmptyStates and look weird).
  if (!isAdmin) {
    return (
      <main className="anim-fade flex flex-1 flex-col overflow-hidden p-6">
        <Card className="p-6">
          <EmptyState
            icon={Shield}
            title={tr('需要管理员权限', 'Admin permission required')}
            hint={tr('只有管理员（admin）才能访问用户管理。请联系管理员授予权限。', 'Only admins can access user management. Ask an admin to grant permission.')}
          />
        </Card>
      </main>
    );
  }
  return (
    <main className="anim-fade flex flex-1 flex-col overflow-hidden">
      <PageHeader title={tr('用户管理', 'Admin')} subtitle={tr('用户 / 组织 / 审计；platform governance', 'Users / orgs / audit — platform governance')} />

      <div className="flex-1 overflow-hidden">
        <div className="grid h-full grid-cols-1 lg:grid-cols-[240px_1fr]">
          <nav
            aria-label={tr('管理分类', 'Admin categories')}
            className={cn(
              'border-zinc-800 bg-zinc-950/40',
              'lg:h-full lg:overflow-y-auto lg:border-r',
              'flex shrink-0 gap-1 overflow-x-auto border-b px-3 py-3 lg:flex-col lg:gap-0.5 lg:px-3 lg:py-4',
            )}
          >
            {items.map((item) => (
              <RailLink key={item.to} item={item} />
            ))}
          </nav>

          <div className="overflow-y-auto">
            {/* Local Suspense — keeps the rail + page header mounted
                while a lazy-loaded leaf route resolves; otherwise the
                fallback bubbles to Layout.tsx and the whole admin
                shell flashes / re-fades on every rail click. */}
            <Suspense
              fallback={
                <div className="flex h-32 items-center justify-center text-sm text-zinc-500">
                  <Loader2 size={14} className="mr-2 animate-spin" /> {tr('加载中…', 'Loading…')}
                </div>
              }
            >
              <Outlet />
            </Suspense>
          </div>
        </div>
      </div>
    </main>
  );
}

function RailLink({ item }: { item: RailItem }) {
  const Icon = item.icon;
  return (
    <NavLink
      to={item.to}
      className={({ isActive }) =>
        cn(
          'group relative flex shrink-0 items-center gap-2 rounded-md px-3 py-2 text-sm transition-colors',
          'lg:gap-3',
          isActive
            ? 'bg-zinc-800 text-zinc-100'
            : 'text-zinc-400 hover:bg-zinc-800/60 hover:text-zinc-100',
        )
      }
    >
      {({ isActive }) => (
        <>
          {isActive && (
            <span
              aria-hidden
              className="absolute inset-y-1.5 left-0 hidden w-0.5 rounded-r bg-accent lg:block"
            />
          )}
          <Icon
            size={14}
            className={cn(
              'shrink-0',
              isActive ? 'text-accent' : 'text-zinc-500 group-hover:text-zinc-300',
            )}
          />
          <div className="min-w-0 flex-1">
            <div className="truncate text-[13px] font-medium">{item.label}</div>
            <div className="hidden truncate text-[11px] text-zinc-500 lg:block">{item.hint}</div>
          </div>
        </>
      )}
    </NavLink>
  );
}
