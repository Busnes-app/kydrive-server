import { readChoice, saveChoice, watchChoice } from './ky-ui/theme';
import { SYSTEM_THEME, THEME_CATALOG, applyThemeVariables, findTheme } from './ky-ui/themes';
import type { CatalogTheme, SystemTheme } from './ky-ui/themes';

// Busnes.app defaults; explicit choices are local to this browser and saved by catalog ID.
export type Theme = CatalogTheme | SystemTheme;
export const THEME_OPTIONS: readonly Theme[] = [SYSTEM_THEME, ...THEME_CATALOG];
// Themes KyDrive shipped before the suite catalog keep the exact values in styles/theme.css.
const LEGACY = new Set(['patina', 'cyber', 'nord', 'paper', 'oled']);
const KEY = 'ky_theme';

export function storedTheme(): Theme {
  try { return findTheme(readChoice(KEY)) ?? SYSTEM_THEME; }
  catch { return SYSTEM_THEME; }
}
let current = storedTheme();
const media = window.matchMedia?.('(prefers-color-scheme: dark)');
export function applyTheme(theme: Theme, persist = false) {
  current = theme;
  const root = document.documentElement;
  const named = applyThemeVariables(LEGACY.has(theme.id) ? SYSTEM_THEME.id : theme.id, root);
  const resolved = theme.id === 'system' ? (media?.matches ? 'busnes-dark' : 'busnes-light') : theme.id;
  root.dataset.theme = resolved;
  root.style.colorScheme = named ?? (theme.scheme === 'system' ? (media?.matches ? 'dark' : 'light') : theme.scheme);
  // The base :root warning is a dark-theme amber; light named themes take Paper's.
  if (named === 'light') root.style.setProperty('--warning', '#a16207');
  else root.style.removeProperty('--warning');
  if (persist) try { saveChoice(KEY, theme.id); } catch { /* Storage is optional. */ }
  window.dispatchEvent(new Event('ky:theme'));
}
applyTheme(current);
watchChoice(KEY, () => applyTheme(storedTheme()), () => current.id !== 'system');
