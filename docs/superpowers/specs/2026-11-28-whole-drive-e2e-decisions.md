# Whole-drive end-to-end encryption: owner decisions

Date: 2026-11-28. Status: **decided** by the owner on 28 November 2026 (~07:30 UTC). Decided,
not built yet: the code on `master` still works as described under each "Now" below.

On 27 November the owner decided that the whole of KyDrive becomes end-to-end encrypted (E2E),
and that CryptPad replaces Euro-Office as the editor. The reasons given were that CryptPad's
editor services idled at 510 MiB, against 2,219 MiB for Euro-Office, and that users care more
about E2E than about speed. There are no live servers. On 28 November the owner answered the
fourteen questions this note put to them. The decisions come first. The option analysis
below them is kept as the record of what was weighed.

## Decisions (28 Nov)

"We say E2E and mean it." (owner, Q12)

| # | Feature | Decision |
|---|---|---|
| 1 | Search and sealed index | **1A.** Each client searches what it holds: names on every client, content on desktop only. The server index and hosted snapshots retire. |
| 2 | Previews, thumbnails | **2A.** Render on the client when a file is opened. No thumbnails at launch. |
| 3 | WebDAV | **3A.** Retire `/dav/`, app passwords and the rclone/Cyberduck guide. |
| 4 | Desktop and Android | **4B.** The desktop Go core first, compiled into Android with gomobile. Launch may go ahead with desktop only. |
| 5 | MCP / AI | **5A.** Retire `/mcp`, agent tokens and the agent skill. |
| 6 | Shares, links, guests, KyPost | **Groups A:** a manager's client expands a group into its members. **Guest links A:** the link key sits in the URL fragment. **Organisation links are dropped.** **KyPost A:** a drop box only the owner can open; **viewing copies are dropped.** |
| 7 | Backup and restore | **7B**, with N between 1 and 5 minutes: clients export automatically, and a restore returns each document to its last export. N is set by a test, which is a gate (below). |
| 8 | Malware scanning | **8A.** No server-side scanning is promised. The device's OS scanner covers downloads. |
| 9 | Dedup and quota | **9A.** The workspace quota counts ciphertext, CryptPad history included. Dedup across vaults is lost. |
| 10 | Euro-Office | **Removed (10a yes).** Other formats are **converted on import, on the client** (below). PDF stays view-only on the device. |
| 11 | Vault caps | **11A.** Ciphertext uses the resumable upload path. The ordinary 10 GiB file limit, quota, trash and retention rules apply. |
| 12 | Key recovery | **12A + 12C** on the hosted service: a recovery key shown once, and members re-adding a user's new key to shared vaults. **No passphrase copy (12B rejected).** Escrow exists only on Enterprise deployments, as the **Key Steward** (12D, renamed). |
| 13 | Editor and crypto code origin | **13B** plus a **signed browser extension.** The separate editor origin (13A) and server-served signed code (13C) are rejected. |
| 14 | Zip, handover, migration | **14A.** Zip on the client. Handover only by a member re-wrapping keys or by the Key Steward. **Start empty, with no migration tool.** |

### D7. The export interval is chosen by a test

- Clients export each open document automatically every N minutes, with N between 1 and 5.
- **Gate:** before N is fixed, measure the server, relay and client cost of automatic export
  at N = 1, 2 and 5 minutes. Pick N from those measurements and record them in the repo.
  The test has not been run (unproven).
- After a server restore, edits made since a document's last export are lost. The loss is
  at most N minutes.

### D10. Euro-Office goes. Other formats are converted on import

- Euro-Office, its routes, editor JWT, `internal/editor`, the Compose overlay and its
  provenance docs are removed at the switch.
- This supersedes the hosted Office qualification (owner, 2026-11-23, Priority 2) and the
  "Euro-Office is the editor" and "Euro-Office may run in production" lines in root
  `AGENTS.md` (`AGENTS.md:115-118` at `e589317`; the User Preferences section on `master`).
- When a user opens an ODT, ODS, ODP, RTF or CSV file for editing, the **client** converts it:
  ODT and RTF to DOCX, ODS and CSV to XLSX, ODP to PPTX. The server never converts.
- Conversion can lose formatting. The client tells the user so before it converts.
- PDF is view-only, rendered on the device.
- Which converter each client uses is not chosen yet, and its fidelity is unproven.

### D12. Key recovery: a recovery key, re-adding, and the Enterprise Key Steward

- On the hosted service, each user gets a recovery key, shown once (text and QR). The server
  holds the user key encrypted to it, and a key replacement must be signed by it (12A).
- Members of a shared vault can re-add a user's new key (12C).
- There is no passphrase copy on the server (12B rejected).
- The Key Steward is the vault spec's escrow (lines 318–340), renamed. It exists only on
  Enterprise deployments, never on the shared install.
- Clients tell users plainly, when the key is created: losing every device and the recovery
  key loses your personal files.

### D13. Decrypting code never comes from a server the user has to trust

- **Signed native apps** carry the editor and the crypto: desktop first, then Android and iOS
  (13B).
- **A signed browser extension** carries the crypto. The web app can open encrypted files
  only when the extension is installed. Without it, the web app manages files and metadata
  but can't decrypt.
- Rejected: a separate editor origin run by another party (13A), and code served by a server,
  signed or pinned (13C).
- `master` still differs: the merged vault browser staging (#211, `cmd/vault-browser`) serves
  WebAssembly vault code from a separately deployed HTTPS origin, which is 13A. Under this
  decision that path can't ship to users as it is.

#### The browser extension (design sketch)

**What it ships.**
- The crypto: age with the hybrid recipient, and hybrid signature checks for revisions and
  membership, from the same source as the native clients' core.
- A key store: the user and device keys, held in extension storage and never handed to the
  page.
- An editor bridge: it decrypts a document and passes it to the editor, and encrypts what the
  editor saves before it goes to the server.

**How it checks the page's code.**
- Each signed KyDrive release publishes the hashes of the web app's scripts and of the editor
  bundle. That hash allowlist reaches the extension only from signed releases: built into each
  extension release, or as a release manifest the extension checks against a signing key it
  carries. Which of the two is open question 3.
- Before it releases any plaintext or key operation to a page, the extension hashes the code
  the page has loaded and compares it with the allowlist. On any mismatch it refuses and
  says why.
- Targets: Chrome, Firefox and Edge.

**Open questions.**
1. Can an extension see and hash every script a page runs, including dynamically inserted
   scripts and WebAssembly, before that code reaches plaintext? Firefox can read response
   bodies (`webRequest.filterResponseData`). Chrome's Manifest V3 has no equivalent in its
   blocking API. Whether a content script can reliably cover this on Chrome and Edge is
   unproven.
2. Should the editor run in the extension's own page instead of the web app's, so the web
   app's code never touches plaintext at all? That would make the hash check less critical.
   Its cost and the editor's fit inside an extension page are unproven.
3. Who signs and publishes the extension in each store? Can store review delay a security
   fix? Is the allowlist built into the extension or shipped as a signed manifest, and how
   does it stay in step with server releases?
4. How does a user link the extension as a device and bring its keys in (scan from the
   desktop app, or the recovery key)?
5. Does the CryptPad editor bundle run under each store's content security policy for
   extensions? Unproven.
6. How does the web app show a user without the extension that encrypted files exist but
   can't be opened here?

### D14. Start empty

No install, the pilot included, is migrated. There is no migration tool.

## Analysis kept for the record

- Code references are to `origin/master` at `e589317` (#209, opt-in witnessed vaults and the
  CryptPad staging gateway).
- **Unproven** marks anything this note could not check in code, in merged docs or by running
  it.
- The CryptPad plan (`docs/superpowers/plans/2026-11-27-cryptpad-encrypted-kydrive.md`) and
  the browser vault work on branch `feat/vault-browser` are not merged. Where this note cites
  them, it says so.
- Today the server reads plaintext everywhere outside the opt-in vault routes: root
  `AGENTS.md:92` says "It is not end-to-end".

The opt-in vault design (`2026-11-22-e2e-vault-design.md`) already decided four things. A
vault gives up search, previews, editing, WebDAV, MCP, zip downloads and links (lines 28–38).
Keys use age's hybrid recipient (line 47). The multi-tenant service has no escrow (lines
17–22). An independent witness provides freshness (lines 208–276). This note takes those
decisions as given and applies them to the whole drive.

### Summary of the options

| # | Feature | Today (server reads plaintext) | Under whole-drive E2E | Recommendation |
|---|---|---|---|---|
| 1 | Search and sealed index | Name and content search on the server; keyed-hash index and hosted sealed snapshots | Server can match nothing | Search on the client: names on every client, content only on desktop. Retire the server index. |
| 2 | Previews, thumbnails | Server decodes images; Euro-Office converts Office files and PDFs | Impossible on the server | Render on the client when a file is opened. Encrypted thumbnails come later, for photos. |
| 3 | WebDAV | `/dav/` serves plaintext over app passwords | Ciphertext only, which is useless | Retire it. A local WebDAV bridge in the desktop app comes later, if users ask. |
| 4 | Desktop and Android | Sync over plaintext routes; neither app has vault code | Both need the full vault protocol | Desktop first. Android shares the Go core through gomobile. |
| 5 | MCP / AI | Server reads files and extracted text for agents | Impossible on the server | Retire `/mcp`. A local MCP server in the desktop app comes later. |
| 6 | Shares, links, guests, KyPost | Group grants, organisation links, guest links, KyPost intake | Group, link and server-written items need keys | Expand groups to members. Guest links carry the key in the URL fragment. Drop organisation links. KyPost drops attachments into a box only the owner can open. |
| 7 | Backup and restore | Restic bulk plus capsule, including vault ciphertext | Confidentiality is fine. Consistency with the witness and CryptPad is not | Back up only exported revisions. A restore returns each document to its last export. |
| 8 | Malware scanning | None exists | Still impossible on the server | Do not promise it. Rely on the OS scanner on the device. |
| 9 | Dedup and quota | No dedup except shared blobs on copy; quota from sizes | Quota still works on ciphertext. Copies across vaults need re-encryption | Count ciphertext, CryptPad history included. Accept losing cross-vault dedup. |
| 10 | Euro-Office | The editor, PDF text and Office thumbnails | Reads plaintext, so it is incompatible | Remove it outright at the switch: there are no live servers. |
| 11 | Vault caps | 32 MiB per revision, 1 GiB per vault, no delete path | Far below normal files (10 GiB) | Move ciphertext onto the resumable upload path. Workspace quota replaces the caps. Add trash and purge. |
| 12 | Key recovery | Immutable key enrolment; no recovery copy | Losing every device loses data | A recovery key, plus re-adding by other members. No passphrase copy on the hosted service. |
| 13 | Editor web code origin | KyDrive serves the SPA and the editor page | The operator could swap the code that handles keys | A separate origin run by an independent party, with published reproducible builds. Native apps later. |
| 14 | Zip, handover, migration | Server zips and hands over trees; a pilot is mentioned | Impossible on the server | Zip on the client. Handover only by a member or Enterprise escrow. No migration tool. |

The questions put to the owner are listed at the end (Q1–Q14), numbered as in this table. The
answers are in "Decisions (28 Nov)" above.

## 1. Search and the sealed search index

**Now.**
- `GET /api/drive/search` (`internal/api/drive_handlers.go:61`, `:349`) matches names and,
  unless `KYDRIVE_CONTENT_SEARCH=off` (`internal/config/config.go:464`), content.
- The indexer reads each blob (`internal/api/search_index.go:120-135`) and extracts Office and
  OpenDocument text in process (`internal/extract/extract.go:73`). It reads PDFs through
  Euro-Office (`internal/api/document_text.go:105`).
- The single-tenant index stores HMACs of words (`internal/search/index.go:192-198`). It leaks
  word frequency and shared-word patterns (root `AGENTS.md:92`).
- Hosted mode publishes one sealed SQLite snapshot per org (`internal/api/search_hosted.go:69-169`).
- The open search options study (`2026-11-15-content-search-options.md`) is still waiting for
  the owner's choice.

**What breaks.** The server sees neither names nor content, so it has nothing to index or
match. The whole server search stack becomes dead code, and so does the options study.

| Option | Cost | Memory | Security |
|---|---|---|---|
| A. Each client indexes what it holds: names everywhere, content on desktop (local SQLite FTS) | M, in the desktop core and Android | On the client only. The server indexer and its snapshots go | No index on the server: the current leakage disappears |
| B. A client builds an encrypted per-vault index and stores it as ciphertext. Other devices download it and query it locally | L: index merging across devices, and conflicts | Client RAM for the whole vault index (unproven at scale) | Only the index's size and update times leak |
| C. Name search only, on the client. Drop content search | S | None | Same as A |

**Recommendation:** A. Content search on Android and in the web view waits until a later
version.

**Q1.** Content search becomes desktop-only and covers only files the device has synced. Is
that acceptable at launch, or must content search work on every client, which means option B?

**Decided (28 Nov):** 1A, as recommended.

## 2. Previews and thumbnails

**Now.**
- Thumbnails come from the server decoding images (`internal/drive/thumbs.go:73`, files up to
  64 MiB, `:26`).
- Office and PDF thumbnails come from Euro-Office conversion (`internal/api/office_thumbs.go:20`,
  `:108`; dispatched at `internal/api/drive_preview.go:87-98`).
- Inline previews are served at `internal/api/drive_preview.go:101`.

**What breaks.** All three need plaintext on the server.

| Option | Cost | Memory | Security |
|---|---|---|---|
| A. Render on the client when the user opens a file. Store no thumbnails | S on desktop (OS viewers). M in a web view (decrypt in the browser) | On the client only | Nothing new leaks |
| B. The client makes a small thumbnail, encrypts it with the vault key and uploads it as a sidecar | M: sidecar format, quota, rotation | A little extra storage | The sidecar's size and existence leak (minor) |
| C. No previews at all | None | None | Same as A |

**Recommendation:** A now. Add B later for Android photo galleries.

**Q2.** Browsing a folder will show icons, not thumbnails. Is that acceptable at launch?

**Decided (28 Nov):** 2A, as recommended.

## 3. WebDAV

**Now.**
- `/dav/` (`internal/api/server.go:690`, `internal/api/dav.go:18`) serves plaintext files to
  app-password and device-token clients through `internal/dav/fs.go:67`.
- `docs/DESKTOP-ACCESS.md` documents rclone and Cyberduck.

**What breaks.** A third-party WebDAV client cannot decrypt, so the server could only hand it
ciphertext.

| Option | Cost | Memory | Security |
|---|---|---|---|
| A. Retire `/dav/` and app passwords | S, mostly deletion | Frees the DAV path | Removes a credential type |
| B. A localhost-only WebDAV bridge in the desktop app that decrypts locally | M | On the client | Plaintext stays on the device, but any local process can reach the bridge (needs a local token) |
| C. Keep `/dav/` serving ciphertext | S | None | No user value |

**Recommendation:** A, then B only if users ask for it.

**Q3.** May WebDAV, app passwords and the rclone/Cyberduck guide be removed?

**Decided (28 Nov):** 3A, as recommended.

## 4. Desktop sync client and Android app

**Now.**
- The desktop client (`Busnes-app/kydrive-desktop`, `origin/main` `863c81b`, not yet released)
  syncs over plaintext routes (`core/api/api.go:252-305`). It checks each download against the
  server's plaintext SHA-256 (`core/api/api.go:262-277`).
- Android (`KyDrive-for-Android`, `origin/main` `184ca41`) does the same
  (`app/src/main/java/org/kysecurity/drive/core/Drive.kt:47-150`; digest check at `:117`;
  `If-Range` on the digest at `:128`).
- Neither repository contains vault or age code. A search for `vault`, `filippo.io/age` and
  `mldsa` found none.
- The desktop app already signs device requests with `hybridsig` (`core/api/sign.go:19`). The
  vault routes require a signed device and refuse any browser `Origin` (`internal/api/vault.go:37`).
- Today's vault catalogue is flat: file ID and revision only. It has no folders, names, trash
  or change feed (`internal/drive/vault_documents_schema.go:6-20`). Sync uses the change feed
  and snapshot (`internal/api/drive_changes.go:28`, `:43`).

**What breaks.** Every sync path: listing, names, change detection, upload and download. The
server's plaintext digest (`internal/drive/blobs.go:19-20`, `:54`) also has to go, because it
reveals when two files are equal and confirms guesses about a file's contents.

| Option | Cost | Memory | Security |
|---|---|---|---|
| A. Write the vault protocol twice: in the Go desktop core, then natively in Kotlin for Android | L+L. Android needs a Kotlin age with the hybrid recipient and ML-DSA (vault spec lines 50, 56; unproven that one exists) | Normal | Two crypto codebases to review |
| B. Desktop Go core first, compiled into Android with gomobile | L + M | Larger APK; Go runtime on the phone (unproven size) | One implementation to review |
| C. Desktop only at launch; Android read-only or not shipped | L | Normal | Same as B |

All three options also need the server's vault catalogue to grow folders, encrypted names, a
change feed and trash. That is the "workspace becomes a vault" work in the CryptPad plan
(unmerged).

**Recommendation:** B.

**Q4.** Is it acceptable to ship Android through gomobile on the desktop core (option B)? And
can launch go ahead with desktop vault sync only, if Android is late?

**Decided (28 Nov):** 4B, and launch may go ahead with desktop only.

## 5. MCP and AI access

**Now.**
- `/mcp` (`internal/api/server.go:645`) gives agent tokens tools such as `search_files`,
  `read_file` and `write_file` (`internal/api/mcp.go:207-227`).
- `read_file` opens the blob on the server and returns its text (`internal/api/mcp.go:622`,
  `:679`).

**What breaks.** The server cannot read or write files for an agent.

| Option | Cost | Memory | Security |
|---|---|---|---|
| A. Retire `/mcp`, agent tokens and the agent skill | S | Frees the server path | Removes a credential type |
| B. A local MCP server in the desktop app, reading decrypted synced files | M | On the client | The AI provider then sees whatever the user lets the agent read. That is the user's choice, but say so |
| C. A server-side agent with an escrow-like key | L | — | Breaks E2E. Not compatible with the decision |

**Recommendation:** A now, B later.

**Q5.** May MCP be removed at launch? It is listed as a product feature in root `AGENTS.md:121`.

**Decided (28 Nov):** 5A, as recommended.

## 6. Share links, groups, guests and KyPost

**Now.**
- Shares grant people or groups a role (`internal/drive/shares.go:120`).
- Organisation links open for any signed-in org member (`internal/drive/links.go:29`, `:119`).
- Guest links take a secret and serve files to anonymous visitors (`internal/drive/guest.go:52`;
  public routes at `internal/api/guest.go:30-32`).
- On the hosted service, shared workspaces exist only as suite group workspaces (tenancy D10,
  `2026-11-20-tenancy-design.md:66`).
- KyPost saves mail attachments into My files and shares them by organisation link
  (`internal/api/kypost.go:384`, `:425`). Its viewing copies open in Euro-Office
  (`internal/api/kypost_view.go:35`, `:157`).
- A vault grants rank only through individual member wraps. "A group or a link holder cannot
  receive the vault key" (vault spec lines 117–119). Today's catalogue caps a vault at 64
  members (`docs/VAULT-INTEGRATION.md:177`).

**What breaks.**
- Group grants and group workspaces: a group cannot hold a key.
- Organisation links: they hold no key.
- Guest links: the server can no longer serve a file to a guest.
- KyPost: a server-written file has no member signature, and the vault spec requires one
  (lines 263–273).

| Option | Cost | Memory | Security |
|---|---|---|---|
| Groups A. A manager's client expands the group into its members and wraps the key to each. A group change waits until a manager's client is online | M | None | Every membership change rotates the key (`VAULT-INTEGRATION.md:178`). The 64-member cap applies |
| Groups B. Drop group grants; members only | S | None | Simplest |
| Links A. Guest links put a link key in the URL fragment, which the server never receives. The client encrypts a copy of the file to it | M | None | The link is a bearer key. The guest's browser runs code from wherever the page is served (see 13) |
| Links B. Drop guest and organisation links | S | None | No external sharing |
| KyPost A. KyPost encrypts each attachment to the user's public recipient into a drop box. The user's client verifies it, signs it and moves it into the vault | M on both sides | None | The mail server saw the attachment anyway. Contract C in `docs/KYPOST.md` changes on both sides |
| KyPost B. Drop save-to-drive and viewing copies | S | None | — |

**Recommendation:**
- Groups A, with D10 amended so that a group workspace is a vault whose members a manager's
  client maintains.
- Links A for guest links. Drop organisation links: within an org, a share does the same job.
- KyPost A, and drop viewing copies.

**Q6.** Three choices:
- (a) Expand groups into members (A), or drop group sharing (B)?
- (b) Keep guest links with the key in the URL fragment, or drop all external sharing?
- (c) Rework KyPost into a drop box, or drop save-to-drive?

**Decided (28 Nov):** groups A, guest links A, organisation links dropped, KyPost A with viewing copies dropped.

## 7. Server-side backup and restore

**Now.**
- Bulk backup is Restic over the sealed blobs. It already includes vault ciphertext
  (`internal/backup/bulk.go:227-231`). `TestBulkRestoresVaultCiphertext`
  (`internal/backup/bulk_test.go:714`) restores it.
- The capsule carries the database. `ResetAfterRestore` (`internal/store/sqlstore.go:78`)
  invalidates sessions after a restore.
- CryptPad's live history lives in the relay's own volumes
  (`deploy/vault-gateway/compose.yml:47-50`), outside the KyDrive backup.
- Restoring the witness is an open release gate: "Witness state rollback detection and
  coordinated restore are release gates" (`docs/VAULT-INTEGRATION.md:31-32`).

**What breaks.** Confidentiality does not break: backups hold only ciphertext. Consistency
does:
- A restored vault log is shorter than the head that devices and the witness hold, which is
  exactly what a rollback attack looks like. The vault spec's replay rule (lines 278–305)
  handles this, but it is not built.
- CryptPad history restored to a different moment from the catalogue can point at rooms that
  no longer exist.
- A backup cannot help a user who lost their keys (see 12).

| Option | Cost | Memory | Security |
|---|---|---|---|
| A. One backup barrier across the KyDrive catalogue, ciphertext and CryptPad volumes, with the witness reconciled after a restore (plan step 2.6, unmerged) | L | A backup-time copy of the relay volumes | Strongest. Most moving parts |
| B. Back up only KyDrive. Live CryptPad history is disposable: a restore returns each document to its last Office export, and clients export automatically every N minutes | M | None | Edits made after the last export are lost on restore |
| C. Drop server backup and rely on the clients' copies | S | None | Breaks the existing KyRecovery contract (`docs/RESTORE.md`) |

**Recommendation:** B. Build the vault-log replay from the vault spec either way.

**Q7.** After a server restore, what loss is acceptable for edits made inside CryptPad but not
yet exported? B loses up to N minutes, and the owner picks N. Or is a full cross-store barrier
(A) required?

**Decided (28 Nov):** 7B with N between 1 and 5 minutes, set by a cost test that is a gate. See D7.

## 8. Malware scanning

**Now.** There is none. A search of Go, Markdown and YAML for `clamav`, `virus`, `malware` and
`antivirus` found nothing.

**What breaks.** Nothing breaks. Under E2E, server-side scanning also becomes impossible for
good.

| Option | Cost | Memory | Security |
|---|---|---|---|
| A. State that there is no server scanning. Downloads to disk are scanned by the OS | S (docs) | None | As today |
| B. Scan in the desktop client before upload or after download | M, plus a dependency | On the client (unproven) | Gives little over the OS scanner |
| C. Scan at KyPost intake, before encryption | M | ClamAV-class RAM on the KyPost side (unproven) | Covers mail only |

**Recommendation:** A.

**Q8.** Can you confirm that no plan or marketing promises server-side malware scanning?

**Decided (28 Nov):** 8A, as recommended.

## 9. Dedup and quota accounting

**Now.**
- Each upload writes a new blob ID (`internal/drive/blobs.go:21`).
- The only sharing is a copy reusing its source blob (`internal/drive/move.go:116-135`).
- Quota sums version sizes (`internal/drive/store.go:295`; org pool `internal/drive/pool.go:77`),
  so a copy counts twice.
- Restic deduplicates the backups (`internal/backup/bulk.go:66`).
- Vault ciphertext already counts toward the org pool (`pool.go:77`), but not toward a
  workspace quota.
- CryptPad's history, blobs and rooms are not counted anywhere.

**What breaks.** The server still sees sizes, so quota still works on ciphertext. Two things
are lost:
- A copy across vaults has to be re-encrypted by a client, so blob sharing works only within
  a vault.
- Restic cannot deduplicate identical plaintext encrypted twice.

Age adds a small overhead per chunk plus a header per version. The size of that overhead was
not measured here (unproven).

| Option | Cost | Memory | Security |
|---|---|---|---|
| A. Count ciphertext in the workspace quota, and add CryptPad history to it through the binding's room and blob records | M | None | Sizes stay visible, as the vault spec accepts |
| B. Count KyDrive ciphertext only, and cap CryptPad history per document by trimming it after each export | M | None | Same as A. Trimming history loses old editor states (unproven whether this CryptPad version supports trimming) |
| C. Convergent encryption to keep dedup | M | Saves storage | Leaks file equality and confirms guessed files. Not recommended |

**Recommendation:** A, and accept that dedup is lost.

**Q9.** Should CryptPad's encrypted editing history count against the user's quota (A), or
be capped and trimmed outside it (B)?

**Decided (28 Nov):** 9A, as recommended.

## 10. Euro-Office retirement

**Now.**
- Euro-Office is the editor by owner decision (root `AGENTS.md:118`). Its hosted
  qualification is Priority 2 (root `AGENTS.md:115`; `2026-11-20-tenancy-design.md:1382`).
- The editor routes are `internal/api/drive_handlers.go:94-98`, with config built at `:642`
  and the callback at `:671`. The editor page CSP is at `internal/api/editor_page.go:20`.
- The Compose overlay pins the image with a 4 GiB memory limit (`docker-compose.office.yml:4`,
  `:10`).
- New files are blank DOCX, XLSX or PPTX opened in Euro-Office (`AGENTS.md:100`;
  `internal/api/drive_documents.go:15`).
- PDF text extraction and Office thumbnails also depend on it (sections 1 and 2).

**What breaks.** Euro-Office reads plaintext on the server side, so it cannot coexist with
whole-drive E2E.

| Option | Cost | Memory | Security |
|---|---|---|---|
| A. Remove it at the switch: routes, editor JWT, `internal/editor`, the overlay, provenance docs and the Priority 2 qualification | M, mostly deletion | Frees the 2,219 MiB idle measured on 27 Nov (owner's brief; the merged runbook records a relay peak of 512 MiB in one fixture, `VAULT-INTEGRATION.md:171`) | Removes a plaintext processor |
| B. Keep Euro-Office for a plaintext workspace class | — | Keeps both editors | Contradicts the whole-drive decision |
| C. Run both editors through a migration window | M | Both editors' RAM | Not needed: there are no live servers |

**Recommendation:** A. The 23 Nov Priority 2 decision and `AGENTS.md:115-118` must be
recorded as superseded.

Formats beyond DOCX, XLSX and PPTX lose in-browser editing. These include ODT, ODS, ODP, RTF,
CSV and PDF, which Euro-Office converts today (`office_thumbs.go:20`). Whether CryptPad edits
any of them is unproven.

**Q10.** (a) May Euro-Office and its Priority 2 qualification be dropped now? (b) Is it
acceptable that OpenDocument, RTF, CSV and PDF files open only on the device?

**Decided (28 Nov):** Euro-Office is removed (10a yes). Other formats are converted to DOCX, XLSX or PPTX on the client when opened for editing; PDF is view-only. See D10.

## 11. The vault caps

**Now.**
- `MaxDocumentBytes = 32 << 20` and `MaxVaultBytes = 1 << 30` (`internal/vault/document.go:12-13`).
- A vault version is a single request body of at most 32 MiB (`internal/api/server.go:725-727`).
- The 1 GiB limit counts every version ever stored (`internal/drive/vault_documents.go:180-190`).
- Nothing deletes vault versions except an org purge (`internal/drive/orgpurge.go:208`). So
  once a vault fills, it stays full.
- Ordinary files allow 10 GiB (`internal/config/config.go:371`), use resumable upload sessions
  (`server.go:731`), and have trash, purge and retention (root `AGENTS.md`). A personal
  workspace defaults to 10 GiB (`internal/drive/peer.go:25`).

**What breaks.** Under whole-drive E2E, these caps become the limits for every file and every
workspace.

| Option | Cost | Memory | Security |
|---|---|---|---|
| A. Ciphertext goes through the existing resumable upload sessions (age streams). The workspace quota and `KYDRIVE_MAX_FILE_SIZE` replace both caps. Vault versions get trash, purge and retention | M | Streaming: no whole-file buffer | The signed revision still commits the size and digest (`document.go:53`) |
| B. Keep a separate vault store and only raise the caps | S | The single-request body bounds memory and timeouts (unproven above 32 MiB) | Same |
| C. Keep the caps | None | None | Unusable as a drive |

**Recommendation:** A.

**Q11.** Do the ordinary limits (10 GiB per file, the workspace and org pool quota, the trash
and retention rules) apply unchanged to encrypted files?

**Decided (28 Nov):** 11A, as recommended.

## 12. Key recovery when a user loses every device

**Now.**
- A user's vault key enrolls once and is immutable: a different key returns a conflict
  (`internal/drive/vault.go:26-58`, the check at `:55-57`).
- The stored key is only the public recipient and the signing key (`internal/drive/vault.go:16-19`).
  No recovery copy exists on master.
- The vault spec designs a recovery key and a recovery copy held by the server (lines
  307–316). It allows no escrow on the hosted service (lines 17–22) and Enterprise-only escrow
  (lines 318–340).
- Handover of a leaver's files runs on the server (`internal/drive/handover.go:98`).
- The unmerged `feat/vault-browser` runbook says browser key recovery "remain[s] open".

**What breaks.** On the hosted service, a user who loses every device and has no recovery
means loses their personal data. Today the server cannot even accept a replacement key.

| Option | Cost | Memory | Security |
|---|---|---|---|
| A. A recovery key shown once (text and QR). The server holds the user key encrypted to it. A key replacement must be signed by the recovery key | M | None | Strong. Users lose recovery keys |
| B. A passphrase-wrapped user key on the server (Argon2id) | S | None | The operator can guess the passphrase offline. Weaker on the hosted service |
| C. Other members re-add the user's new key to shared vaults | S on top of A or B | None | Personal vaults still die |
| D. Enterprise escrow (already designed) | M | None | Single-tenant only |

**Recommendation:** A and C on the hosted service, plus D on Enterprise. Reject B for the
hosted service. Clients state the loss in plain words when the key is created (vault spec
line 315).

**Q12.** Is it acceptable that the hosted service tells users, without exception, that
losing every device and the recovery key loses their personal files? Or is a passphrase
copy (B) wanted despite offline guessing?

**Decided (28 Nov):** 12A and 12C on the hosted service, no passphrase copy, and escrow only on Enterprise as the Key Steward. See D12.

## 13. Where the editor's web code is served from

**Now.**
- KyDrive embeds and serves the SPA (`web/embed.go:10`, CSP at `internal/api/server.go:638`).
- KyDrive also serves the editor page (`internal/api/editor_page.go:8-20`).
- The vault API refuses browser requests (`internal/api/vault.go:37`), so on master only
  native clients can use vaults.
- The merged gateway serves no editor assets, only `/cryptpad_websocket` and `/blob/...`
  (`deploy/vault-gateway/gateway.mjs:14-15`). In the merged DOCX fixture the editor bundle is
  built locally outside the repository (`VAULT-INTEGRATION.md`, "DOCX integration fixture").
- The unmerged plan wanted a signed browser package. The owner cannot offer one.
- The unmerged `feat/vault-browser` work serves a WebAssembly vault site from "a trusted
  editor HTTPS origin with deployment control separate from KyDrive".

**What breaks.** Any code that decrypts in a browser is whatever its server sent on that
load. If KyDrive's operator serves it, the operator can swap it and read keys. The vault spec
names this risk (lines 40–43, 362).

| Option | Cost | Memory | Security |
|---|---|---|---|
| A. A static editor and vault site on a separate origin, run by a party independent of the KyDrive operator (as for the witness, vault spec Q5). Reproducible builds with published hashes, following the existing release reproducibility check (root `AGENTS.md`, Verification) | M plus a second operator | A static host. The relay stays on the KyDrive side | Moves the trust from the KyDrive operator to the site operator. A swap is detectable only by someone comparing hashes |
| B. No browser editing: bundle the editor into the desktop app, and later Android, shipped as signed app releases | L: the CryptPad assets inside a desktop web view | Each client carries the editor (the 510 MiB idle figure is a server figure; client cost unproven) | Strongest. Code comes from signed releases, not a server |
| C. KyDrive serves it with SRI, or a service worker pinned on first use | S | None | The operator can still swap the code on first load or by bypassing the worker. Not E2E against the operator |

**Recommendation:** A at launch, with the product copy saying the editor site's operator is
trusted. Plan B as the strong path.

**Q13.** Who operates the separate editor origin, and is it the same independent party as the
witness? Is it acceptable for the web editor to rest on that party's honesty until the native
editor (B) ships?

**Decided (28 Nov):** 13A and 13C are rejected. Signed native apps (13B) and a signed browser extension. See D13.

## 14. Zip downloads, handover and migration

**Now.**
- Folder zips are built on the server (`internal/api/drive_zip.go:15`).
- An org admin hands a leaver's tree to a successor on the server
  (`internal/api/drive_handlers.go:93`; `internal/drive/handover.go:98`).
- Root `AGENTS.md:104` mentions a pilot install with NAS-local snapshots.

**What breaks.** Both zip and handover need plaintext or keys on the server.

| Option | Cost | Memory | Security |
|---|---|---|---|
| A. Zip on the client. Handover only by a member re-wrapping keys, or by Enterprise escrow. No migration tool: start empty | S–M | None | — |
| B. A migration client for existing installs (plan step 3, unmerged) | L | — | Needed only if data must be kept |

**Recommendation:** A.

**Q14.** Can you confirm that no install, the pilot included, holds data that must be
migrated, so the switch can start empty?

**Decided (28 Nov):** 14A. Start empty, with no migration tool.

## Questions put to the owner (answered 28 Nov, see Decisions)

1. Desktop-only content search at launch (1A), or every client (1B)?
2. No thumbnails at launch (2A)?
3. Remove WebDAV and app passwords (3A)?
4. Android through gomobile on the desktop core (4B), and may launch go ahead with desktop only?
5. Remove MCP at launch (5A)?
6. (a) Expand groups into members, or drop group sharing? (b) Guest links with the key in the
   URL fragment, or no external sharing? (c) KyPost drop box, or drop save-to-drive?
7. Acceptable loss of unexported CryptPad edits after a restore (7B, N minutes), or the full
   barrier (7A)?
8. Confirm there is no promise of server-side malware scanning (8A).
9. Does CryptPad history count against quota (9A), or is it capped and trimmed (9B)?
10. (a) Drop Euro-Office and its Priority 2 qualification now? (b) Non-OOXML formats open only
    on the device?
11. Do the ordinary file size, quota, trash and retention rules apply to encrypted files (11A)?
12. A recovery key with no passphrase copy on the hosted service (12A+C)?
13. Who runs the separate editor origin? Is trusting it acceptable until a native editor ships?
14. Confirm that no install, the pilot included, needs migrating.
