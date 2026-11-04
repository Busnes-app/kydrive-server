import { afterEach, describe, expect, it, vi } from 'vitest';
import { cleanup, fireEvent, render, screen } from '@testing-library/react';
import { OwnPassword } from './OwnPassword';

afterEach(() => { cleanup(); vi.unstubAllGlobals(); });
const json = (v: unknown, status = 200) => new Response(JSON.stringify(v), { status, headers: { 'Content-Type': 'application/json' } });

describe('OwnPassword', () => {
  it('changes a local password and returns to sign-in', async () => {
    const reload = vi.fn();
    vi.stubGlobal('location', { ...window.location, reload });
    const bodies: unknown[] = [];
    vi.stubGlobal('fetch', vi.fn(async (_: RequestInfo | URL, init?: RequestInit) => { bodies.push(JSON.parse(String(init?.body))); return json({ password_changed: true, login_required: true }); }));
    render(<OwnPassword local />);
    fireEvent.change(screen.getByLabelText('Current password'), { target: { value: 'OldPassword123!' } });
    fireEvent.change(screen.getByLabelText('New password'), { target: { value: 'NewPassword123!' } });
    fireEvent.change(screen.getByLabelText('Confirm new password'), { target: { value: 'NewPassword123!' } });
    fireEvent.click(screen.getByRole('button', { name: 'Change password' }));
    await vi.waitFor(() => expect(reload).toHaveBeenCalled());
    expect(bodies).toEqual([{ current_password: 'OldPassword123!', new_password: 'NewPassword123!' }]);
  });

  it('shows the server refusal and stays', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => json({ error: 'Current password is incorrect' }, 401)));
    render(<OwnPassword local />);
    fireEvent.change(screen.getByLabelText('Current password'), { target: { value: 'wrong' } });
    fireEvent.change(screen.getByLabelText('New password'), { target: { value: 'NewPassword123!' } });
    fireEvent.change(screen.getByLabelText('Confirm new password'), { target: { value: 'NewPassword123!' } });
    fireEvent.click(screen.getByRole('button', { name: 'Change password' }));
    expect(await screen.findByText('Current password is incorrect')).toBeTruthy();
  });

  it('points a KyIdentity account to KyIdentity', () => {
    render(<OwnPassword local={false} />);
    expect(screen.getByText('Your password is managed in KyIdentity.')).toBeTruthy();
    expect(screen.queryByRole('button', { name: 'Change password' })).toBeNull();
  });
});
