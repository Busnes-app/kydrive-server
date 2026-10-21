import type { ReactNode } from 'react';
import { AlertCircle, CheckCircle2, Info } from 'lucide-react';

// Alert is an inline error (role alert), or a success or information note (role status).
export function Alert({ children, tone = 'error' }: { children: ReactNode; tone?: 'error' | 'success' | 'info' }) {
  const Icon = { error: AlertCircle, success: CheckCircle2, info: Info }[tone];
  return <div role={tone === 'error' ? 'alert' : 'status'} className={`alert alert-${tone}`}><Icon size={18} aria-hidden="true" /><div>{children}</div></div>;
}
