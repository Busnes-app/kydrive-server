# SSO

## Purpose
Provides KySignOn (KyIdentity) OIDC sign-on, back-channel logout token verification and its signed directory-sync webhook.

## Ownership
Owns the application adapters around OAuth/OIDC login and KyIdentity's `syncauth`-signed directory sync webhook.

## Local Contracts
- `KySignOnClient.HandleSyncWebhook` is the receiver for KyIdentity's suite webhook (`suite_webhook` pairing). It refuses secrets below `crypto.MinHMACKeyBytes` and verifies with ky-primitives `syncauth.Verify` (the `X-KySignOn-*` headers: `v1=` HMAC-SHA256 over timestamp, event type, event ID and body hash) before parsing or modifying local user state. A bare body HMAC, the format before syncauth, is refused.
- The verifier fetches JWKS through `strongJWKSTransport`: 1 MiB cap, no redirects, every RSA modulus at least 2048 bits, parsed with go-oidc's envelope and go-jose's case-sensitive members so it checks the key the verifier uses. Provider and verifier are built together under the discovery mutex; go-oidc keeps caching and rotation.
- With `GODEBUG=fips140=on`, issuer, authorization, token and JWKS URLs must be HTTPS without credentials and discovery/token redirects are refused; the caller's `oauth2.HTTPClient` (roots, transport) is kept. `docs/STRONG-CRYPTO.md` owns the profile.
- PKCE with `S256` is enforced on all OAuth/OIDC authorization requests.
- `IdentityClaims.ExportOnly` reads the boolean `export_only` only from the verified ID token. The API ignores it on single-tenant installs; on multi-tenant installs it can issue only a separate export session, never an ordinary one or a step-up.
- ID tokens require provider signature, issuer, audience, expiry, and one-time nonce verification before claims are trusted.
- OAuth discovery, authorization URLs, PKCE parameters, code exchange, and token verification are delegated to `golang.org/x/oauth2` and `coreos/go-oidc`; application code only maps verified claims.
- There is no generic OIDC client and no SAML service provider; add one only with a working callback/ACS route.
- The body is KyIdentity's SCIM user resource; the event type comes from the signed header. `user.created` and `user.updated` apply `active` (KyIdentity deactivates with `user.updated`, `active: false`), `user.deleted` deletes on a single-tenant install and, on a multi-tenant one (where it means "disable"), disables through `ApplyDirectoryUser`, keeping the row and its revision; `user.erased` (Delete everything, multi-tenant only; acknowledged without effect otherwise) calls `EraseDirectoryUser`, and an update refused with `ErrErased` is acknowledged; `user.mfa_reset` ends the account's sessions and every credential it issued but leaves it active (`RevokeDirectoryCredentials`, whatever the revision: revoking is restrictive and repeatable), first committing a subject fence (`store.SSOLogoutFence`, cutoff now + 1s) so a callback holding a pre-reset ID token answers 401; for a subject with no account yet it writes only that fence (`EndSSOSubject`), so a first login in flight cannot finish either, and other types are acknowledged without effect. Timestamps are accepted only within five minutes (`syncauth.DefaultWindow`). Each event applies only when its revision, `meta.version` `W/"N"` with N positive, is newer than the account's last applied one (`users.directory_rev`, compared in the UPDATE/DELETE). KyIdentity re-signs a retry with a new timestamp but the same revision, so ordering never uses time. An equal revision goes to the restrictive event (deactivate and delete apply, reactivation does not). An older event, or an equal one that would not restrict, is acknowledged (200) without effect. A missing or non-positive revision is malformed.
- The webhook never sets the application role: a payload `role` is ignored, new accounts are `user`, and an operator grant survives updates. It writes only through `store.ApplyDirectoryUser`/`DeleteDirectoryUser` (username validation, session/MFA revocation on disable in the same transaction) and resolves subjects with `store.DirectorySubject` (KySignOn row, then SCIM row) like the sign-on callback, so it updates a SCIM-provisioned account instead of duplicating it.
- Multi-tenant (`store.Drive().Multi()`): the body's top-level `org_id` is required (else `ErrWebhookMalformed`). A known subject whose account is in another org is refused the same way and audited `tenancy.org_mismatch` (`entry=webhook`); `user.mfa_reset` applies whatever the org, since it only revokes. A new account takes the body's org; an org KyDrive has not registered yet fails the insert (500), so KyIdentity retries until the org poll lists it. `IdentityClaims.OrgID` and `LogoutClaims.OrgID` carry the tokens' `org_id`; single-tenant ignores all three.
- `ErrWebhookUnauthorized` (secret, signature, timestamp) maps to 401, `ErrWebhookMalformed` to 400, anything else to 500 with the detail logged, so the sender retries outages only. Single-tenant deleted accounts leave no tombstone, so a late event older than deletion can recreate one. Multi-tenant `user.deleted` keeps the account row; `user.erased` keeps the permanent `drive_erasures` record and cannot be recreated.

- `VerifyLogoutToken` (`backchannel.go`) reuses the ID-token verifier, then requires
  `iat`, `sub` or `sid`, the back-channel logout event as a JSON object and no `nonce`;
  a present `typ` must be `logout+jwt` or `application/logout+jwt`. Every failure is
  `ErrInvalidLogoutToken`. `IdentityClaims.SessionID` and `IssuedAt` carry the ID token
  `sid` and `iat` so sessions can be ended by them. KySignOn is the only provider, so a `sid` is never
  ambiguous between issuers.
- KyDrive links verified OIDC `sub` to SCIM `externalId`, never email/username. Disabled accounts cannot receive a session. The base-named KySignOn callback can point at the actual KyIdentity issuer.

## Verification
- `go test -v ./internal/sso/...`, and again with `GODEBUG=fips140=on`: `strong_crypto_test.go` signs real RS256 tokens at 1024/2047/2048 bits and checks a weak trusted TLS root against a strong-leaf control; `TestVerifyLogoutToken` holds one rejection case per logout-token rule.

## Child DOX Index
None.
