# SCIM

## Purpose
Adapts the `elimity-com/scim` RFC 7643/7644 server to the local user and group stores for enterprise provisioning from Okta, Azure AD / Microsoft Entra ID, OneLogin, and KySignOn.

## Ownership
Owns local persistence adapters and bearer authentication; the library owns `/scim/v2/Users`, `/scim/v2/Groups`, discovery endpoints, schema validation, filter/PATCH parsing, pagination, and SCIM error serialization.

## Local Contracts
- Content-Type for all SCIM endpoints must be `application/scim+json`.
- Requests must be authenticated with the configured bearer token.
- User de-provisioning via `PATCH` with `active: false` updates user status to `inactive`.
- SCIM protocol models and parsing must come from `github.com/elimity-com/scim`; do not add parallel local request/response implementations.

- KyIdentity is the directory authority. Support exact, bounded `externalId` lookup for Users/Groups and replayable PUT membership replacement. External identifiers are immutable; filter attributes must not fall back to substring search.
- Incoming directory roles cannot elevate application administration or replace an explicit operator role grant. Offboarding still disables an application administrator. SCIM user operations cannot modify a local break-glass account. Membership updates use the store’s atomic replacement.

## Verification
- `go test -v ./internal/scim/...`

## Child DOX Index
None.
