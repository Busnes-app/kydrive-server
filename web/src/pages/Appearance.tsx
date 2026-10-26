import { useEffect, useState } from 'react';
import { LEGACY, THEME_OPTIONS, applyTheme, storedTheme } from '../theme';

const mode = (scheme: string) => scheme === 'system' ? 'Light or dark' : scheme === 'light' ? 'Light' : 'Dark';

export function Appearance() {
  const [selected, setSelected] = useState(storedTheme);
  useEffect(() => {
    const sync = () => setSelected(storedTheme());
    window.addEventListener('ky:theme', sync);
    return () => window.removeEventListener('ky:theme', sync);
  }, []);
  return <section className="page">
    <header className="page-header"><h1>Appearance</h1></header>
    <p className="ky-appearance-description">Pick a theme. It applies straight away and is saved in this browser only. Match system follows your device’s light or dark setting.</p>
    <fieldset className="ky-appearance">
      <legend className="ky-appearance-legend">Theme</legend>
      <div className="ky-appearance-grid">
        {THEME_OPTIONS.map(theme => <label key={theme.id} className="ky-appearance-option">
          <input className="ky-appearance-radio" type="radio" name="kydrive-theme" value={theme.id} checked={theme.id === selected.id} onChange={() => { setSelected(theme); applyTheme(theme, true); }} />
          <span className="ky-appearance-preview" aria-hidden="true" data-ky-preview={theme.id} data-theme={LEGACY.has(theme.id) ? theme.id : undefined} />
          <span className="ky-appearance-name">{theme.name}</span>
          <small className="ky-appearance-mode">{mode(theme.scheme)}</small>
        </label>)}
      </div>
    </fieldset>
  </section>;
}
