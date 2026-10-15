# Auth

## Purpose
Manages user authentication, password policies, Multi-Factor Authentication (RFC 6238 TOTP), recovery codes, session token lifecycle, and anti-abuse CAPTCHA (Proof-of-Work default).

## Ownership
Owns session issuance and verification, TOTP token generation/validation, recovery code redemption, and client-side PoW verification.

## Local Contracts
- `RequestNetwork` reports `ClientIP`, whether the transport peer is trusted and the nearest proxy's forwarded scheme. Untrusted peers cannot supply either address or scheme; `/healthz` uses this for installer G11 verification.
- Sessions for `must_change_password` users authenticate only GET `/api/auth/me` and POST `/api/auth/change-password` or `/api/auth/logout`; private settings and product actions fail closed. Session issuance is bound to the verified password hash and serialized with replacement.
- Passwords require at least 12 characters (`ValidatePassword`).
- Expired sessions and MFA challenges are deleted hourly by `cmd/server` (`CleanExpiredSessions`, `CleanExpiredMFAChallenges`).
- Active sessions are stored with SHA-256 hashed tokens and verified only from the secure HttpOnly / SameSite session cookie; a session token in an `Authorization` header authenticates nothing.
- HTTP Basic credentials authenticate only `/dav/`, and only as an app password (`drive.AppPasswordUser`); nowhere else reads them.
- Session authentication rejects inactive accounts and deletes their presented session.
- PoW solutions must carry unexpired server-signed challenge metadata and are single-use: `PoWSpent` keeps verified salts in memory until their challenge expires (single-instance server, like the login limiter). `KY_CAPTCHA_POW_DIFFICULTY` (default 100000) is the challenge's `maxnumber`; the browser searches linearly with WebCrypto, about 50 000 SHA-256 calls on average, roughly 0.25-1 s.
- The CSRF token is `HMAC-SHA256(KY_SESSION_SECRET, "ky-csrf:" + session token)`, set as a readable cookie at issuance and re-set by `/api/auth/me`; `ValidateCSRF` recomputes it from the session cookie and compares the header, so a planted CSRF cookie is worthless. With `CookieSecure` and no `KY_COOKIE_DOMAIN`, both cookies carry the `__Host-` prefix.
- Password verification creates a five-minute opaque MFA transaction; MFA endpoints never accept a user ID and consume the transaction once.
- Single-use recovery codes are invalidated immediately upon redemption.
- TOTP secrets are sealed by `SealTOTPSecret`: AES-GCM under `DeriveKey(deployment key, "kydrive:totp")` with AAD `users.totp_secret_enc:<user ID>`, so a ciphertext copied to another row fails. `OpenTOTPSecret` also opens the legacy raw-key, no-AAD form and reports it; `internal/api` reseals such a row on its next use. Until then a legacy row is not row-bound.
- `ClientIP` is the one client-address helper: the rate limiter and session binding both use it. `X-Forwarded-For` is honoured only when the peer is in `KY_TRUSTED_PROXIES`, walking the chain from the right past trusted hops; `X-Real-IP` is ignored. Each entry may be a bare IP or `ip:port` (Azure App Service/Application Gateway append the port); anything else ends the walk and falls back to the peer.

## Verification
- `go test -v ./internal/auth/...`

## Child DOX Index
None.
