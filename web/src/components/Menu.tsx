import { useRef } from 'react';
import type { ReactNode } from 'react';
import { Menu as Base } from '@base-ui/react/menu';
import { ChevronDown, MoreHorizontal, Plus } from 'lucide-react';

export type MenuEntry = { label: string; icon?: ReactNode; danger?: boolean; disabled?: boolean } & ({ onSelect: () => void } | { href: string; newTab?: boolean }) | 'separator';

// Menu is a button that opens a list of actions beside it, on Base UI's menu (keyboard,
// focus return, outside click, collision flipping). It closes after any choice.
export function Menu({ label, items, trigger = 'more', className }: { label: string; items: MenuEntry[]; trigger?: 'more' | 'text' | 'primary'; className?: string }) {
  const button = useRef<HTMLButtonElement>(null);
  const more = trigger === 'more';
  return <div className={`menu-wrap ${className ?? ''}`}>
    <Base.Root modal={false}>
      <Base.Trigger ref={button} className={more ? 'icon-btn' : trigger === 'text' ? 'btn-secondary' : undefined} aria-label={more ? label : undefined} title={more ? label : undefined}>
        {more ? <MoreHorizontal size={18} aria-hidden="true" /> : <>{trigger === 'primary' && <Plus size={16} aria-hidden="true" />}{label}<ChevronDown size={16} aria-hidden="true" /></>}
      </Base.Trigger>
      <Base.Portal>
        <Base.Positioner className="menu-positioner" align="end" sideOffset={4}>
          <Base.Popup className="menu" aria-label={label}>
            {items.map((item, i) => {
              if (item === 'separator') return <Base.Separator key={i} className="menu-sep" />;
              const cls = item.danger ? 'menu-item danger' : 'menu-item';
              if ('href' in item) return <Base.LinkItem key={i} className={cls} href={item.href} closeOnClick {...(item.newTab ? { target: '_blank', rel: 'noreferrer' } : {})}>{item.icon}{item.label}</Base.LinkItem>;
              // Focus the button before the action, so a dialog it opens returns focus there.
              return <Base.Item key={i} className={cls} disabled={item.disabled} onClick={() => { button.current?.focus(); item.onSelect(); }}>{item.icon}{item.label}</Base.Item>;
            })}
          </Base.Popup>
        </Base.Positioner>
      </Base.Portal>
    </Base.Root>
  </div>;
}
