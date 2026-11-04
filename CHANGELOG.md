# Changelog

Interface and behaviour changes, newest first.

## Unreleased

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
