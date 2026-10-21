// Dates from the server are RFC 3339 (often with nanoseconds) or Unix seconds. People see the
// local date and time, and how long ago it was; the exact instant stays in <time dateTime>.

const units: [Intl.RelativeTimeFormatUnit, number][] = [['year', 31536000], ['month', 2592000], ['week', 604800], ['day', 86400], ['hour', 3600], ['minute', 60]];

export function toDate(v: string | number): Date | null {
  const d = typeof v === 'number' ? new Date(v * 1000) : new Date(v);
  return Number.isNaN(d.getTime()) ? null : d;
}

// relative is "just now", "5 minutes ago", "yesterday", "in 3 days".
export function relative(d: Date, now = new Date()): string {
  const seconds = (d.getTime() - now.getTime()) / 1000;
  if (Math.abs(seconds) < 45) return 'just now';
  const format = new Intl.RelativeTimeFormat(undefined, { numeric: 'auto' });
  for (const [unit, size] of units) if (Math.abs(seconds) >= size) return format.format(Math.round(seconds / size), unit);
  return format.format(Math.round(seconds / 60), 'minute');
}

// local is the date and time in the browser's locale, e.g. "21 Oct 2026, 14:03".
export function local(d: Date): string {
  return d.toLocaleString(undefined, { year: 'numeric', month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit' });
}

export function localDate(d: Date): string {
  return d.toLocaleDateString(undefined, { year: 'numeric', month: 'short', day: 'numeric' });
}
