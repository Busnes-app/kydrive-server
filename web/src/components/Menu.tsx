import { useEffect, useId, useRef, useState } from 'react';
import type { ReactNode } from 'react';
import { ChevronDown, MoreHorizontal, Plus } from 'lucide-react';

export type MenuEntry = { label: string; icon?: ReactNode; danger?: boolean; disabled?: boolean } & ({ onSelect: () => void } | { href: string; newTab?: boolean }) | 'separator';

// Menu is a button that opens a list of actions beside it. It closes after any choice, on
// Escape (returning focus to the button) and on a click or focus outside it.
export function Menu({ label, items, trigger = 'more', className, align = 'right' }: { label: string; items: MenuEntry[]; trigger?: 'more' | 'text' | 'primary'; className?: string; align?: 'left' | 'right' }) {
  const [open, setOpen] = useState(false);
  const [up, setUp] = useState(false);
  const wrap = useRef<HTMLDivElement>(null);
  const button = useRef<HTMLButtonElement>(null);
  const id = useId();
  useEffect(() => {
    if (!open) return;
    const outside = (e: Event) => { if (!wrap.current?.contains(e.target as Node)) setOpen(false); };
    const key = (e: KeyboardEvent) => {
      if (e.key === 'Escape') { setOpen(false); button.current?.focus(); return; }
      if (e.key !== 'ArrowDown' && e.key !== 'ArrowUp') return;
      const all = Array.from(wrap.current?.querySelectorAll<HTMLElement>('[role="menuitem"]:not([disabled])') ?? []);
      if (all.length === 0) return;
      e.preventDefault();
      const at = all.indexOf(document.activeElement as HTMLElement);
      all[(at + (e.key === 'ArrowDown' ? 1 : all.length - 1)) % all.length].focus();
    };
    document.addEventListener('pointerdown', outside);
    document.addEventListener('focusin', outside);
    document.addEventListener('keydown', key);
    wrap.current?.querySelector<HTMLElement>('[role="menuitem"]:not([disabled])')?.focus();
    return () => { document.removeEventListener('pointerdown', outside); document.removeEventListener('focusin', outside); document.removeEventListener('keydown', key); };
  }, [open]);
  const toggle = () => {
    // Open upward when the list would run past the bottom of the window.
    const r = button.current?.getBoundingClientRect();
    setUp(!!r && typeof window.innerHeight === 'number' && r.bottom + 40 * items.length + 16 > window.innerHeight && r.top > 40 * items.length);
    setOpen(o => !o);
  };
  const choose = (run: () => void) => { setOpen(false); button.current?.focus(); run(); };
  return <div className={`menu-wrap ${className ?? ''}`} ref={wrap}>
    <button ref={button} type="button" className={trigger === 'more' ? 'icon-btn' : trigger === 'text' ? 'btn-secondary' : undefined} aria-label={trigger === 'more' ? label : undefined} title={trigger === 'more' ? label : undefined}
      aria-haspopup="menu" aria-expanded={open} aria-controls={open ? id : undefined} onClick={toggle}>
      {trigger === 'more' ? <MoreHorizontal size={18} aria-hidden="true" /> : <>{trigger === 'primary' && <Plus size={16} aria-hidden="true" />}{label}<ChevronDown size={16} aria-hidden="true" /></>}
    </button>
    {open && <div className={['menu', align === 'left' ? 'left' : '', up ? 'up' : ''].filter(Boolean).join(' ')} role="menu" id={id} aria-label={label}>
      {items.map((item, i) => item === 'separator' ? <div key={i} className="menu-sep" role="separator" /> : 'href' in item
        ? <a key={i} role="menuitem" className={item.danger ? 'menu-item danger' : 'menu-item'} href={item.href} {...(item.newTab ? { target: '_blank', rel: 'noreferrer' } : {})} onClick={() => setOpen(false)}>{item.icon}{item.label}</a>
        : <button key={i} type="button" role="menuitem" className={item.danger ? 'menu-item danger' : 'menu-item'} disabled={item.disabled} onClick={() => choose(item.onSelect)}>{item.icon}{item.label}</button>)}
    </div>}
  </div>;
}
