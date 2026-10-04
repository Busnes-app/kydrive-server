# Euro-Office adapter

## Purpose

Connect KyDrive to an unmodified Euro-Office Document Server.

## Ownership

Own fixed-algorithm JWT authentication, editor configuration, restricted output fetching and revocation commands.

## Local Contracts

- Bind credentials to document versions and users. Validate signed callbacks before using their fields.
- Output downloads stay within the configured editor origin; refuse redirects and cap streamed bytes.
- Session revocation must be visible and retried if the editor refuses it. A successful command is not proof that readable content was recalled from a browser.
- Local development may use explicit HTTP loopback/editor Docker URLs; deployed endpoints require HTTPS.

## Work Guidance

- The public origin constrains output URLs; optional internal origin changes transport only. HS256 is fixed; malformed command responses fail closed. HTTP is development-only.

## Verification
- `go test -race ./internal/editor` covers signature forgery/expiry, output restrictions, redirects and production HTTPS.

## Child DOX Index
