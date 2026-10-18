# Desktop sync options for KyDrive

Research note, 2026-10-17, against `master` at `8504407`. No product code. Every external
claim links a primary source; anything not confirmed from one says **unconfirmed**.
KyDrive facts cite `file:line` in this repo.

## Summary

- **Before the November release, ship only docs and one fix.** Add a "Desktop access" page
  covering the OS WebDAV mount (already in the README) and rclone with an app password.
  Fix the ETag-reuse hazard: make the ETag `"<file-id>-<revision>"`. That is a small,
  contained change to `internal/dav/fs.go:468`.
- **After the release, build the server primitives first, not a client.** In order:
  1. stored client mtime (the cheapest rclone upgrade);
  2. a checksum property (rclone reads SHA-1/MD5, not our SHA-256; see section 2);
  3. folder ETags that propagate up the tree;
  4. a per-user change feed with a cursor, tombstones and access-loss rows;
  5. a per-device sync credential issued by a KyDrive-hosted device-link flow.

  Every serious client option needs these. With them, rclone works better today, and either
  Nextcloud-protocol compatibility or a native Ky client becomes feasible later.
- **Then decide between two client paths.** One is a Ky-branded "sync-lite" CLI or tray app
  on librclone (M). The other is a native Ky client on Windows Cloud Files and macOS File
  Provider (XL). Do not chase Nextcloud-client compatibility. It is L-sized, our support
  depends on a third party's release cadence, their trademark policy restricts how we can
  advertise it, and it conflicts with the spirit of the AGENTS.md "no Nextcloud product"
  rule.
- **Contracts.** The roadmap now has desktop sync in scope as M7 (2026-10-17). The AGENTS.md
  Nextcloud rule (`AGENTS.md:104`) stays: this plan does not use Nextcloud products.

## Comparison

Ranked from least to most work. "Server work" is what KyDrive must add on top of today's
code.

| # | Option | UX | Offline | Conflicts | Sign-in | Client effort | Server work |
|---|---|---|---|---|---|---|---|
| 1 | OS WebDAV mount (Windows WebClient, Finder, gvfs, KIO) | Network drive; slow; per-OS quirks | None | Last writer wins; If-Match only if the client sends it | App password, Basic over TLS | S (docs only) | ETag fix; optional persistent locks, mtime |
| 2 | Cyberduck / Mountain Duck profile | Browser (Cyberduck) or mounted drive with smart sync (Mountain Duck, paid) | Mountain Duck: yes | Client-side | App password | S | Same as 1 |
| 3 | rclone mount / bisync, Ky-documented | Mount (VFS cache) or scheduled two-way folder | bisync: full local copy; mount: cache only while running | bisync conflict copies; mount: last writer | App password in encrypted config + keyring | S–M | ETag fix, checksums, mtime; vendor-compatible props help a lot |
| 4 | Ky "sync-lite" on librclone | Ky-branded tray/CLI, one-click link | Full local copy | bisync semantics, Ky-tuned | Device-link flow, token in OS keyring | M | Device credential + change feed |
| 5 | Nextcloud-protocol compatibility | Mature client, VFS on Windows | Yes | Mature engine | Login Flow v2 (= app password) | 0 (theirs) | L: status.php, OCS, oc: props, propagated folder ETags, chunking v2 |
| 6 | Seafile emulation | Mature client | Yes | Theirs | Seafile tokens | 0 (theirs) | XL: different data model (commits, blocks) |
| 7 | Native Ky client on cfapi / File Provider / FUSE | Best: placeholders, Explorer/Finder native | Yes, pin-able | Ours | Device-link flow | XL | Change feed, IDs, hashes, device creds, push or long-poll |
| – | Syncthing / BEP | — | — | — | — | Does not fit | Does not fit (peer model) |

## KyDrive today (the baseline)

- **Model.** Workspaces with group grants, a personal workspace per user, shares and links.
  Folders and files are keyed by UUID. Files carry an integer `revision`, and versions carry
  a SHA-256 `digest` (`internal/drive/store.go:92-104`). Trash is a flag. Names are encrypted
  at rest; this is not end-to-end encryption.
- **REST API.** Per-folder listings, file info, download, versions, trash, move, copy,
  search, and shared-with-me (`internal/api/drive_handlers.go:25-76`). It also has
  **resumable upload sessions**, published with an expected revision (conflict-checked). They
  expire after 24 h, with a 10 GiB default cap.
- **Auth.** Browser session cookie, or workspace-scoped bearer service tokens (one
  workspace, 90-day) (`internal/api/server.go:334-364`). There is no per-user native-app
  credential.
- **No change feed.** `drive_events` is an audit log, with no sequence, no workspace column
  and no read filtering.
- **WebDAV** at `/dav/`, Basic auth with per-user app passwords (`internal/dav/AGENTS.md`).
  The ETag is the quoted per-file revision (`internal/dav/fs.go:468`).
  - Folders have no ETag and zero mtime.
  - PROPPATCH is accepted but stores nothing.
  - No checksum property.
  - `Depth: infinity` gets 403.
  - Locks are kept in memory, last at most 1 h, and are lost on restart.
  - Shared-with-me is not exposed.
- **KyIdentity.**
  - Supports: auth code with mandatory PKCE S256, exact redirect matching, 15-min access
    tokens, RFC 7009 revocation, and a jwt-bearer grant for paired KyAuth devices.
  - Does not support: refresh tokens, the device grant, or any-port loopback.

### Verified hazard: ETag reuse defeats If-Match

A throwaway probe test against the real DAV handler and store (`go test ./internal/dav`,
2026-10-17, master `8504407`) showed:

1. `PUT a.txt` → ETag `"1"`.
2. `DELETE a.txt` → the file is trashed.
3. `PUT a.txt` with different content → a new file, ETag `"1"` again.
4. `PUT a.txt` with `If-Match: "1"` (the deleted file's ETag) → **201**.

So a stale client's save is accepted over an unrelated new file. The overwritten version
stays in history, so no data is lost, but If-Match conflict protection fails across
delete+recreate, and also across rename-away+recreate. The same probe confirmed that
`PROPFIND Depth: 0` on a workspace root returns no `getetag`.

RFC 9110 requires If-Match to use strong comparison and to refuse the method when the
condition is false ([RFC 9110 §13.1.1](https://www.rfc-editor.org/rfc/rfc9110#section-13.1.1)).
It also says a strong validator changes whenever the representation would change
([§8.8.1](https://www.rfc-editor.org/rfc/rfc9110#section-8.8.1)). Two different files that
share `"1"` break that.

**Fix:** make the ETag `"<file-uuid>-<revision>"`, or the version digest. If a sync client
is the target, also give folders an ETag (see Primitives). This is S-sized and worth doing
before the release, because every option below relies on ETags.

---

## 1. OS-native WebDAV mounts

### Windows: WebClient / Mini-Redirector ("Map network drive")

- **Status: deprecated.** Microsoft's deprecated-features list says: "The Webclient (WebDAV)
  service is deprecated. The Webclient service isn't started by default in Windows." No
  removal date is given
  ([Deprecated features for Windows client](https://learn.microsoft.com/en-us/windows/whats-new/deprecated-features)).
  The announcement date is November 2023 per that page. Whether current Windows 11 builds
  still ship the component installed is **unconfirmed** from a primary source.
- **Limits.** These come from the `WebClient\Parameters` registry key
  ([Using the WebDAV Redirector, IIS docs](https://learn.microsoft.com/en-us/iis/publish/using-webdav/using-the-webdav-redirector)):
  - `FileSizeLimitInBytes` defaults to 50,000,000. The README already tells admins to raise
    it (`README.md:86`).
  - The commonly quoted 4 GB ceiling (`4294967295`) is a DWORD maximum. A Microsoft
    statement of it is **unconfirmed**.
  - `FileAttributesLimitInBytes` is 1,000,000, which caps the PROPFIND response size, so very
    large folders fail.
  - `SendReceiveTimeoutInSec` is 60.
- **Auth.** `BasicAuthLevel` defaults to 1, meaning Basic only over SSL (same IIS page).
  That suits us: app password over HTTPS only.
- **Offline.** None of the documented settings concerns offline use. There is no offline
  mode. Treat it as an online network drive.

### macOS: Finder "Connect to Server" (webdavfs)

- **How you connect.** Go > Connect to Server with an `http(s)://` URL
  ([Apple support](https://support.apple.com/guide/mac-help/connect-to-a-webdav-server-mchlp1546/mac)).
- **Locking.** In Apple's source, the mount sends OPTIONS. A server advertising only DAV
  class 1 is mounted **read-only**; class 2 (LOCK) is needed for write
  ([webdavfs `mount.tproj/webdav_network.c`](https://github.com/apple-oss-distributions/webdavfs)).
  So Finder writes depend on our in-memory locks.
- **Chunked PUT.** The sequential-write path sends PUT with no `Content-Length`, only
  `X-Expected-Entity-Length`, and a streamed body (same file). That is chunked transfer
  encoding. KyDrive's behaviour with chunked PUT bodies is untested here.
- **AppleDouble.** The kext has no xattr vnops and handles `._` companion files
  (`webdav_vnops.c`). The inference is that extended attributes land on the server as `._*`
  files. Apple does not state this outright, so treat it as source-inferred. A primary
  source for `.DS_Store` writes and Finder performance is **unconfirmed**.
- **Offline:** none.

### Linux: GNOME gvfs and KDE KIO

- **gvfs** (`dav://`, `davs://`, LGPL-2.0) uses GET, HEAD, OPTIONS, PROPFIND, PUT, MKCOL,
  DELETE, MOVE and COPY, but **no LOCK**. It uploads with `Content-Length`, and its only
  cache is the credential cache
  ([gvfsbackenddav.c](https://gitlab.gnome.org/GNOME/gvfs/-/blob/master/daemon/gvfsbackenddav.c)).
- **KIO** (KDE Frameworks, `src/kioworkers/http`, LGPL-2.0-or-later) supports PROPPATCH,
  LOCK and UNLOCK with lock tokens. It buffers PUT bodies rather than chunking them, because
  chunking is something "not every server and proxy accepts"
  ([http.cpp](https://invent.kde.org/frameworks/kio/-/blob/master/src/kioworkers/http/http.cpp)).
- **Offline:** none found in either.

### What breaks with KyDrive specifically

| KyDrive behaviour | Effect on OS mounts |
|---|---|
| Locks in memory, max 1 h, lost on restart | Finder needs class 2 to write. A KyDrive restart drops locks, so a client holding a token gets errors on the next request (unconfirmed which). |
| PROPPATCH stores nothing | Copy-in keeps no source mtime. File managers show the upload time. |
| `Depth: infinity` → 403 | Fine for mounts, which walk per folder. Tools that try infinity must fall back. |
| Shared-with-me not on DAV | Shared items are invisible in the mount. Users must use the web UI. |
| Folders have no ETag, zero mtime | Explorer and Finder show 1970 or blank dates on folders, and clients can't cheaply detect folder changes. |
| ETag reuse (above) | Silent If-Match bypass on delete+recreate. |
| `._*` files from macOS | Clutter in the shared workspace, visible to Windows and web users. Consider rejecting or hiding `._*` and `.DS_Store` on PUT (a policy call). |

**Sign-in:** app password, already revocable, named, and killed on deactivation.
**Security:** fine over TLS. Basic auth sends the app password on every request, so it must
stay DAV-only, as it is today.
**Effort: S.** Docs only. The README already covers Windows.
**Server changes:** the ETag fix. Optionally: persist locks, store PROPPATCH
`getlastmodified`, and give folders an mtime.

---

## 2. rclone

rclone is MIT-licensed ([COPYING](https://github.com/rclone/rclone/blob/master/COPYING)).

### `rclone mount`

VFS cache modes ([rclone mount](https://rclone.org/commands/rclone_mount/)):

- `off` (default): no disk cache, many POSIX operations unsupported.
- `minimal`: read+write opens buffered to disk.
- `writes`: writes buffered to disk; "should support all normal file system operations".
  Failed uploads retry.
- `full`: all reads and writes are buffered via sparse files.

Write-back starts after `--vfs-write-back`, which defaults to 5 s. Uploads left pending
when rclone dies resume on the next run with the same flags (same page).

- **Change detection.** rclone's WebDAV backend implements no `ChangeNotify`
  ([backend/webdav/webdav.go](https://github.com/rclone/rclone/blob/master/backend/webdav/webdav.go)).
  So remote changes appear only when `--dir-cache-time` expires, which defaults to 5 min.
- **Offline.** Whether a full-cache mount serves cached files while the server is
  unreachable is not documented, so it is **unconfirmed**. The cache's directory state is
  not persisted. Do not sell mount as offline.
- **Windows** requires [WinFsp](https://rclone.org/commands/rclone_mount/). WinFsp is
  GPLv3 with a FLOSS linking exception. The exception forbids distributing it with
  proprietary software, and requires an attribution line in the UI
  ([WinFsp License.txt](https://github.com/winfsp/winfsp/blob/master/License.txt)). An
  MIT-licensed Ky app may bundle the unmodified installer under that exception.
- **macOS** needs macFUSE or FUSE-T, or rclone's NFS-based mount. The docs recommend
  `rclone nfsmount` on macOS ([rclone mount](https://rclone.org/commands/rclone_mount/),
  [rclone nfsmount](https://rclone.org/commands/rclone_nfsmount/)).
  - The macFUSE kernel extension is closed source. Its licence forbids bundling it with
    commercial software without permission
    ([macFUSE LICENSE.txt](https://github.com/macfuse/macfuse/blob/master/LICENSE.txt)).
  - FUSE-T is a proprietary binary and needs a commercial licence for commercial bundling
    ([FUSE-T License.txt](https://github.com/macos-fuse-t/fuse-t/blob/main/License.txt)).
  - `nfsmount` avoids both. Ky should document `nfsmount` on macOS.

### `rclone bisync`

Source for all of this: [rclone bisync](https://rclone.org/bisync/).

- **Status.** Billed as "an advanced command, so use with care". "Files that change during
  a bisync run may result in data loss". It must be run periodically from cron or Task
  Scheduler, and lock files make overlapping runs safe.
- **First run.** `--resync` is required on the first run and after filter changes. Running
  it every time makes deletions "reappear".
- **`--check-access`** requires `RCLONE_TEST` files on both sides. It guards against syncing
  against an empty or unmounted path.
- **`--max-delete`** defaults to 50%. Above that, the run aborts.
- **Conflicts.**
  - `--conflict-resolve`: none, newer, older, larger, smaller, path1 or path2.
  - `--conflict-loser`: num (`file.conflict1`), pathname or delete.
  - `--conflict-suffix`.
- **Comparison.** `--compare size,modtime,checksum`. Since v1.66, modtime is no longer
  required. Without modtime, bisync can tell changed from unchanged but **not newer from
  older**, so `--conflict-resolve newer` is meaningless against plain WebDAV.
- **Recommended unattended flags:** `--resilient --recover --max-lock 2m --conflict-resolve newer`.

### WebDAV backend vendors

Sources: [rclone webdav](https://rclone.org/webdav/) and
[webdav.go](https://github.com/rclone/rclone/blob/master/backend/webdav/webdav.go).

| `vendor` | Sets modtime | Hashes | Chunked upload |
|---|---|---|---|
| `other` | No ("Plain WebDAV does not support modified times") | None | No |
| `nextcloud` | `X-OC-Mtime` on PUT, and PROPPATCH `lastmodified` | SHA1 via `oc:checksums` / `OC-Checksum` | Nextcloud chunking v2 (`--webdav-nextcloud-chunk-size`, 10 MiB) |
| `owncloud` | `X-OC-Mtime`, PROPPATCH | MD5, SHA1 | No |
| `infinitescale` | `X-OC-Mtime`, PROPPATCH | SHA1 | tus, 10 MB chunks |

Notes:

- The docs list "SHA1 and MD5" for Nextcloud. The code shows SHA1 only. Trust the code.
- **For KyDrive today the only honest vendor is `other`**: no mtime, no hashes, so bisync
  must use `--compare size` plus whatever ETag gives it. That is weak.
- **Cheapest server upgrade with the biggest rclone payoff.** Accept `X-OC-Mtime` on PUT
  (store it as client mtime) and expose `oc:checksums` from the stored digest. Users could
  then pick `vendor=owncloud`.
  - Our digest is SHA-256, and rclone's owncloud vendor reads MD5/SHA1. Checksums would
    need SHA-1 stored too, or an upstream change to rclone.
  - The mtime gain needs no hash change.
  - Choosing a third party's vendor name is a compatibility promise. See section 3 for the
    risk.

### Credentials

- **`rclone obscure` is not secret.** The docs say: "This is **not** a secure way of
  encrypting these passwords as rclone can decrypt them"
  ([rclone obscure](https://rclone.org/commands/rclone_obscure/)). A Ky config with an
  obscured app password is a plaintext credential on disk.
- **Config encryption** uses NaCl secretbox (XSalsa20+Poly1305), keyed by SHA-256 of the
  password ([rclone docs: configuration encryption](https://rclone.org/docs/#configuration-encryption)).
  `--password-command` / `RCLONE_PASSWORD_COMMAND` fetch that password from a helper.
- **No native keyring support.** The docs shell out instead (same page):
  - macOS: `security find-generic-password -a rclone -s config -w`.
  - Linux: `pass`. Using `secret-tool` with libsecret is my extrapolation, not documented.
  - Windows: PowerShell `Import-Clixml`.
- **Better for Ky:** a `bearer_token_command`-style hook exists for WebDAV
  ([rclone webdav](https://rclone.org/webdav/)). A Ky helper could fetch a short-lived token
  from the keyring-held device credential. That needs DAV to accept bearer tokens, which it
  doesn't today.

**UX:** a mount is close to the OS-mount UX but more robust. bisync is a real offline folder
with conflict copies. Both are fiddly for non-technical users.
**Sign-in:** app password. Store it in an encrypted config whose password lives in the OS
keyring.
**Offline / conflicts:** bisync gives a full local copy plus conflict copies. A mount gives
none.
**Security:** fine if the docs insist on config encryption. Obscured passwords at rest are
the main trap.
**Effort: S** for a documented page with a copy-paste config. **M** for a small wizard
script per OS.
**Server changes:** the ETag fix, client mtime via `X-OC-Mtime`/PROPPATCH, a checksum
property, and folder mtimes.

---

## 3. Speaking a mature client's protocol

### 3a. Nextcloud desktop client

**What the client needs.** Sources: [nextcloud/desktop](https://github.com/nextcloud/desktop)
`src/gui/connectionvalidator.cpp` and `src/libsync/discovery.cpp`.

1. `status.php` JSON containing `installed`, with `maintenance` false.
2. `ocs/v1.php/cloud/capabilities`, with `core.status.version`. A version below 20 only
   triggers an "Unsupported Server Version … Proceed at your own risk" tray warning, not a
   block.
3. A terms-of-service OCS probe.
4. **Login Flow v2**
   ([docs](https://docs.nextcloud.com/server/latest/developer_manual/client_apis/LoginFlow/index.html)):
   - anonymous `POST /index.php/login/v2`;
   - the browser opens `login`;
   - the client polls `/login/v2/poll` with a token valid 20 min;
   - the result is `server`, `loginName` and **`appPassword`**, returned once.
5. **WebDAV at `/remote.php/dav/files/{user}/`**
   ([docs](https://docs.nextcloud.com/server/latest/developer_manual/client_apis/WebDAV/basic.html)):
   - properties `oc:fileid`, `oc:id`, `oc:permissions` (letters S R M G D N V W C K),
     `oc:checksums`, `oc:size`, plus `nc:` properties;
   - the client treats an entry as **missing data** if size, permissions, ETag or fileid is
     absent (`discovery.cpp`).
6. **Chunked upload v2**
   ([docs](https://docs.nextcloud.com/server/latest/developer_manual/client_apis/WebDAV/chunking.html)):
   - `MKCOL /remote.php/dav/uploads/<user>/<id>`;
   - PUT chunks 1–10000 (5 MB–5 GB);
   - `MOVE .file` to assemble, with `Destination`, `OC-Total-Length` and `X-OC-Mtime`;
   - 24 h expiry.

**Change detection is folder-ETag propagation, not a change feed.**

- The admin manual: the client "only issues a PROPFIND on the root of the sync folder on
  every sync cycle". This relies on the etags of all parent folders changing when a file
  changes
  ([admin manual](https://docs.nextcloud.com/server/stable/admin_manual/configuration_files/external_storage_configuration_gui.html)).
- `discovery.cpp` skips any subtree whose folder ETag and permissions are unchanged.
- The root poll runs every 30 s (`DEFAULT_REMOTE_POLL_INTERVAL 30000`,
  `src/libsync/configfile.cpp`). This is from the source; a docs statement is
  **unconfirmed**.
- An optional websocket push (`notify_push`) replaces polling.

**Virtual files** (from the source):

| Plugin | Platform | Status |
|---|---|---|
| `cfapi` | Windows | Production |
| `openvfs` | Linux (FUSE, from OpenCloud) | Labelled experimental; "This is **experimental** code" ([openvfs](https://github.com/opencloud-eu/openvfs)) |
| `suffix`, `xattr` | — | Experimental |
| Separate File Provider build | macOS | One domain per account ([docs](https://docs.nextcloud.com/server/stable/user_manual/de/desktop/macosvfs.html)) |

**Licence:** GPL-2.0-or-later
([COPYING](https://github.com/nextcloud/desktop/blob/master/COPYING), SPDX headers). The
client is a fork of the ownCloud client.

### 3b. ownCloud client, oCIS, OpenCloud

- **ownCloud desktop**: GPLv2-or-later
  ([COPYING](https://github.com/owncloud/client/blob/master/COPYING)).
  - Since 7.1.0 it **removed ownCloud 10 support and requires oCIS**, dropping the OC10
    auth flows ([changelog 7.1.0](https://github.com/owncloud/client/tree/master/changelog)).
  - Auth is OIDC discovery (`src/gui/creds/oauth.cpp`); I infer it is OIDC-only from the
    source.
  - VFS: Windows cfapi only.
- **oCIS**: Apache-2.0 ([LICENSE](https://github.com/owncloud/ocis/blob/master/LICENSE)).
  - Spaces via libregraph `GET /graph/v1.0/me/drives`, each with a `webDavUrl`
    ([Spaces API](https://owncloud.dev/apis/http/graph/spaces/)).
  - Uploads via tus ([TUS upload](https://owncloud.dev/apis/http/tus_upload/)).
  - Emulating this means libregraph, spaces DAV, tus, and an OIDC provider whose behaviour
    the client accepts. KyIdentity issues no refresh tokens, so a 15-minute session would
    force re-login. **Not viable without KyIdentity changes.**
- **OpenCloud** exists ([github.com/opencloud-eu](https://github.com/opencloud-eu)). It is a
  2025 fork of oCIS; the repos carry ownCloud history.
  - Server: Apache-2.0.
  - Desktop ([opencloud-eu/desktop](https://github.com/opencloud-eu/desktop)): GPL-2.0, a
    fork of the ownCloud client, VFS plugins cfapi/off/openvfs.
  - openvfs: GPL-3.0.
  - Same protocol family as oCIS.

### 3c. Seafile

- **Protocol.** Its own git-like model of repos, commits, fs objects and content-defined
  chunked blocks ([data model](https://manual.seafile.com/latest/develop/data_model/)). The
  daemon speaks a `/seafhttp/` API: head-commits, `check-fs`/`recv-fs`/`pack-fs`,
  `check-blocks`/`block` and others
  ([haiwen/seafile `daemon/http-tx-mgr.c`](https://github.com/haiwen/seafile)).
- **Emulating it** means re-modelling KyDrive storage as commit DAGs over blocks. **XL, and
  a different product.** Reject.
- **Licences.**
  - Daemon: GPLv2 with OpenSSL exception
    ([LICENSE.txt](https://github.com/haiwen/seafile/blob/master/LICENSE.txt)).
  - GUI: Apache-2.0 ([seafile-client](https://github.com/haiwen/seafile-client)).
  - Server: AGPLv3 ([seafile-server](https://github.com/haiwen/seafile-server)).
  - SeaDrive GUI: Apache-2.0. SeaDrive Linux FUSE: GPL-3.0.
  - Windows/macOS SeaDrive daemon licence: **unconfirmed** (no public repo).

### Trademarks, honesty, and existing rules

- **Nextcloud** ([trademarks](https://nextcloud.com/trademarks/)):
  - "You **should not** include a Nextcloud mark in the name of your application, product or
    service".
  - Identifying compatible projects "is only allowed for non-commercial purposes".
  - Redistributing the desktop client needs permission.
  - So "KyDrive works with the Nextcloud client" in marketing is restricted, and shipping
    their binary is not allowed without permission.
- **ownCloud** ([trademark](https://owncloud.com/trademark/)) permits "Works with ownCloud"
  style taglines. A modified client build must remove the marks and "MUST make clear that
  your client is NOT the official ownCloud client".
- **Forking or rebranding is legal** under GPLv2 (source offer, same licence for the client,
  marks removed). It means owning a large C++/Qt codebase.
  - Nextcloud sells branded clients as part of Enterprise
    ([pricing](https://nextcloud.com/pricing/)). Whether self-service branding docs are
    public is **unconfirmed**.
  - ownCloud publishes a
    [Branded Clients manual](https://doc.owncloud.com/pdf/branded_clients/next_ownCloud_Branded_Clients_Manual.pdf).
  - Either way we'd inherit their protocol assumptions.
- **Supportability.**
  - Their client can change what it requires in any release, and we'd find out from users.
  - The client already requests `nc:` lock, encryption and live-photo properties, and gates
    features on server version.
  - Login Flow v2 is an app-password design, which matches what we have. But the user sees
    "Nextcloud" branding while their data is in KyDrive, which is a trust and confusion
    cost.
- **`AGENTS.md:104`.** "Euro-Office is the editor; no Nextcloud or ONLYOFFICE product
  deployment." Recommending users install the Nextcloud client is not literally a server
  deployment. It is, however, a Nextcloud product in our supported path, and **changes that
  contract's spirit**. Decide explicitly before doing it.

**Option 5 (Nextcloud compatibility) as a whole:**
- **UX:** excellent, from their client.
- **Sign-in:** Login Flow v2 maps onto app passwords.
- **Offline / conflicts:** theirs, mature.
- **Security:** a third-party binary holds the credential, and we can't fix client bugs.
- **Effort: L** on the server. We need `status.php`, OCS capabilities, `/remote.php/dav`
  paths, `oc:` properties, propagated folder ETags, chunking v2 and Login Flow v2. The
  propagated ETags are themselves a schema change.
- **Ongoing:** unbounded compatibility tracking.

---

## 4. A thin Ky client on OS virtual-file APIs

### Windows: Cloud Files API (cfapi)

- **Requirements.** Windows 10 1709 or later, desktop apps only (not UWP), NTFS only. The
  engine is designed for a packaged app (Desktop Bridge)
  ([Build a cloud file sync engine](https://learn.microsoft.com/en-us/windows/win32/cfapi/build-a-cloud-file-sync-engine)).
- **Placeholders.** ~1 KB reparse points. Files can be placeholder, full, or pinned full
  (same page).
- **`CfRegisterSyncRoot`.** Registers a sync root once, with hydration, population and
  in-sync policies. File identity blobs go up to 4 KB
  ([CfRegisterSyncRoot](https://learn.microsoft.com/en-us/windows/win32/api/cfapi/nf-cfapi-cfregistersyncroot)).
- **Hydration policies.** PROGRESSIVE is the default and must handle random-offset fetch
  callbacks. PARTIAL is "not currently supported"
  ([CF_HYDRATION_POLICY_PRIMARY](https://learn.microsoft.com/en-us/windows/win32/api/cfapi/ne-cfapi-cf_hydration_policy_primary)).
  KyDrive already serves this: sealed blobs are AES-256-GCM in 64 KiB chunks with a
  seeking reader (`internal/drive/sealed.go:16-18`, `:202`), and downloads use
  `http.ServeContent` (`internal/api/drive_handlers.go:348`), so a range decrypts only the
  chunks it touches.
- **`CfConnectSyncRoot`** takes the callback table
  ([docs](https://learn.microsoft.com/en-us/windows/win32/api/cfapi/nf-cfapi-cfconnectsyncroot)).
  **`CfSetPinState`** sets "always keep on this device"
  ([docs](https://learn.microsoft.com/en-us/windows/win32/api/cfapi/nf-cfapi-cfsetpinstate)).
- **Samples.** Microsoft's CloudMirror sample is MIT. It is "not intended to be used as
  production code" and does not do cloud-to-local sync
  ([Windows-classic-samples LICENSE](https://github.com/microsoft/Windows-classic-samples/blob/main/LICENSE)).
- **Libraries.**
  - Rust wrappers: [ok-nick/wincs](https://github.com/ok-nick/wincs) (MIT, small, README
    flags an uncertain future) and its fork
    [ho-229/cloud-filter-rs](https://github.com/ho-229/cloud-filter-rs) (MIT).
  - Neither is mature.
  - No Go wrapper was found. Whether a .NET one exists is **unconfirmed**.

### macOS: File Provider

- **`NSFileProviderReplicatedExtension`** (macOS 11+): the system keeps replicated
  placeholders on disk
  ([docs](https://developer.apple.com/documentation/fileprovider/nsfileproviderreplicatedextension)).
- **It needs a change feed.** `enumerateChanges(for:from:)` "Requests the next batch of
  changes after the specified sync anchor"
  ([docs](https://developer.apple.com/documentation/fileprovider/nsfileproviderenumerator/enumeratechanges(for:from:))).
  Expired anchors return `syncAnchorExpired` and force a full re-enumeration. The extension
  calls `signalEnumerator` when it learns of server changes by push or poll
  ([Synchronizing files using File Provider extensions](https://developer.apple.com/documentation/fileprovider/synchronizing-files-using-file-provider-extensions)).
  **File Provider presumes a server change feed with anchors.**
- **Eviction.** `evictItem` fails on items with unuploaded changes or open descriptors
  ([docs](https://developer.apple.com/documentation/fileprovider/nsfileprovidermanager/evictitem(identifier:completionhandler:))).
- **Conflicts.** Modify and delete receive a `baseVersion`, so the server needs an
  ETag-style version to compare. Apple's explicit conflict-resolution guidance is
  **unconfirmed**.
- **Distribution.** Developer ID plus notarization requires the paid Apple Developer
  Program, $99/year ([programs](https://developer.apple.com/programs/)). Whether the
  extension must be sandboxed, and whether File Provider works on a free account, are
  **unconfirmed**.

### Linux

- **No placeholder API equivalent to cfapi or File Provider was found.** Its absence is
  **unconfirmed** by any primary source.
- **Choices:**
  - FUSE: [libfuse](https://github.com/libfuse/libfuse/blob/master/LICENSE) (lib
    LGPL-2.1) or [go-fuse](https://github.com/hanwen/go-fuse/blob/master/LICENSE)
    (BSD-3-style).
  - A plain two-way sync folder, as in option 4 of the comparison.
  - A KIO worker or gvfs backend for file-manager integration
    ([KIO](https://invent.kde.org/frameworks/kio), [gvfs](https://gitlab.gnome.org/GNOME/gvfs)).
- **Pragmatic Linux answer:** a sync folder, plus the existing gvfs/KIO DAV mounts for
  browsing.

### Implementation shape

- **The engine** (state DB, change-feed consumer, uploader using our resumable sessions,
  conflict copies) is shareable in Go. KyDrive and the KyAuth/kypost desktop apps are
  already in the suite.
- **The platform shims are not shareable:**
  - cfapi is C. Go would need cgo or syscall bindings, and no library exists.
  - File Provider is a Swift app extension.
  - FUSE has go-fuse.
- **Rust** has the only cfapi wrappers, and they are thin.
- **Realistic plan:** a Go core daemon, a Swift File Provider extension talking to it, and
  cfapi called from the native Windows shell (see "What a Ky client looks like").

**UX:** best. Native placeholders, Files On-Demand style, Finder/Explorer status.
**Sign-in:** device-link flow, with the credential in the OS keychain (see Sign-in design).
**Offline / conflicts:** pinned files are offline. Conflicts are resolved by our engine
against server ETags.
**Security:** we own the code. The credential is device-bound, the extension sandbox reduces
blast radius, and a paid Apple account plus code signing are mandatory.
**Effort: XL.** Three platform integrations, a sync engine, code signing and notarization,
installers, and auto-update. This is months of focused work for one person, and much more
than a release cycle.
**Server changes:** all the primitives below, plus push or long-poll for prompt
`signalEnumerator`.

---

## 5. Other

### Syncthing / BEP

MPL-2.0 ([LICENSE](https://github.com/syncthing/syncthing/blob/main/LICENSE)). BEP is a
cluster of peer devices
([BEP v1](https://docs.syncthing.net/specs/bep-v1.html)):

- the global model is "the union of all files in the local models";
- device identity is a TLS certificate fingerprint;
- there are no users.

The only access controls are per-device folder types (send-only, receive-only,
receive-encrypted). Nothing in it maps to workspace grants, shares, revocation on
deactivation, or version history on the server. **Does not fit** a server-authoritative,
permissioned drive.

### Cyberduck / Mountain Duck

- **Cyberduck.** GPLv3 ([LICENSE.txt](https://github.com/iterate-ch/cyberduck/blob/master/LICENSE.txt)).
  A `.cyberduckprofile` is an XML plist with `Protocol`, `Vendor`, `Description` and
  defaults such as hostname and path. Users double-click it to install
  ([profiles](https://docs.cyberduck.io/protocols/profiles/)). Shipping a `KyDrive.cyberduckprofile`
  (protocol `davs`, path `/dav/`) is near-zero effort.
- **Mountain Duck** is commercial, sold per seat by major version
  ([buy](https://mountainduck.io/buy/); prices **unconfirmed**).
  - **Integrated mode** uses File Provider on macOS and Cloud Files on Windows
    ([interface](https://docs.mountainduck.io/mountainduck/interface/)).
  - **Smart Synchronization** gives offline work with background upload and "Keep Offline"
    pinning ([sync](https://docs.mountainduck.io/mountainduck/sync/)).
  - **WebDAV:** it uses LOCK/UNLOCK when editing. Preserving mtimes needs server-side
    custom-property storage ([WebDAV](https://docs.mountainduck.io/protocols/webdav/)). So
    our PROPPATCH-drops-everything costs us mtimes here too.
- **Verdict.** Worth a profile and a docs mention as the "paid but polished" route. We can't
  depend on it as the official client.

### Ky "sync-lite" on librclone (the comparison table's #4)

- **librclone** exposes rclone's rc API (`RcloneRPC` with JSON) to Go and C. Its interfaces
  "are experimental and may change in the future"
  ([librclone README](https://github.com/rclone/rclone/blob/master/librclone/README.md)).
  A Go app can import rclone packages directly, and rclone is MIT, so it's compatible with
  KyDrive's MIT licence.
- **What we'd build:** a Ky-branded CLI or tray app that:
  1. runs the device-link sign-in;
  2. stores the credential in the keyring via [zalando/go-keyring](https://github.com/zalando/go-keyring)
     (MIT; uses Credential Manager, `/usr/bin/security`, and Secret Service);
  3. runs bisync-style sync of chosen workspaces to local folders on a schedule.
- **Version two:** replace polling with our change feed.
- **Effort: M.** No virtual files and no kernel drivers.
- **Risk:** bisync's documented "files that change during a run may be lost" caveat. Our
  change feed plus If-Match uploads would let us replace bisync with a small purpose-built
  loop later.

---

## What a Ky client looks like

Decided by Yoshi on 2026-10-17: native Windows UI; wiping on revoke is an option; Shared
with me waits for v2; the roadmap is amended (milestone M7).

**Shape.** One Go sync core per user runs as a background process (Windows per-user
service, macOS launch agent, systemd user unit). It owns:
- the state DB (SQLite);
- the change-feed poller;
- uploads and downloads through the resumable sessions;
- the conflict rules;
- the keyring.

Each OS gets a thin native shell, following the suite's existing desktop apps (KyPost:
SwiftUI on macOS, Qt6/Kirigami Flatpak on Linux). The shell talks to the core over a local
socket or named pipe.

| | Windows | macOS | Linux |
|---|---|---|---|
| Shell | WinUI 3 (Windows App SDK), tray icon | SwiftUI menu-bar app | Qt6/Kirigami tray |
| Files on demand (phase 2) | Cloud Files API, called from the shell. A P/Invoke route (e.g. CsWin32) is unverified | File Provider extension (Swift) | None: plain sync folder |
| Package | Signed MSIX | Developer ID, notarized | Flatpak (`--filesystem` for the sync folder), .deb/.rpm/AUR |

**Linking.** "Link this computer" opens the browser and goes through KyIdentity sign-in to
a KyDrive approval page showing the device name and code (see Sign-in design). The token
goes into the OS keychain. The device is then listed on the Account page, revocable, and
killed on deactivation.

**Phase 1, sync-lite (all three OSes).**
- A `KyDrive` folder with one subfolder per synced workspace. Every synced file is a full
  local copy, so it works offline.
- The tray shows status, activity and conflicts, lets the user choose workspaces and
  folders, and offers "Open in browser".
- Shared with me is excluded until the change feed has a per-user share slice (v2).

**Phase 2, files on demand.**
- Windows: Explorer navigation-pane entry with status icons, "Always keep on this device"
  and "Free up space".
- macOS: Finder Locations entry under `~/Library/CloudStorage`.
- Linux stays a sync folder; the GNOME/KDE WebDAV mounts cover browsing.

**Behaviour.**
- **Changes:** poll the feed every 30–60 s, and also on wake and on network change.
- **Uploads:** resumable sessions against the last-seen revision.
- **Conflicts:** a refused stale upload keeps both copies. The local one is renamed
  `<name> (conflict from <device> <date>).<ext>` and listed in the tray.
- **Deletes:** go to the server trash. A large deletion pauses for confirmation.
- **Lost access:** synced copies are removed. Local edits that were never uploaded move to
  a local `Unsynced` folder and are never destroyed.
- **Revoking a device** stops sync at once. The revoke dialog offers "Also remove KyDrive
  files from this computer". When chosen, the client deletes synced copies the next time it
  reaches the server and is told it is revoked. Unuploaded edits still go to `Unsynced`.
  - A device that never comes back online never wipes. This is not a remote-wipe guarantee
    for a stolen offline laptop, so the UI must say so.
  - The checkbox defaults to checked (wipe). An administrator can change the drive-wide
    default and lock it, so users cannot change it when revoking.

**Prerequisites.** The task plan is `superpowers/plans/2026-10-18-m7-desktop-sync.md`. It refines
this note: a per-workspace change log instead of a per-user feed, no propagated folder ETags or
new checksum property, and loopback + PKCE linking instead of a device code. Phase 1 is M;
phase 2 is XL per platform.

## Server-side primitives any real sync needs

| Primitive | Reference design | Why |
|---|---|---|
| **Change feed with cursor** | Graph `delta`: follow `nextLink` to a `deltaLink`; "same item may appear more than once… use the last occurrence"; 410 Gone with resync on expired tokens ([driveItem delta](https://learn.microsoft.com/en-us/graph/api/driveitem-delta)). Dropbox `list_folder`/`continue` cursor, `reset` error, `longpoll` ([API spec](https://github.com/dropbox/dropbox-api-spec)). Google `getStartPageToken` + `changes.list`; the token "doesn't expire" ([manage changes](https://developers.google.com/workspace/drive/api/guides/manage-changes)). | Polling every folder is O(tree). File Provider requires anchors. |
| **Tombstones and permission loss in the feed** | Graph `deleted` facet; Dropbox `DeletedMetadata`; Google `removed` covers "deletion or loss of access" ([changes ref](https://developers.google.com/workspace/drive/api/reference/rest/v3/changes)). | A revoked grant must make clients drop local copies. Our audit log can't express "user X lost read on folder Y". |
| **Stable item IDs; rename/move by ID** | Graph `parentReference` has no path, so clients track ids ([delta](https://learn.microsoft.com/en-us/graph/api/driveitem-delta)). | We already have UUIDs; expose them on DAV (`oc:fileid`-style) and in the feed. |
| **Strong per-item ETag** | Graph `eTag` = metadata+content; `cTag` = content only ([driveItem](https://learn.microsoft.com/en-us/graph/api/resources/driveitem)). RFC 9110 strong comparison. | Must be unique across items: fixes the reuse hazard. |
| **Recursive folder ETag** | Graph: on folders, "cTag value is modified when content or metadata of any descendant of the folder is changed" ([driveItem](https://learn.microsoft.com/en-us/graph/api/resources/driveitem)). The same page's table says cTag "isn't returned if the item is a folder", so Graph contradicts itself. Nextcloud's client depends on propagation ([admin manual](https://docs.nextcloud.com/server/stable/admin_manual/configuration_files/external_storage_configuration_gui.html)). | Cheap "did anything change below here". Costs a write per ancestor per change on one SQLite writer. Fine at homelab scale, but measure. |
| **Content hash** | Graph: only `quickXorHash` is guaranteed; `sha256Hash` "isn't supported" ([hashes](https://learn.microsoft.com/en-us/graph/api/resources/hashes)). Dropbox `content_hash`: SHA-256 per 4 MiB block, then SHA-256 of the concatenation ([content hash](https://docs.dropboxapi.com/dropbox-api/docs/technical-reference/content-hash)). | We already store a SHA-256 of the plaintext. Expose it. |
| **Chunked resumable upload** | Graph upload sessions: 320 KiB multiples, `nextExpectedRanges`, `if-match` → 412 ([createUploadSession](https://learn.microsoft.com/en-us/graph/api/driveitem-createuploadsession)). [tus 1.0](https://tus.io/protocols/resumable-upload). IETF [resumable upload draft](https://datatracker.ietf.org/doc/draft-ietf-httpbis-resumable-upload/). Nextcloud chunking v2. | **Already have it** (REST upload sessions with expected revision). |
| **Conflict semantics** | If-Match/412 (RFC 9110 §13.1.1). Graph `conflictBehavior` fail/replace/rename. | We have revision-conditioned publish. We need a documented conflict-copy naming rule for clients (e.g. `name (conflict from <device> <date>).ext`). |
| **Client mtime preservation** | Graph `fileSystemInfo` is "File system information on client. Read-write", and is settable in upload sessions ([driveItem](https://learn.microsoft.com/en-us/graph/api/resources/driveitem), [uploadable properties](https://learn.microsoft.com/en-us/graph/api/resources/driveitemuploadableproperties)). | Without it, every sync tool sees upload time, and bisync can't order changes. |
| **Push / long-poll** | Dropbox `longpoll` (30–480 s); Graph socket.io channel ([driveItem methods](https://learn.microsoft.com/en-us/graph/api/resources/driveitem)). | Optional. Poll the feed cursor every 30–60 s first. |

**Feed design sketch for KyDrive** (not code, a shape):
- **Table.** A `drive_changes(seq INTEGER PRIMARY KEY AUTOINCREMENT, workspace_id,
  item_id, kind, revision, created)` table, written in the same transaction as the mutation.
  The single SQLite writer makes `seq` monotonic for free.
- **Endpoint.** `GET /api/drive/changes?cursor=` returns, for each workspace the caller can
  read, the latest state per item since `seq`, plus tombstones.
- **Grants.** Grant changes emit `kind=access` rows, so clients rescan that workspace.
- **Retention.** Prune old rows. An expired cursor returns 410 and the client re-enumerates,
  as Graph does.
- **Shares.** Shared-with-me items need a per-user feed slice. Defer them to v2 rather than
  leak or miss.

## What KyDrive has vs lacks

| Primitive | Status | Source |
|---|---|---|
| Stable item IDs (UUID) | **Have** (not exposed on DAV) | `internal/drive/store.go:92-104` |
| Per-file revision | **Have** | same |
| Unique strong ETag | **Lacks**: reused across delete/recreate (probe-verified) | `internal/dav/fs.go:468` |
| Folder ETag / propagation | **Lacks** (no folder revision; workspace root has no `getetag`) | `internal/dav/fs.go:461-465` |
| Content hash | **Have** SHA-256 digest; **not exposed** on DAV | `drive_versions.digest` |
| Change feed / cursor | **Lacks** (`drive_events` is an audit log) | schema |
| Tombstones in a feed | **Lacks** (trash flag exists) | schema |
| Permission-loss events | **Lacks** | — |
| Resumable chunked upload | **Have** (REST, 24 h, revision-conditioned) | `internal/api/drive_handlers.go:25-76` |
| Conditional write | **Have**: REST expected revision; DAV If-Match → 412 (but see ETag hazard) | `internal/dav` |
| Client mtime | **Lacks** (PROPPATCH dropped) | `internal/dav/AGENTS.md` |
| Version history (loser kept) | **Have** | `drive_versions` |
| Recursive listing | **Lacks** on DAV (`Depth: infinity` 403); none on REST | `internal/dav` |
| Shared-with-me for sync | REST only; **lacks** on DAV | `internal/api/drive_shares.go` |
| Persistent locks | **Lacks** (memory, 1 h) | `internal/dav/AGENTS.md` |
| Per-user native-app credential | **Partial**: DAV app passwords only; REST tokens are per-workspace | `internal/api/server.go:334-364` |
| Push / long-poll | **Lacks** | — |

## Sign-in design for any native client

**Constraints from KyIdentity.**
- No refresh tokens, so a native app holding only KyIdentity tokens re-authenticates every
  15 min.
- No device grant.
- Exact redirect matching. RFC 8252 says the AS "MUST allow any port to be specified" for
  loopback redirects ([RFC 8252 §7.3](https://www.rfc-editor.org/rfc/rfc8252#section-7.3));
  KyIdentity deliberately does not. A loopback client must register fixed ports that may be
  taken.
- If KyIdentity ever adds refresh tokens for public clients, RFC 9700 requires them to be
  sender-constrained or rotated
  ([RFC 9700 §2.2.2](https://www.rfc-editor.org/rfc/rfc9700#section-2.2.2)).

**Recommended design: KyDrive mints its own per-device sync credential.** KyIdentity remains
the only place a human proves who they are.

1. **Start.** The client calls an anonymous `POST /api/devices/link` with `name=<hostname>`
   and `platform`. KyDrive returns `device_code` (secret, poll-only), a short `user_code`,
   `verification_uri`, `interval` and a 10-min expiry. This is RFC 8628's shape
   ([RFC 8628](https://www.rfc-editor.org/rfc/rfc8628)) implemented by KyDrive, not
   KyIdentity, much like Nextcloud's Login Flow v2.
2. **Approve.** The client opens `verification_uri?user_code=…` in the browser. The user
   signs in to KyDrive through normal KyIdentity OIDC, sees "Link *Yoshi-laptop* (Windows)
   to your KyDrive?" with the code, and approves.
3. **Collect.** The client polls with `device_code` and gets an opaque random token, once.
   KyDrive stores only its hash, as with app passwords.
4. **Use.** The token authenticates **both** REST (change feed, upload sessions, per-user
   across all readable workspaces) and DAV, via `Authorization: Bearer`.
5. **Manage.** Each device is listed next to app passwords, with name, platform, created and
   last-used times. It is revocable individually, killed on deactivation, and expires after
   N days unused (e.g. 90).

**Why this design:**
- It needs **zero KyIdentity changes**. No any-port loopback or refresh tokens are needed,
  and MFA and policy stay in KyIdentity.
- The user never sees or copies a secret.
- It removes the per-workspace limitation of service tokens.

**Mitigating device-code phishing:**
- RFC 8628 §5.4 describes remote phishing, where an attacker gets a victim to approve the
  attacker's code ([RFC 8628 §5.4](https://www.rfc-editor.org/rfc/rfc8628#section-5.4)).
- Mitigation: the approval page shows device name, platform and the request IP's rough
  location, and requires an explicit click.
- Alternative: since the client and browser are on the same machine, use a one-shot
  loopback callback *to the client* carrying the code after approval. The loopback goes
  to KyDrive, not KyIdentity, so no KyIdentity redirect registration is needed.
- **Sender-constraining the token** (a device keypair, with the token bound to it):
  possible later. It is unconfirmed as worth the cost now.

**Compared with plain app passwords:**

| | App password | Device credential |
|---|---|---|
| Setup | User copies a shown-once secret into a client | Browser approve, no copy |
| Scope | DAV only | REST + DAV, per user |
| Revocation / deactivation | Yes / yes | Yes / yes |
| Works with third-party clients | Yes (rclone, Finder, Cyberduck) | Only via `bearer_token_command`-style hooks |
| Phishing surface | Pasted secrets leak | User-code phishing (mitigable) |

Keep app passwords for third-party clients. Use device credentials for anything Ky ships.

**Storing it on the client.** Use the OS store, never a config file.
- **Windows:** Credential Manager via `CredWrite`. The blob is at most 2,560 bytes, which
  is fine for an opaque token. Use `CRED_PERSIST_LOCAL_MACHINE`, not `ENTERPRISE`, which
  roams
  ([CredWriteW](https://learn.microsoft.com/en-us/windows/win32/api/wincred/nf-wincred-credwritew),
  [CREDENTIALW](https://learn.microsoft.com/en-us/windows/win32/api/wincred/ns-wincred-credentialw)).
- **macOS:** Keychain Services with `kSecAttrAccessibleAfterFirstUnlockThisDeviceOnly`, so
  the item doesn't migrate
  ([Keychain Services](https://developer.apple.com/documentation/security/keychain-services),
  [attribute](https://developer.apple.com/documentation/security/ksecattraccessibleafterfirstunlockthisdeviceonly)).
- **Linux:** the Secret Service API via libsecret. Lookup attributes are not secret, so
  don't put the token in them
  ([Secret Service spec](https://specifications.freedesktop.org/secret-service/latest/),
  [libsecret](https://gitlab.gnome.org/GNOME/libsecret)).
- **Go:** [zalando/go-keyring](https://github.com/zalando/go-keyring) (MIT) covers all three.

## Recommendation

**Before the early-November 2026 release** (cheap, no contract changes):

1. **Fix ETag reuse.** Use `"<file-id>-<revision>"` in `internal/dav/fs.go:468`, and add a
   regression test for delete → recreate → stale If-Match → 412. This is a bug fix to an
   existing promise (If-Match → 412), not a feature. **S.**
2. **Docs page "Desktop access".**
   - Windows map-drive (already in the README), Finder, GNOME and KDE, each with its known
     limits.
   - rclone `mount`/`nfsmount` and `bisync` with `vendor = other`, an app password, **config
     encryption plus `--password-command`** per OS, `--check-access`, `--max-delete`, and an
     honest note that conflict detection is size-only today.
   - A `KyDrive.cyberduckprofile`.
   - **S.** No server change.

**After the release, in order:**

3. **Server primitives** (M–L total, each independently useful):
   - stored client mtime (`X-OC-Mtime`/PROPPATCH `getlastmodified`, and in REST upload
     sessions);
   - checksum property from the digest;
   - folder ETags with propagation;
   - per-user change feed with tombstones and access rows.

   Ship mtime first: it upgrades rclone immediately. Checksums help rclone only if we also
  store a SHA-1 (its oc vendors read SHA-1/MD5) or rclone learns SHA-256; our own client
  can use the SHA-256 we already have.
4. **Device-link credential** (M), as designed above. It is useful to any client, including
   rclone via a token command, once DAV accepts bearer device tokens.
5. **Ky sync-lite** on Go + rclone packages + go-keyring (M): a sync folder per workspace,
   driven by the change feed, with the native shells described in "What a Ky client looks
   like". This is roadmap M7 phase 1.
6. **Native virtual files** (XL): cfapi first if users are mostly Windows, File Provider if
   Mac. Only once the feed has proven itself under sync-lite.

**Do not pursue:**
- Nextcloud/ownCloud client compatibility. It is L-sized; their release cadence becomes our
  breakage; their trademark terms limit how we can say it works; and it bends `AGENTS.md:104`.
  If Yoshi wants it anyway, amend that rule explicitly first.
- Seafile emulation.
- Syncthing.

## Sources

Microsoft
- https://learn.microsoft.com/en-us/windows/whats-new/deprecated-features
- https://learn.microsoft.com/en-us/iis/publish/using-webdav/using-the-webdav-redirector
- https://learn.microsoft.com/en-us/windows/win32/cfapi/build-a-cloud-file-sync-engine
- https://learn.microsoft.com/en-us/windows/win32/api/cfapi/nf-cfapi-cfregistersyncroot
- https://learn.microsoft.com/en-us/windows/win32/api/cfapi/ne-cfapi-cf_hydration_policy_primary
- https://learn.microsoft.com/en-us/windows/win32/api/cfapi/nf-cfapi-cfconnectsyncroot
- https://learn.microsoft.com/en-us/windows/win32/api/cfapi/nf-cfapi-cfsetpinstate
- https://github.com/microsoft/Windows-classic-samples/blob/main/LICENSE
- https://learn.microsoft.com/en-us/graph/api/driveitem-delta
- https://learn.microsoft.com/en-us/graph/api/resources/driveitem
- https://learn.microsoft.com/en-us/graph/api/resources/hashes
- https://learn.microsoft.com/en-us/graph/api/driveitem-createuploadsession
- https://learn.microsoft.com/en-us/graph/api/resources/driveitemuploadableproperties
- https://learn.microsoft.com/en-us/windows/win32/api/wincred/nf-wincred-credwritew
- https://learn.microsoft.com/en-us/windows/win32/api/wincred/ns-wincred-credentialw

Apple
- https://support.apple.com/guide/mac-help/connect-to-a-webdav-server-mchlp1546/mac
- https://github.com/apple-oss-distributions/webdavfs
- https://developer.apple.com/documentation/fileprovider/nsfileproviderreplicatedextension
- https://developer.apple.com/documentation/fileprovider/nsfileproviderenumerator/enumeratechanges(for:from:)
- https://developer.apple.com/documentation/fileprovider/synchronizing-files-using-file-provider-extensions
- https://developer.apple.com/documentation/fileprovider/nsfileprovidermanager/evictitem(identifier:completionhandler:)
- https://developer.apple.com/programs/
- https://developer.apple.com/documentation/security/keychain-services
- https://developer.apple.com/documentation/security/ksecattraccessibleafterfirstunlockthisdeviceonly

Linux / desktop
- https://gitlab.gnome.org/GNOME/gvfs/-/blob/master/daemon/gvfsbackenddav.c
- https://invent.kde.org/frameworks/kio/-/blob/master/src/kioworkers/http/http.cpp
- https://github.com/libfuse/libfuse/blob/master/LICENSE
- https://github.com/hanwen/go-fuse/blob/master/LICENSE
- https://specifications.freedesktop.org/secret-service/latest/
- https://gitlab.gnome.org/GNOME/libsecret
- https://github.com/zalando/go-keyring

rclone and FUSE layers
- https://github.com/rclone/rclone/blob/master/COPYING
- https://rclone.org/commands/rclone_mount/
- https://rclone.org/commands/rclone_nfsmount/
- https://rclone.org/bisync/
- https://rclone.org/webdav/
- https://github.com/rclone/rclone/blob/master/backend/webdav/webdav.go
- https://rclone.org/commands/rclone_obscure/
- https://rclone.org/docs/#configuration-encryption
- https://github.com/rclone/rclone/blob/master/librclone/README.md
- https://github.com/winfsp/winfsp/blob/master/License.txt
- https://github.com/macfuse/macfuse/blob/master/LICENSE.txt
- https://github.com/macos-fuse-t/fuse-t/blob/main/License.txt

Nextcloud / ownCloud / OpenCloud / Seafile
- https://github.com/nextcloud/desktop (COPYING, src/gui/connectionvalidator.cpp, src/libsync/discovery.cpp, src/libsync/configfile.cpp, src/common/vfs.cpp)
- https://docs.nextcloud.com/server/latest/developer_manual/client_apis/LoginFlow/index.html
- https://docs.nextcloud.com/server/latest/developer_manual/client_apis/WebDAV/basic.html
- https://docs.nextcloud.com/server/latest/developer_manual/client_apis/WebDAV/chunking.html
- https://docs.nextcloud.com/server/stable/admin_manual/configuration_files/external_storage_configuration_gui.html
- https://docs.nextcloud.com/server/stable/user_manual/de/desktop/macosvfs.html
- https://nextcloud.com/trademarks/
- https://nextcloud.com/pricing/
- https://github.com/owncloud/client (COPYING, changelog/7.1.0_2026-06-08)
- https://doc.owncloud.com/pdf/branded_clients/next_ownCloud_Branded_Clients_Manual.pdf
- https://owncloud.com/trademark/
- https://github.com/owncloud/ocis/blob/master/LICENSE
- https://owncloud.dev/apis/http/graph/spaces/
- https://owncloud.dev/apis/http/tus_upload/
- https://github.com/opencloud-eu
- https://github.com/opencloud-eu/desktop
- https://github.com/opencloud-eu/openvfs
- https://manual.seafile.com/latest/develop/data_model/
- https://github.com/haiwen/seafile
- https://github.com/haiwen/seafile-client
- https://github.com/haiwen/seafile-server

Other clients
- https://docs.cyberduck.io/protocols/profiles/
- https://github.com/iterate-ch/cyberduck/blob/master/LICENSE.txt
- https://mountainduck.io/buy/
- https://docs.mountainduck.io/mountainduck/interface/
- https://docs.mountainduck.io/mountainduck/sync/
- https://docs.mountainduck.io/protocols/webdav/
- https://github.com/syncthing/syncthing/blob/main/LICENSE
- https://docs.syncthing.net/specs/bep-v1.html

Reference sync APIs and specs
- https://github.com/dropbox/dropbox-api-spec
- https://docs.dropboxapi.com/dropbox-api/docs/technical-reference/content-hash
- https://developers.google.com/workspace/drive/api/guides/manage-changes
- https://developers.google.com/workspace/drive/api/reference/rest/v3/changes
- https://tus.io/protocols/resumable-upload
- https://datatracker.ietf.org/doc/draft-ietf-httpbis-resumable-upload/
- https://www.rfc-editor.org/rfc/rfc9110
- https://www.rfc-editor.org/rfc/rfc8252
- https://www.rfc-editor.org/rfc/rfc8628
- https://www.rfc-editor.org/rfc/rfc7009
- https://www.rfc-editor.org/rfc/rfc9700
