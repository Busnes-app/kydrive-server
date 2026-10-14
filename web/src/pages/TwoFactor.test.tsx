import { afterEach, expect, it, vi } from 'vitest';
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { TwoFactor } from './TwoFactor';

afterEach(() => { cleanup(); vi.unstubAllGlobals(); document.cookie = 'ky_csrf=; Max-Age=0'; });

function stub(routes: Record<string, () => Response>) {
  const fetcher = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    expect(init?.method).toBe('POST');
    expect(new Headers(init?.headers).get('X-CSRF-Token')).toBe('csrf-test');
    return routes[String(input)]();
  });
  vi.stubGlobal('fetch', fetcher); document.cookie = 'ky_csrf=csrf-test';
  return fetcher;
}

it('enrols, confirms and shows the recovery codes once', async () => {
  const confirmations = [
    () => Response.json({ error: 'Invalid TOTP verification code' }, { status: 401 }),
    () => Response.json({ totp_enabled: true, recovery_codes: ['aaaa-bbbb', 'cccc-dddd'] }),
  ];
  const fetcher = stub({
    '/api/auth/mfa/enroll': () => Response.json({ secret: 'JBSWY3DPEHPK3PXP', otpauth_uri: 'otpauth://totp/KyDrive:mia?secret=JBSWY3DPEHPK3PXP' }),
    '/api/auth/mfa/enroll/confirm': () => confirmations.shift()!(),
  });
  const changed = vi.fn();
  render(<TwoFactor enabled={false} local onChange={changed} />);

  fireEvent.click(screen.getByRole('button', { name: 'Turn on two-factor authentication' }));
  expect(await screen.findByText('JBSWY3DPEHPK3PXP')).toBeTruthy();
  expect(screen.getByRole('link', { name: 'Open in authenticator app' }).getAttribute('href')).toContain('otpauth://totp/');

  fireEvent.change(screen.getByLabelText('Code'), { target: { value: '000000' } });
  fireEvent.click(screen.getByRole('button', { name: 'Confirm' }));
  expect((await screen.findByRole('alert')).textContent).toContain('Invalid TOTP verification code');
  expect(changed).not.toHaveBeenCalled();

  fireEvent.change(screen.getByLabelText('Code'), { target: { value: ' 123456 ' } });
  fireEvent.click(screen.getByRole('button', { name: 'Confirm' }));
  expect((await screen.findByRole('status')).textContent).toContain('Save these recovery codes now');
  expect(screen.getByText('aaaa-bbbb')).toBeTruthy();
  expect(changed).toHaveBeenCalledWith(true);
  expect(fetcher).toHaveBeenLastCalledWith('/api/auth/mfa/enroll/confirm', expect.objectContaining({ body: JSON.stringify({ code: '123456' }) }));

  fireEvent.click(screen.getByRole('button', { name: 'I have saved them' }));
  expect(screen.queryByText('aaaa-bbbb')).toBeNull();
});

it('turns off with the password and a code', async () => {
  const fetcher = stub({ '/api/auth/mfa/disable': () => Response.json({ totp_enabled: false }) });
  const changed = vi.fn();
  render(<TwoFactor enabled local onChange={changed} />);
  fireEvent.change(screen.getByLabelText('Password'), { target: { value: 'SuperSecretPass123!' } });
  fireEvent.change(screen.getByLabelText('Authenticator or recovery code'), { target: { value: 'aaaa-bbbb' } });
  fireEvent.click(screen.getByRole('button', { name: 'Turn off two-factor authentication' }));
  await waitFor(() => expect(changed).toHaveBeenCalledWith(false));
  expect(fetcher).toHaveBeenCalledWith('/api/auth/mfa/disable', expect.objectContaining({ body: JSON.stringify({ password: 'SuperSecretPass123!', code: 'aaaa-bbbb' }) }));
});

it('leaves directory accounts to their identity provider', () => {
  render(<TwoFactor enabled={false} local={false} onChange={vi.fn()} />);
  expect(screen.queryByRole('button')).toBeNull();
  expect(screen.getByText(/identity provider manages/)).toBeTruthy();
});
