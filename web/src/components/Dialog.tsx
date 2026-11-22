import { useCallback, useRef, useState } from 'react';
import type { FormEvent, ReactNode } from 'react';
import { Dialog as Base } from '@base-ui/react/dialog';
import { X } from 'lucide-react';

// Dialog is the suite's modal, on Base UI's Dialog: a titled header, a scrolling body and a
// footer of actions (secondary first, primary last). Escape and the close button call onClose;
// a click on the backdrop does not, so a half-filled form survives a stray click.
export function Dialog({ title, description, children, footer, onClose, onSubmit, wide, className, closeButton = true }: {
  title: ReactNode; description?: ReactNode; children?: ReactNode; footer?: ReactNode; closeButton?: boolean;
  onClose: () => void; onSubmit?: (e: FormEvent<HTMLFormElement>) => void; wide?: boolean; className?: string;
}) {
  const popup = useRef<HTMLDivElement>(null);
  const inner = <>
    <header className="dialog-header">
      <div><Base.Title>{title}</Base.Title>{description && <Base.Description>{description}</Base.Description>}</div>
      {/* Without the icon, the footer's Cancel and Escape close the dialog. */}
      {closeButton && <button type="button" className="icon-btn" aria-label="Close" onClick={onClose}><X size={18} aria-hidden="true" /></button>}
    </header>
    <div className="dialog-body">{children}</div>
    {footer && <footer className="dialog-footer">{footer}</footer>}
  </>;
  return <Base.Root open disablePointerDismissal onOpenChange={open => { if (!open) onClose(); }}>
    <Base.Portal>
      <Base.Backdrop className="dialog-backdrop" />
      {/* An autoFocus field inside keeps focus; otherwise Base UI focuses the first control. */}
      <Base.Popup ref={popup} className={['dialog', wide ? 'dialog-wide' : '', className ?? ''].filter(Boolean).join(' ')}
        initialFocus={() => popup.current?.contains(document.activeElement) ? document.activeElement as HTMLElement : true}>
        {onSubmit ? <form onSubmit={e => { e.preventDefault(); onSubmit(e); }}>{inner}</form> : inner}
      </Base.Popup>
    </Base.Portal>
  </Base.Root>;
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
