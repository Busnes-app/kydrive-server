# Euro-Office adapter

## Purpose

Connect KyDrive to an unmodified Euro-Office Document Server.

## Ownership

Own fixed-algorithm JWT authentication, editor configuration, restricted output fetching, first-page conversion and revocation commands.

## Local Contracts

- Bind credentials to document versions and users. Validate signed callbacks before using their fields. `Download` accepts a Document Server file request only when its outbox JWT has `payload.url` equal to the issued download URL; the editor config, callback and command tokens carry no such claim.
- Output downloads stay within the configured editor origin; refuse redirects and cap streamed bytes.
- `Convert` posts a body-signed (`token`) request to `<internal origin>/converter` (`async: false`, `outputtype: png`, `thumbnail: {aspect: 1, first: true}`) and accepts only `endConvert` with a `fileUrl` that `Fetch` would accept (public editor origin, `/cache/`). Verified against Euro-Office 9.3.4 with `TestConvertAgainstLiveEditor` (skipped unless `KYDRIVE_LIVE_SOURCE` is set).
- Session revocation must be visible and retried if the editor refuses it. A successful command is not proof that readable content was recalled from a browser.
- Local development may use explicit HTTP loopback/editor Docker URLs; deployed endpoints require HTTPS.

## Work Guidance

- The public origin constrains output URLs; optional internal origin changes transport only. HS256 is fixed; malformed command responses fail closed. HTTP is development-only.

## Verification
- `go test -race ./internal/editor` covers signature forgery/expiry, download URL binding, output restrictions, redirects and production HTTPS.

## Child DOX Index
