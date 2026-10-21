import React, { useState } from 'react';
import { Key, LogIn } from 'lucide-react';
import { Alert } from '../components/Alert';
import { CaptchaWidget } from '../components/CaptchaWidget';
import { ThemeSwitcher } from '../components/ThemeSwitcher';

interface LoginProps {
  onSuccess: (user: any) => void;
  appName: string;
}

interface MFAChallenge {
  mfa_token: string;
}

export const Login: React.FC<LoginProps> = ({ onSuccess, appName }) => {
  const [username, setUsername] = useState<string>('');
  const [password, setPassword] = useState<string>('');
  const [captchaToken, setCaptchaToken] = useState<string>('');
  // A solution is single-use server-side: each attempt mounts a fresh puzzle.
  const [captchaRound, setCaptchaRound] = useState<number>(0);
  const [mfaChallenge, setMfaChallenge] = useState<MFAChallenge | null>(null);
  const [mfaCode, setMfaCode] = useState<string>('');
  const [isRecoveryCode, setIsRecoveryCode] = useState<boolean>(false);
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

      if (data.mfa_required) {
        setMfaChallenge(data);
        return;
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

  const handleMFASubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!mfaChallenge) return;
    setError('');
    setLoading(true);

    try {
      const endpoint = isRecoveryCode ? '/api/auth/mfa/recovery-code' : '/api/auth/mfa/totp';
      const resp = await fetch(endpoint, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          mfa_token: mfaChallenge.mfa_token,
          code: mfaCode.trim(),
        }),
      });

      const data = await resp.json();
      if (!resp.ok) {
        throw new Error(data.error || 'MFA verification failed');
      }

      if (data.authenticated) {
        onSuccess(data.user);
      }
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Invalid verification code');
    } finally {
      setLoading(false);
    }
  };

  return (
    <div className="auth-page">
      <div className="auth-theme"><ThemeSwitcher /></div>
      <main className="auth-center">
        <div className="auth-card">
          <div className="auth-brand">
            <img src="/app-icon.png" width={56} height={56} alt="" style={{ borderRadius: 14 }} />
            <h1>{appName || 'Busnes.app'}</h1>
            <p>Organization files and shared workspaces</p>
          </div>
          <div className="panel">
            {mfaChallenge ? (
              <form onSubmit={handleMFASubmit}>
                <div>
                  <h2 style={{ fontSize: '1.1rem', marginBottom: '.25rem' }}>Two-Factor Verification</h2>
                  <p className="hint">{isRecoveryCode ? 'Enter one of your single-use recovery codes.' : 'Enter the 6-digit code from your authenticator app.'}</p>
                </div>
                {error && <Alert>{error}</Alert>}
                <label className="field">
                  {isRecoveryCode ? 'Recovery Code' : 'Authenticator Code'}
                  <input type="text" className="otp-input" autoFocus autoComplete="one-time-code" inputMode={isRecoveryCode ? 'text' : 'numeric'} value={mfaCode} onChange={(e) => setMfaCode(e.target.value)} placeholder={isRecoveryCode ? 'XXXX-XXXX' : '123456'} required />
                </label>
                <button type="submit" disabled={loading}>{loading ? 'Verifying...' : 'Verify & Sign In'}</button>
                <button type="button" className="btn-ghost btn-sm" onClick={() => setIsRecoveryCode(!isRecoveryCode)}>
                  {isRecoveryCode ? 'Use Authenticator App' : 'Use a Recovery Code'}
                </button>
              </form>
            ) : (
              <form onSubmit={handleLogin}>
                {error && <Alert>{error}</Alert>}
                <label className="field">
                  Username
                  <input type="text" autoFocus autoComplete="username" value={username} onChange={(e) => setUsername(e.target.value)} placeholder="admin" required />
                </label>
                <label className="field">
                  Password
                  <input type="password" autoComplete="current-password" value={password} onChange={(e) => setPassword(e.target.value)} placeholder="••••••••••••" required />
                </label>
                <CaptchaWidget key={captchaRound} onToken={setCaptchaToken} />
                <button type="submit" disabled={loading}>
                  <LogIn size={16} aria-hidden="true" />
                  <span>{loading ? 'Signing in...' : 'Sign In'}</span>
                </button>
                <div className="auth-divider">
                  <span>Or continue with Single Sign-On</span>
                  <a href="/api/sso/kysignon/login" className="btn btn-secondary">
                    <Key size={16} aria-hidden="true" />
                    <span>Sign On with KyIdentity</span>
                  </a>
                </div>
              </form>
            )}
          </div>
        </div>
      </main>
    </div>
  );
};
