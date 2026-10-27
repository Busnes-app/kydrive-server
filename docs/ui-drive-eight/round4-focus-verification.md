# Round4 focused navigation verification

**FAIL — Appearance still clips 0.1875 CSS px of its outer ring on forward Tab in both phone schemes.** All 48 buttons fit; 46/48 rings fit. No fix or rebuild attempted after failure.

Frozen tested source `ad0ae38d0435c288cf8b7999d43c74b28446d94f`, branch `ui/drive-eight`. The canonical 40 stage03 screenshots remain source `8a23d753ba7aa0f67025efa1eca19bce3df63363`; they do not picture this source. No scoring or broader accessibility/behavior acceptance claim.

## Precise failure

At390×844/DPR2, My files active, real Tab from My files → Shared with me → Account → Appearance, then explicit500ms settle: row x0–390/y56–104; Appearance x259.890625–386.1875/y60–100; scrollLeft121.5. Outline2px + offset2px gives ring x255.890625–390.1875/y56–104. Right overflow0.1875px in both OSlight/dark. Document/main/header vertical scroll remains0 and h1/current destination remain My files. No rounding tolerance applied.

Settings & DB now fits: x250.984375–385.78125, outer ring x246.984375–389.78125 at scrollLeft373. Every backward stop fits. Desktop uses the effective240px app-header, not app-navs zero box.

## Exact per-destination metrics

Phone OSlight and OSdark measured identically. Button y60–100; ring y56–104; row x0–390/y56–104. All outlined focus stops use2px solid outline plus2px offset and :focus-visible. Full separate observations and effective clipping ancestors are in the JSON.

| Direction | Destination | Button x bounds | Outer ring x bounds | scrollLeft | Ring right overflow | Result |
|---|---|---|---|---:|---:|---|
| forward | My files | 16–113.296875 | 12–117.296875 | 0 | 0 | PASS |
| forward | Shared with me | 117.296875–265.59375 | 113.296875–269.59375 | 0 | 0 | PASS |
| forward | Account | 274.59375–377.390625 | 270.59375–381.390625 | 0 | 0 | PASS |
| forward | Appearance | 259.890625–386.1875 | 255.890625–390.1875 | 121.5 | 0.1875 | FAIL |
| forward | Recovery | 140.6875–248.984375 | 136.6875–252.984375 | 371 | 0 | PASS |
| forward | Settings & DB | 250.984375–385.78125 | 246.984375–389.78125 | 373 | 0 | PASS |
| backward | Settings & DB | 250.984375–385.78125 | 246.984375–389.78125 | 373 | 0 | PASS |
| backward | Recovery | 138.6875–246.984375 | 134.6875–250.984375 | 373 | 0 | PASS |
| backward | Appearance | 8.390625–134.6875 | 4.390625–138.6875 | 373 | 0 | PASS |
| backward | Account | 4.09375–106.890625 | 0.09375–110.890625 | 270.5 | 0 | PASS |
| backward | Shared with me | 117.296875–265.59375 | 113.296875–269.59375 | 0 | 0 | PASS |
| backward | My files | 16–113.296875 | 12–117.296875 | 0 | 0 | PASS |

Desktop1280×900, both OS schemes and directions: button x16–223, ring x12–227; effective header clip x0–240/y0–900; app-navs display:contents box0×0. All scrollLeft/document/main/header scroll values0.

| Destination | Button y bounds | Outer ring y bounds | Result |
|---|---|---|---|
| My files | 111.5859375–151.5859375 | 107.5859375–155.5859375 | PASS |
| Shared with me | 153.984375–193.984375 | 149.984375–197.984375 | PASS |
| Account | 312.15625–352.15625 | 308.15625–356.15625 | PASS |
| Appearance | 354.5546875–394.5546875 | 350.5546875–398.5546875 | PASS |
| Recovery | 396.953125–436.953125 | 392.953125–440.953125 | PASS |
| Settings & DB | 439.3515625–479.3515625 | 435.3515625–483.3515625 | PASS |

Every focus stop retained h1/current My files and document x/y0. Appearance → My files → Account → My files clicks passed in all four conditions (16 checks), without document scroll. Account checkbox observed ON in phone-dark and desktop-dark; authenticated before/after preference remainedON in every retained API check. Saved ky_theme remained absent.

## Commands and provenance

- PASS `npm --prefix web run build` (exit0; existing node_modules/caches). Vendor/TypeScript/Vite completed;51.79s Vite build. Existing >500kB chunk warning and PLUGIN_TIMINGS retained in private log.
- PASS `GOMAXPROCS=2 go build -p 1 -trimpath -o /tmp/kydrive-r3-recreated-sgjfh9md/server-r4-focus ./cmd/server` (exit0; empty Go log). Builds were sequential.
- Qualification: startup/pinning helper exited1 only at its final expected-clean-VCS assertion, after owned restart/readiness and record publication succeeded. Actual binary metadata has vcs.revision=ad0ae38d0435c288cf8b7999d43c74b28446d94f and vcs.modified=true. An untracked reviewer temporary file was observed afterward; never read/modified. All631 tracked files outside Council evidence match pre-build hashes, tracked diff remains empty. No clean-VCS claim or rebuild.
- Binary SHA256 `6cc500894d2229877a3cf59e2e1e6767bb1a76388f54484f1c5cd250620eaef0`; served/dist index SHA256 `e6547a4fd7822c8a4e05d223c54e54bb3de9bef8153a289453c05e108a972f6d`. Exact index bytes present in binary; all230 dist files unchanged during native checks. Full hashes/build info in JSON.

## Owned process and private paths

- OldPID181143/startticks412713/exe server-r4-confirm/SHA2568f24ccca5e02c1548504658201487145b8b7e751eb9c02bece3572e424c6b6bb verified immediately before pidfd SIGTERM; graceful exit observed.
- Preserved binary `/tmp/kydrive-r3-recreated-sgjfh9md/server-r4-confirm-pre-focus` (0700); preserved record `/tmp/kydrive-r3-recreated-sgjfh9md/owned-process-r4-confirm-pre-focus.json` (0600). Original binary/record also remain unchanged.
- NewPID214795/startticks530677; binary `/tmp/kydrive-r3-recreated-sgjfh9md/server-r4-focus`; record `/tmp/kydrive-r3-recreated-sgjfh9md/owned-process-r4-focus.json` (0600); same http://127.0.0.1:5878. Server remains owned/running for parent.
- Same0700 fixture directory/data/keys/config; credentials0600. `environment-r4-focus.json` exactly matches recorded minimal `environment-r4-confirm.json`; no deployment environment inherited. No credential values published.

## Focused screenshots and immutability

- [Appearance failing focus probe](focus-evidence/round4-ad0ae38-phone-light-appearance-native.png):390×844 CSS,780×1688 pixels, nativeDPR2, OSlight, sourcead0ae38.
- [Settings passing focus probe](focus-evidence/round4-ad0ae38-phone-light-settings-native.png):same conditions; last nav destination.
- These two PNGs are behavioral probes; no redesigned batch/contact sheet/thumbnail. Canonical40PNG/manifests unchanged.
- All95 pinned current/preserved evidence files match before/after, including46 preserved initial artifacts checked against preservation hashes; old native focus screenshot also unchanged. All private credential/environment/key/original binary/record pins unchanged. Full before/after hashes in JSON.

## State, method and limits

- Native T3 preview_status first; one own incognito tab; real type/click login. No snapshot of filled/password/pending login; first snapshot after shell and zero password fields. Explicit500ms await at each stop; real Tab/Shift+Tab. Tab closed before report publication.
- Authenticated read-only API identity/workspaces/live-files/folders/trash match existing8a fixture and before/after exactly: same account/workspace/folder/four file IDs, digests, revisions, sizes and timestamps, preferenceON. No duplicate SQL/download/version/grant/token audit. Normal login/session/audit/idempotent existing personal-workspace lookups are allowed product side effects.
- Final bounded native diagnostics: no console entries or failed/HTTP≥400 requests. Runtime Chrome154, nativeDPR2, normal service-worker controller. These are focused checks, not full diagnostics or accessibility acceptance.
- Initial exploratory files?parent GET was ignored by the server and returned root files; excluded from state assertions. No folder-audit claim. Initial assertion stopped at the first Appearance failure; screenshot taken of that same state, then only planned matrix completed.
- No product/test/AGENTS edits, commits, agents, installs, audits, Go tests/race, Docker, concurrent compiles, full suites, other browser specs or forty-image recapture. No auth/CSP/CSRF bypass, session/database fabrication, new content/grants/tokens/links or destructive actions. No raw reviews/history/parent scores/galleries read; no scores inferred.
- DOX pass complete; AGENTS left unchanged because current contracts cover these evidence outputs and owner forbids edits. MySlop known401: no requests/posts.

Only owned repository outputs: this Markdown, round4-focus-verification.json and the two focus-evidence PNGs.
