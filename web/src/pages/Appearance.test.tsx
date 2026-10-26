import { afterEach, describe, expect, it } from 'vitest';
import { cleanup, fireEvent, render, screen } from '@testing-library/react';
import { Appearance } from './Appearance';
import { applyTheme, storedTheme, THEME_OPTIONS } from '../theme';
import { findTheme } from '../ky-ui/themes';

const root = document.documentElement;
afterEach(() => { cleanup(); localStorage.clear(); root.removeAttribute('style'); });

describe('Appearance', () => {
  it('offers Match system and the suite catalog as radios and saves the catalog ID', () => {
    render(<Appearance />);
    expect(screen.getAllByRole('radio')).toHaveLength(THEME_OPTIONS.length);
    fireEvent.click(screen.getByRole('radio', { name: /^Ocean/ }));
    expect(localStorage.getItem('ky_theme')).toContain('ocean');
    expect(root.dataset.theme).toBe('ocean');
    expect(root.style.getPropertyValue('--bg')).toBe('#0f1b24');
    expect(screen.getByRole('radio', { name: /^Ocean/ })).toHaveProperty('checked', true);
  });

  it('keeps the shipped values of themes saved before the catalog', () => {
    for (const id of ['patina', 'cyber', 'nord', 'paper', 'oled']) {
      localStorage.setItem('ky_theme', id);
      expect(storedTheme().id).toBe(id);
      applyTheme(findTheme('ocean')!);
      applyTheme(storedTheme());
      // styles/theme.css [data-theme] supplies the colours; nothing inline overrides them.
      expect(root.dataset.theme).toBe(id);
      expect(root.style.getPropertyValue('--bg')).toBe('');
      expect(root.style.getPropertyValue('--accent')).toBe('');
    }
  });

  it('follows the system until a choice is saved', () => {
    expect(storedTheme().id).toBe('system');
    applyTheme(storedTheme());
    expect(['busnes-light', 'busnes-dark']).toContain(root.dataset.theme);
  });
});
