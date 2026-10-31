import { useCallback, useEffect, useId, useRef, useState } from 'react';
import type { FormEvent, ReactNode } from 'react';
import { X } from 'lucide-react';

// Dialog is the suite's modal: a native <dialog> with a titled header, a scrolling body and a
// footer of actions (secondary first, primary last). Escape and the close button call onClose.
export function Dialog({ title, description, children, footer, onClose, onSubmit, wide, className, closeButton = true }: {
  title: ReactNode; description?: ReactNode; children?: ReactNode; footer?: ReactNode; closeButton?: boolean;
  onClose: () => void; onSubmit?: (e: FormEvent<HTMLFormElement>) => void; wide?: boolean; className?: string;
}) {
  const ref = useRef<HTMLDialogElement>(null);
  const id = useId();
  useEffect(() => {
    const d = ref.current;
    const opener = document.activeElement instanceof HTMLElement ? document.activeElement : null;
    // jsdom lacks showModal; opening by attribute keeps the dialog reachable in unit tests.
    if (d && !d.open) { if (typeof d.showModal === 'function') d.showModal(); else d.setAttribute('open', ''); }
    return () => { if (d?.open && typeof d.close === 'function') d.close(); opener?.focus(); };
  }, []);
  const inner = <>
    <header className="dialog-header">
      <div><h2 id={`${id}-title`}>{title}</h2>{description && <p>{description}</p>}</div>
      {/* Without the icon, the footer's Cancel and Escape close the dialog. */}
      {closeButton && <button type="button" className="icon-btn" aria-label="Close" onClick={onClose}><X size={18} aria-hidden="true" /></button>}
    </header>
    <div className="dialog-body">{children}</div>
    {footer && <footer className="dialog-footer">{footer}</footer>}
  </>;
  return <dialog ref={ref} className={['dialog', wide ? 'dialog-wide' : '', className ?? ''].filter(Boolean).join(' ')} aria-labelledby={`${id}-title`}
    onCancel={e => { e.preventDefault(); onClose(); }}>
    {onSubmit ? <form onSubmit={e => { e.preventDefault(); onSubmit(e); }}>{inner}</form> : inner}
  </dialog>;
}

type Ask = { title: string; body?: ReactNode; confirm: string; danger?: boolean; resolve: (ok: boolean) => void };

// useConfirm replaces window.confirm with a styled dialog. ask() resolves true only on the
// confirm button; Cancel, Escape and the close button resolve false.
export function useConfirm(): [(q: Omit<Ask, 'resolve'>) => Promise<boolean>, ReactNode] {
  const [ask, setAsk] = useState<Ask | null>(null);
  const open = useCallback((q: Omit<Ask, 'resolve'>) => new Promise<boolean>(resolve => setAsk({ ...q, resolve })), []);
  const done = (ok: boolean) => { ask?.resolve(ok); setAsk(null); };
  const node = ask && <Dialog title={ask.title} onClose={() => done(false)} onSubmit={() => done(true)}
    footer={<><button type="button" className="btn-secondary" onClick={() => done(false)}>Cancel</button><button type="submit" className={ask.danger ? 'btn-danger' : undefined} autoFocus>{ask.confirm}</button></>}>
    {typeof ask.body === 'string' ? <p>{ask.body}</p> : ask.body}
  </Dialog>;
  return [open, node];
}
