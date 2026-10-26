import { useEffect, useState } from 'react';
import { THEME_OPTIONS, applyTheme, storedTheme } from '../theme';

/** Compact shortcut to the Appearance choice, over the same catalog and saved value. */
export function ThemeSwitcher() {
  const [theme, setTheme] = useState(storedTheme);
  useEffect(() => {
    const sync = () => setTheme(storedTheme());
    window.addEventListener('ky:theme', sync);
    return () => window.removeEventListener('ky:theme', sync);
  }, []);
  return <select className="theme-selector" aria-label="Color theme" value={theme.id} onChange={event => {
    const next = THEME_OPTIONS.find(option => option.id === event.target.value);
    if (next) applyTheme(next, true);
  }}>{THEME_OPTIONS.map(option => <option key={option.id} value={option.id}>{option.name}</option>)}</select>;
}
