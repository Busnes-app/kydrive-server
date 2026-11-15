# Changelog

Interface and behaviour changes, newest first.

## Unreleased

- **KyPost can fetch a file picked for it (suite contract I2).** New signed route `POST
  /api/suite/kypost/fetch` (`kypost.drive.fetch`, `{sub,issuer,file_id,max_bytes}`) answers
  the raw bytes of a file the person chose in the picker's copy mode within the last 24 hours
  and may still read; anything else is 404 `not_found`, larger than `max_bytes` 413
  `too_large`. This is narrower than the contract's "any file the user may read" so the pair
  secret alone never reads a person's drive. See `docs/KYPOST.md`.
- **KyDrive picker for other suite apps (suite contract I).** `/pick?origin=&mode=link|copy`,
  opened as a popup by a suite app, lets a signed-in everyday person choose up to 20 files they
  may share and, only on the confirming click, sends them to the opener with
  `postMessage` at exactly that origin. The origin must be the `https` origin of an app in the
  person's KyIdentity suite list. Link mode makes reader organisation links (1, 7, 30 days or
  never); copy mode, for the paired KyPost only and with the person's KyPost switch on,
  records the files as picked for 24 hours. New session routes `GET`/`POST /api/drive/pick`;
  schema 22 adds `drive_peer_picks`. See `docs/SUITE.md`.
- **KyPost can save and share files (suite contract C).** When KyPost is paired (by
  KyIdentity, or `KYDRIVE_KYPOST_URL` and `KYDRIVE_KYPOST_SECRET_FILE`), KyPost uploads mail
  attachments into the person's own My files, folder `Mail attachments`, and shares them by
  reader organisation link. New signed routes `POST /api/suite/kypost/upload-tickets`,
  `/links` and `/status` (syncauth v1 under the pair secret) and the ticket upload `PUT
  /api/suite/upload/<ticket>`, the only route that answers CORS for another origin (the paired
  KyPost's, without credentials). Each person chooses under **Settings → Files → Let KyPost
  save and share my files** (default on when KyIdentity paired the apps, off when the
  environment did); **Server settings → System** shows the pairing. KyPost's access is a new
  peer context in `access.go` that can add files to that folder and link them, nothing else.
  Schema 21 adds `drive_peer_settings`, `drive_peer_folders` and `drive_peer_tickets`. See
  `docs/KYPOST.md`.
- **Office networks skip the per-address sign-in limit.** Staff behind one NAT address share
  the per-address windows (20 sign-ins a minute, 10 password changes a minute, 20 failed WebDAV
  app-password sign-ins a minute) and can trip them together. An administrator lists the
  office's networks under **Server settings → Office networks**, or the operator sets
  `KY_RATE_LIMIT_EXEMPT_NETWORKS`; the two lists are combined. Requests from a listed network
  skip those windows (and the step-up's share of the sign-in window) only; per-account windows,
  the CAPTCHA and the limits on agent tokens, device tokens, linking, organisation and guest
  links still apply. Empty by default. Ranges broader than /16 (IPv4) or /48 (IPv6), loopback
  and invalid entries are refused, at startup for the variable and with 400 for the setting.
  The match uses the address resolved through `KY_TRUSTED_PROXIES`. New admin routes GET and
  PUT `/api/settings/rate-limit-networks` (`{networks}`; reply `{networks, environment,
  exempted}`), audited as `admin.rate_limit_networks`. List only networks you control: never
  guest Wi-Fi, a VPN exit or the reverse proxy's address.
- **One shared office budget per limit.** All listed networks together get 200 sign-ins,
  200 password changes and 200 failed WebDAV app-password sign-ins a minute past the
  per-address windows; beyond that their requests meet the ordinary per-address window
  again. One host inside an office therefore cannot make unbounded attempts, nor create
  enough per-account counters to push live ones (and their lockouts) out of the limiter.
  The screen's count includes only requests that actually skipped a per-address window.
- **WebDAV reserves its per-account window before checking an app password.** Concurrent
  wrong guesses at one account could all pass the 10-per-15-minutes check before any was
  counted; now each takes its place first and hands it back on success. Sign-in also hands
  its reservation back when the account lookup fails with a server error.
- **ky-primitives moves to the revision with `netallow`.** It also brings the library's own
  split-file join: capsule `restore` of a database with missing or renumbered parts is still
  refused and writes no database, but the error is now `recoveryclient.ErrSplitParts`
  ("split file parts do not add up") instead of `backup.ErrDatabaseParts`.
- **Guest links are credentials.** `POST /api/drive/{files|folders}/{id}/guest-links` answers
  403 `{"code":"step_up_required"}` without a step-up in the last five minutes (Bearer callers
  included); the Share dialog asks "Confirm it's you" and retries. A password change, reset or
  replacement, and a directory deactivation, revoke the account's guest links; reactivation does
  not bring them back. Creation rechecks the browser session in its transaction, so an in-flight
  request cannot mint a new link after its login was revoked. Organisation links are unchanged.
- **Purges go through the parent.** `DELETE /api/drive/files/{id}`, `DELETE
  /api/drive/folders/{id}` and `DELETE /api/drive/files/{id}/versions/{revision}` need manager
  rank on the item's parent (a file's own share does not count). A share holder purging the
  shared item itself, or its versions, now gets 403 instead of 204; inside a folder shared as
  manager nothing changes.
- **Desktop account ID.** `POST /api/devices/token` also returns `user_id`, the account's
  immutable ID. New `GET /api/drive/device` (device token only) answers `{user_id, user}`.
- **Folder trash precondition.** `PUT /api/drive/folders/{id}/trash` takes an optional `head`, a
  change-feed position: 409 when a change after it names the folder or a live item beneath it
  (or it is below the pruned history). Without `head` the route behaves as before.
