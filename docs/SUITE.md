# KyDrive in other suite apps

[KyPost](KYPOST.md) · [Permissions](PERMISSIONS.md) · [Security](../SECURITY.md)

This is KyDrive's durable copy of section I of the suite integration contract (wave 2,
2026-11-14). Both sides change together.

## The picker

Another suite app opens `https://<kydrive>/pick?origin=<caller origin>&mode=link|copy` as a
popup on a user click. The page moves the query to session storage and out of the address bar
at once, so it survives a KyIdentity sign-in, then asks the server whether it may serve it:

`GET /api/drive/pick?origin=&mode=` → 200 `{"app":"KyPost","origin":"…","mode":"link"}`, or a
refusal, after which the page shows only a plain refusal and sends nothing.

| Status | Code | When |
|---|---|---|
| 401 | | Not signed in (the page shows sign-in first). |
| 403 | (message) | A dedicated installer administrator: `/api/drive/` refuses them. |
| 400 | `bad_mode` | `mode` other than `link`, `copy` or empty (`link`). |
| 403 | `bad_origin` | `origin` is not exactly `https://host[:port]` as the origin of an app's `url` in the user's suite app list from KyIdentity (cached a minute). Local accounts have no list. |
| 403 | `copy_not_allowed` | `copy` for any origin but the paired KyPost's. |
| 409 | `sharing_off` | `copy` while the user's KyPost switch is off. |

The user browses their workspaces whose files they may share (My files; a shared workspace
where they are manager, or editor with editors sharing on), ticks up to 20 files and confirms.
That click alone, and only while `window.opener` is open, sends
`POST /api/drive/pick` `{"origin","mode","files":[<file ids>],"days"}` (CSRF as every session
write; same checks as above; 30 a minute per user, else 429 `rate_limited`). The page then
calls `window.opener.postMessage({type:"kydrive.picked", files}, origin)` with the validated
origin, never `*`, and closes.

- `link`: an organisation reader link per file through KyDrive's ordinary link code, listed
  with the file's other links. `days` is 1, 7, 30 or 0 for none; absent means 30.
  `files:[{name,size,url,expires}]`, `expires` RFC 3339 or `null`.
- `copy`: no link. `files:[{name,size,file_id}]`. Each file is recorded as picked
  (`drive_peer_picks`) for 24 hours; KyPost's fetch route reads only picked files (KYPOST.md).
  `days` is refused.
- Each file may also carry `mime`, the media type KyDrive serves the file as (from its name,
  as WebDAV and MCP do), without parameters: sent only when it matches
  `^[a-z0-9]+/[a-z0-9.+-]+$` and is at most 127 bytes, else omitted. The server sets it; the
  page passes the response on unchanged. Receivers must treat it as optional and re-validate it.
- All or nothing: a file the user cannot read or share, trashed, or named twice answers 404
  `not_found`, and links already made in that call are revoked.
- Each picked file is audited (`suite.pick`, `file:<id>`, `app=<id> mode=<mode>` and the link
  ID), never a name, URL or token.

KyDrive sends no `Cross-Origin-Opener-Policy`, so the opener survives without any relaxation;
`/pick` keeps `frame-ancestors 'none'` and `X-Frame-Options: DENY` like every page.

## Suite groups

KyDrive follows KyIdentity's suite group contract (KyIdentity `docs/SUITE-DIRECTORY.md`,
`GET /api/suite/groups`). Every 60 seconds, when KySignOn is configured, it reads the list with
its own client credentials and reconciles each group, keyed on its ID only, never its name or
slug:

- `active`: one shared workspace for the group (created on first sight, quota 1 GiB, named
  after the group, `Sales (2)` when the name is taken, deleted workspaces included), with a
  local group `suite-group:<id>` granted editor on it. The name follows renames. Members are
  exactly the listed subjects that have an active KyDrive account signed in through KyIdentity;
  others are added at a later poll after their first sign-in. KyDrive administrators and the
  local recovery account are never members, and nobody is made manager.
- `archived`: members removed, then the workspace is deleted recoverably (Workspaces > Deleted
  workspaces): links, service tokens and open editors are revoked, files kept. Turning the group
  on again restores the same workspace.
- A group missing from the list, or a call that fails or is refused, changes nothing.

Suite group workspaces are kept for good: never purged by the trash age and never removed,
even empty. An administrator can restore an archived one from Deleted workspaces, but while
KyIdentity still lists the group as archived the next poll archives it again; likewise an
administrator's delete of an active one is undone at the next poll. Removal delay: a member
removed in KyIdentity loses access within one poll (60 s) plus one reconcile pass
(about a millisecond for one group in the tests). Each change is audited as a
`suite_group.*` drive event with IDs and counts only.

## Deviations from the contract text

- Fetch (I2) reads only files the user picked in copy mode within the last 24 hours, not any
  file they may read, so the pair secret alone never reads a user's drive. The wire is
  unchanged.
- KyDrive stores no media type, so `mime` (added 2026-11-15, additive) is the type it derives
  from the file name, the one WebDAV `getcontenttype` and MCP report.
