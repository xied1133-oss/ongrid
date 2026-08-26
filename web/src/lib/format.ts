import { getLocale, tr as trInline } from '@/i18n/locale';

// Map our app-locale tag onto the Intl BCP-47 locale used by the
// Date / Number formatters. We use the APP locale (operator's
// explicit pick) instead of the browser locale because the operator
// may run a Chinese OS but want English dates in the product.
function intlLocale(): string {
  return getLocale() === 'en-US' ? 'en-US' : 'zh-CN';
}

export function relativeTime(input: string | number | Date | null | undefined): string {
  if (!input) return '—';
  const t = typeof input === 'string' || typeof input === 'number' ? new Date(input) : input;
  const ms = Date.now() - t.getTime();
  if (Number.isNaN(ms)) return '—';
  const sec = Math.round(ms / 1000);
  if (sec < 5) return trInline('刚刚', 'just now');
  if (sec < 60) return trInline(`${sec} 秒前`, `${sec}s ago`);
  const min = Math.round(sec / 60);
  if (min < 60) return trInline(`${min} 分钟前`, `${min}m ago`);
  const hr = Math.round(min / 60);
  if (hr < 24) return trInline(`${hr} 小时前`, `${hr}h ago`);
  const day = Math.round(hr / 24);
  if (day < 30) return trInline(`${day} 天前`, `${day}d ago`);
  return t.toLocaleDateString(intlLocale());
}

export function formatBytes(b: number | undefined | null): string {
  if (b === null || b === undefined || Number.isNaN(b)) return '—';
  const units = ['B', 'KB', 'MB', 'GB', 'TB'];
  let v = Math.abs(b);
  let i = 0;
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024;
    i++;
  }
  return `${v.toFixed(v < 10 ? 2 : 1)} ${units[i]}`;
}

export function clockTime(input: string | number | Date): string {
  const t = typeof input === 'string' || typeof input === 'number' ? new Date(input) : input;
  return t.toLocaleTimeString(intlLocale(), { hour: '2-digit', minute: '2-digit', second: '2-digit' });
}

// fullDateTime — for "last sync at X" style displays. Browser would
// format YYYY/M/D for zh-CN OS (looks weird in an English app);
// passing the app locale lets us render "5/18/2026, 10:30:45 AM" for
// English operators on Chinese OS. Single call site (instead of
// scattered toLocaleString()) so locale plumbing stays in one place.
export function fullDateTime(input: string | number | Date | null | undefined): string {
  if (!input) return '—';
  const t = typeof input === 'string' || typeof input === 'number' ? new Date(input) : input;
  if (Number.isNaN(t.getTime())) return '—';
  return t.toLocaleString(intlLocale());
}

// formatNumber — Intl.NumberFormat with operator locale. English
// shows 1,234,567; Chinese leaves grouping off by default. Used in
// place of bare String(n) or n.toString() in metric / count renders.
export function formatNumber(n: number | null | undefined): string {
  if (n === null || n === undefined || Number.isNaN(n)) return '—';
  return new Intl.NumberFormat(intlLocale()).format(n);
}
