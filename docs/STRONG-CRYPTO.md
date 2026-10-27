# Key strength and the Go TLS profile

[Security policy](../SECURITY.md) · [Recovery and encryption](RECOVERY-AND-ENCRYPTION.md) · [Assurance case](ASSURANCE-CASE.md)

This guide covers the keys KyDrive's own code generates, accepts and verifies, and
the optional process setting that restricts its outbound TLS to strong certificate
keys. It is not a FIPS certification and does not certify a deployment. Stock
upstream components shipped beside KyDrive keep their own policies; see
[Out of scope](#out-of-scope).

## Preserve existing data and authority

Keep the database, `encryption.key` (or `KY_ENCRYPTION_KEY`), `.env.runtime`,
the recovery public-key pin, sealed KyRecovery token, local capsules, the bulk
repository and custodian cards. Keep the same data, backup and bulk volumes and
the same Compose image/build, DNS and network overlays. A refused key is a reason
to replace that one credential, never to regenerate the deployment key, rotate
the recovery pin or delete accounts.

## Key rules in every mode

| Boundary | Default | Smaller keys |
| --- | --- | --- |
| Deployment key (blobs, names, TOTP rows, search index, Restic password) | 32 random bytes in `data/encryption.key`, or `KY_ENCRYPTION_KEY` | Refused: the key must decode to exactly 32 bytes; a wrong-length file is refused, never overwritten. Blobs use age v1 with AES-256-GCM-wrapped file keys; names and TOTP rows use AES-256-GCM under HKDF-SHA256 subkeys. |
| Session secret (CSRF and proof-of-work HMAC-SHA256) | 32 random bytes (development); production requires an explicit value | Refused at startup below 16 bytes (`KY_SESSION_SECRET`). Use `openssl rand -hex 32`. |
| Directory webhook HMAC-SHA256 | Unset (webhook off) | `KY_KYSIGNON_HMAC_SECRET` below 16 bytes is refused at startup, and the webhook refuses to verify with one before touching any account. |
| Euro-Office JWT (HS256) | Operator-supplied | `KYDRIVE_EDITOR_SECRET` below 32 bytes is refused; the editor Compose overlay refuses the same. |
| OIDC ID tokens | Verified by `coreos/go-oidc` against the provider's JWKS | Every RSA key in the fetched JWKS must have at least 2048 modulus bits, or the whole set is refused and sign-in fails. The check reads JWK members the way the verifier does. EC/Ed25519 key handling stays with go-jose. |
| TOTP | 20 random bytes (HMAC-SHA1, RFC 6238) | Authentication requires 16 decoded bytes ([existing accounts](../SECURITY.md#totp-key-minimum-and-existing-accounts)). HMAC-SHA1 is a keyed PRF here; no collision resistance is relied on. |
| Recovery capsules | Sealed by `ky-primitives/recoveryclient` to the pinned fixed-suite public key | No size is selectable; the suite is fixed. |
| Generated tokens | Session, service, device, sharing-link and guest tokens 256 random bits; app passwords and agent tokens 160; stored as SHA-256 hashes | Not operator-supplied. |

`KY_SCIM_TOKEN` (default 24 random bytes) and `KY_KYSIGNON_SECRET` are bearer
credentials presented to or by a peer, not keys KyDrive computes with; generate
them with `openssl rand -hex 32`. Changing `KY_SESSION_SECRET` only invalidates
outstanding CSRF tokens (re-issued by the next `/api/auth/me`) and proof-of-work
challenges; sessions survive. A refused OIDC key needs rotation at the provider;
nothing in KyDrive changes.

## Enable the Go TLS profile

Set `GODEBUG=fips140=on` for the server and every CLI subcommand that contacts a
peer. Use `on`; KyDrive is not tested under the diagnostic `only` mode. With
it, Go's TLS client accepts only certificate chains whose
every certificate, including a root you trust explicitly, uses RSA-2048+, ECDSA
P-256/P-384/P-521 or Ed25519 keys. This covers each TLS client in KyDrive's code:

- OIDC discovery, token exchange and JWKS (`internal/sso`). The profile also
  requires HTTPS issuer, authorization, token and JWKS URLs without URL credentials,
  and refuses discovery and token redirects.
- The Euro-Office API (`internal/editor`), HTTPS outside `KY_ENV=development`.
- KyRecovery deposits and restore (`ky-primitives/recoveryclient`), always HTTPS.

Certificate and hostname verification stay on. A private CA does not waive the
floor: replace a weak chain, never disable verification.

For Compose, append `docker-compose.strong-crypto.yml` to the chain you already
use (keep the build, LAN-DNS, static-IP and office overlays):

```sh
docker compose -f docker-compose.yml [your overlays] -f docker-compose.strong-crypto.yml up -d
```

or add it to `COMPOSE_FILE` in `.env`. The overlay sets only `GODEBUG` on the
`app` service. Its value replaces a `GODEBUG` from `.env.runtime`; if you need other
entries, put them all in one value with `fips140=on`. For a native process or
systemd unit, add `GODEBUG=fips140=on` to its environment and restart; a shell
export does not reach a running service.

`python3 docs/check-strong-crypto-compose.py` renders six chains (base, each
overlay alone, all overlays) with synthetic inputs, starts nothing, and fails if
the overlay changes anything but that one variable.

## Configure the independent HTTPS edge

KyDrive serves plain HTTP for its proxy. Bind it to the proxy only (Compose binds
`127.0.0.1` by default) and set `KY_TRUSTED_PROXIES` to the proxy's address. Use
TLS 1.2/1.3 and a verified RSA-2048+ or P-256+ certificate chain. For Nginx/OpenSSL:

```nginx
ssl_protocols TLSv1.2 TLSv1.3;
ssl_ciphers 'DEFAULT@SECLEVEL=2';
```

`GODEBUG` does not configure the proxy, browsers, WebDAV clients or the desktop
client. Check the edge before rollout.

## Out of scope

These ship beside KyDrive or are reached by it, but are stock upstream software
with their own key and TLS policy. This guide makes no claim about them:

- **Euro-Office Document Server** (`docker-compose.office.yml`, Kubernetes): its
  Node services and native converter make their own TLS connections, including
  downloads from and callbacks to KyDrive.
- **Restic** (the image's Alpine package): bulk backups to `s3:https://` or
  `rest:https://` use Restic's TLS client and repository crypto. It inherits
  `GODEBUG`, but its behavior under it is Restic's.
- **Reverse proxies** in front of KyDrive and Euro-Office, and their certificates.
- **Browsers, WebDAV clients and the desktop client.**
- **PostgreSQL**: not a supported KyDrive deployment; SQLite is.
- **KyIdentity, KyRecovery** and other suite services: each has its own guide.

## Verification

CI runs the full race suite twice, plainly and with `GODEBUG=fips140=on`, then
the Compose check. The regressions cover:

- `internal/config/key_strength_test.go`: 1/15-byte session and webhook secrets
  refused, 16/32 accepted, the deployment key file unchanged.
- `internal/sso/strong_crypto_test.go`: real RS256 ID tokens from 1024/2047-bit
  keys refused and 2048 accepted (with JWK casing variants, cache and nonce
  checks); a short webhook secret creates no account; plaintext and redirected
  OIDC refused in the profile; a P-256 leaf under a 1024/2047-bit trusted root
  refused in the profile, accepted otherwise, and a 2048-bit root accepted.
- `internal/sso/jwks_strength_test.go`: JWKS size, redirect and malformed input.
- `internal/auth/totp_strength_test.go`: the TOTP floor.

Local tests use generated keys and local servers; they do not prove a live
provider, editor or proxy. After rollout check sign-in, the editor, a deposit and
a restore drill.

References: [Go FIPS 140-3 mode](https://go.dev/doc/security/fips140),
[RFC 4226 key requirements](https://www.rfc-editor.org/rfc/rfc4226#section-4),
[NIST SP 800-131A Rev. 2](https://nvlpubs.nist.gov/nistpubs/SpecialPublications/NIST.SP.800-131Ar2.pdf).
