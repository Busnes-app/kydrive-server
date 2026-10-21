import { local, localDate, relative, toDate } from './time';

// When shows a server time as local date and time plus how long ago, or the date alone.
export function When({ value, dateOnly, relativeOnly }: { value: string | number; dateOnly?: boolean; relativeOnly?: boolean }) {
  const d = toDate(value);
  if (!d) return <>{String(value)}</>;
  const full = local(d);
  if (dateOnly) return <time dateTime={d.toISOString()} title={full}>{localDate(d)}</time>;
  if (relativeOnly) return <time dateTime={d.toISOString()} title={full}>{relative(d)}</time>;
  return <time dateTime={d.toISOString()} title={full}>{full} · {relative(d)}</time>;
}
