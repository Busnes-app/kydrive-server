import React, { useState } from 'react';
import { Key, LogIn } from 'lucide-react';
import { Alert } from '../components/Alert';
import { CaptchaWidget } from '../components/CaptchaWidget';
import { ThemeSwitcher } from '../components/ThemeSwitcher';

interface LoginProps {
  onSuccess: (user: any) => void;
  appName: string;
  /** Only a configured provider gets the divider and SSO button. */
  sso?: boolean;
}

export const Login: React.FC<LoginProps> = ({ onSuccess, appName, sso }) => {
  const [username, setUsername] = useState<string>('');
  const [password, setPassword] = useState<string>('');
  const [captchaToken, setCaptchaToken] = useState<string>('');
  // A solution is single-use server-side: each attempt mounts a fresh puzzle.
  const [captchaRound, setCaptchaRound] = useState<number>(0);
  const [loading, setLoading] = useState<boolean>(false);
  const [error, setError] = useState<string>('');

  const handleLogin = async (e: React.FormEvent) => {
    e.preventDefault();
    setError('');
    setLoading(true);

    try {
      const resp = await fetch('/api/auth/login', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          username,
          password,
          captcha_token: captchaToken || undefined,
        }),
      });
      setCaptchaToken('');
      setCaptchaRound((n) => n + 1);

      const data = await resp.json();
      if (!resp.ok) {
        throw new Error(data.error || 'Login failed');
      }

      if (data.authenticated) {
        onSuccess(data.user);
      }
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Login failed');
    } finally {
      setLoading(false);
    }
  };

  return (
    <main className="ky-auth">
      <div className="ky-auth-theme"><ThemeSwitcher /></div>
      <div className="ky-auth-body">
        <div className="ky-auth-brand">
          <img className="brand-mark" src="/app-icon.png" width={56} height={56} alt="" />
          <h1>{appName || 'KyDrive'}</h1>
          <p>Organization files and shared workspaces.</p>
        </div>
        <div className="ky-auth-card">
          <form onSubmit={handleLogin}>
            {error && <Alert>{error}</Alert>}
            <div className="ky-auth-field">
              <label htmlFor="login-username">Username</label>
              <input id="login-username" type="text" autoFocus autoComplete="username" autoCapitalize="none" spellCheck={false} value={username} onChange={(e) => setUsername(e.target.value)} required />
            </div>
            <div className="ky-auth-field">
              <label htmlFor="login-password">Password</label>
              <input id="login-password" type="password" autoComplete="current-password" value={password} onChange={(e) => setPassword(e.target.value)} required />
            </div>
            <CaptchaWidget key={captchaRound} onToken={setCaptchaToken} />
            <button type="submit" className="ky-auth-submit" disabled={loading}>
              <LogIn aria-hidden="true" />
              <span>{loading ? 'Signing in…' : 'Sign in'}</span>
            </button>
            {sso && <>
              <hr className="ky-auth-divider" />
              <a href="/api/sso/kysignon/login" className="ky-auth-sso">
                <Key aria-hidden="true" />
                <span>Continue with single sign-on</span>
              </a>
            </>}
          </form>
        </div>
      </div>
    </main>
  );
};
