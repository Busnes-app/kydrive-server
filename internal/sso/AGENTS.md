# SSO

## Purpose
Provides KySignOn (KyIdentity) OIDC sign-on, back-channel logout token verification and its signed directory-sync webhook.

## Ownership
Owns the application adapters around OAuth/OIDC login and KySignOn HMAC-SHA256 signed directory sync webhooks.

## Local Contracts
- `KySignOnClient.HandleSyncWebhook` verifies HMAC-SHA256 signatures before modifying local user state.
- PKCE with `S256` is enforced on all OAuth/OIDC authorization requests.
- ID tokens require provider signature, issuer, audience, expiry, and one-time nonce verification before claims are trusted.
- OAuth discovery, authorization URLs, PKCE parameters, code exchange, and token verification are delegated to `golang.org/x/oauth2` and `coreos/go-oidc`; application code only maps verified claims.
- There is no generic OIDC client and no SAML service provider; add one only with a working callback/ACS route.
- Directory webhook timestamps are accepted only within five minutes. Each event applies only when newer than the account's last applied event (`users.directory_ts`, compared in the UPDATE/DELETE); a same-second tie goes to the restrictive event (deactivate and delete apply, reactivation does not). An older out-of-order event, or a same-second update that would not restrict, is acknowledged (200) without effect.
- The webhook never sets the application role: a payload `role` is ignored, new accounts are `user`, and an operator grant survives updates. It writes only through `store.ApplyDirectoryUser`/`DeleteDirectoryUser` (username validation, session/MFA revocation on disable in the same transaction) and resolves subjects with `store.DirectorySubject` (KySignOn row, then SCIM row) like the sign-on callback, so it updates a SCIM-provisioned account instead of duplicating it.
- `ErrWebhookUnauthorized` (secret, signature, timestamp) maps to 401, `ErrWebhookMalformed` to 400, anything else to 500 with the detail logged, so the sender retries outages only. Deleted accounts leave no tombstone: a late event older than the deletion can recreate one.

- `VerifyLogoutToken` (`backchannel.go`) reuses the ID-token verifier, then requires
  `iat`, `sub` or `sid`, the back-channel logout event as a JSON object and no `nonce`;
  a present `typ` must be `logout+jwt` or `application/logout+jwt`. Every failure is
  `ErrInvalidLogoutToken`. `IdentityClaims.SessionID` carries the ID token `sid` so
  sessions can be ended by it. KySignOn is the only provider, so a `sid` is never
  ambiguous between issuers.
- KyDrive links verified OIDC `sub` to SCIM `externalId`, never email/username. Disabled accounts cannot receive a session. The base-named KySignOn callback can point at the actual KyIdentity issuer.

## Verification
- `go test -v ./internal/sso/...` (`TestVerifyLogoutToken` holds one rejection case per logout-token rule)

## Child DOX Index
None.
