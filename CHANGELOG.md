# Changelog

Interface and behaviour changes, newest first.

## Unreleased

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
