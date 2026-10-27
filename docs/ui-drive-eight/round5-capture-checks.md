# Stage04 bounded capture checks

Frozen HEAD `c320daf27d06bfadce82225f615a0323fca9f9a6`, branch `ui/drive-eight`; one capture, no scoring, no recapture.

## Findings and limits

- All40 originals individually inspected with `view_image(detail=original)`. No capture defect observed; no gallery/resizing/contact sheets.
- Sampled idle fields/secondary/icon outlines consume existing `--ky-field-border` and exceed3:1 against measured fill/adjacent surfaces in Busnes light/dark at both widths. Icons changed36x36 to40x40px.
- Separate Account unselected `label.choice` still consumes `line-strong`:2.3169:1 light,3.4804:1 dark against its card/panel. Its native16px radio has `appearance:auto`, CSS border0; painted indicator contrast was not measured. Current-runtime card measurement only.
- Real Tab after500ms:10/10 sampled buttons fit,8/10 entire outer rings fit. Appearance retains0.1875px right clipping in both schemes. Prior48/46 result is parent-reported history, not rerun or a strict focus pass.
- Preview Close stays in title row at390/1280 in both schemes; links wrap to second row on phone. Share Escape, Preview Close and sampled row-menu Escape/outside click dismiss correctly.
- One shell navigation region; no horizontal overflow in native probes or any formal capture. Preference stays ON, saved theme absent/OS following. No content/grants/links/deletion/credentials/editor actions.

## Before/after computed contrast

Ratios below are border-to-fill / border-to-adjacent. Both sizes produced the same ratios. Idle, unfocused/unhovered; detached canvas resolves computed colors to sRGB bytes, composites alpha over actual fill, then standard WCAG luminance. Modal-underlay controls excluded. Native DPR2 measurements are distinct from formal DPR1 captures. No PNG sampling or all-theme/state claim.

| Control / surfaces | Light before | Light after | Dark before | Dark after |
|---|---:|---:|---:|---:|
| Search / field, page |2.3169 /2.1439|3.6538 /3.3809|3.4804 /3.8425|4.7801 /5.2775|
| Share person + selects / field, dialog |2.2947 /2.4799|3.3809 /3.6538|3.5920 /3.2534|5.2775 /4.7801|
| Refresh + row actions / panel |1.5679 /1.5679|3.6538 /3.6538|2.0509 /2.0509|4.7801 /4.7801|
| Sign out / sidebar or phone header |1.5573 /1.5573|3.3809 /3.3809|2.0681 /2.0681|5.2775 /5.2775|
| New secondary / panel |Not sampled|3.6538 /3.6538|Not sampled|4.7801 /4.7801|
| Share Done secondary / fill, footer |Not sampled|3.6538 /3.3809|Not sampled|4.7801 /5.2775|

All element/state/theme/background/border/bounds/ratio records and reproducible measurement expressions are in `capture-provenance-r5.json`. Ghost Trash intentionally has no outline and is excluded from the outline assertion. Native radio CSS values do not establish its UA-painted indicator contrast.

## Capture and fixture

- Committed `capture-round5.mjs` ran once with private `owned-process-r5.json`;40 PNGs and `manifest-04-after.json`,10 surfaces x2 sizes x2 OS schemes.
- Chromium156.0.8078.4 / Playwright1.64.1;1280x900 and390x844,DPR1,en-US,America/New_York; normal SW registration/controller, no saved `ky_theme`, initial viewport, pointer0,0 before500ms settling.
- Manifest complete;0 diagnostics,0 hovered buttons,0 overflow,all scroll origins0. Signed-out login screenshots precede credential fill; displayed admin/bullets are shipped placeholders and username autofocus is expected.
- Same retained recreated instance as stages02/03: same administrator/workspace/file/folder IDs and timestamps,3 live files +1 trash,revision1; exact declared bytes/hashes,empty Project materials,no grants/links/editor sessions/issued credentials. Live bytes/versions match prior retained provenance; trash digest/size/revision verified without restoring or decrypting separately. Relative ages naturally advanced to1hour.
- Read-only SQLite current-file/version join and nonsensitive user columns matched pre/post capture; preferenceON. Encryption key and private credentials hashes unchanged; mode0600 credentials/private directory0700.

## Build/source/preservation verification

- Reused installed dependencies and persistent caches. Sequential `npm --prefix web run build` then `GOMAXPROCS=2 go build -p 1 -trimpath -o /tmp/kydrive-r3-recreated-sgjfh9md/server-r5 ./cmd/server` both exit0. Vendor verifier passed; Vite>500kB warning retained, no compiler warning/error. No installs,full tests,Go tests/race/crypto,Docker,audit or concurrent compiles.
- `vcs.modified=true` recorded, not hidden. Frozen HEAD,tracked-byte/source inventory,driver,lock,dist,binary,served index and pre/post process pins verified. Only runtime change sincead0ae38 is theme.css. All948 prior tracked files,including317 tracked evidence files and every prior original/raw report/manifest/initial copy,retain hashes. No published bytes overwritten.
- Exactly380 sequential same-size Pillow/NumPy comparisons,at most2 decoded arrays+delta; changed_fraction<0.001 AND max_delta<=8.40 PNGs/40 decoded RGBA hashes unique,0 near duplicates. One audit execution.

## Execution deviations

- Read-only SQL query initially addressed digest on drive_files instead of joining drive_versions and failed before the planned stop. The following frontend build started despite that failure and briefly overlapped the old owned server. Old PID214795 was then reverified by exe/startticks/hash and gently stopped with SIGTERM. Frontend completed successfully; only afterward ran capped Go build and restart. No native/formal browser or concurrent compiles during builds. No data or product edits; no repeated build/capture.
- Unsupported native role locator syntax was corrected; oversized private JSON exec argv was replaced with temporary-file atomic rename. Neither required browser fallback,rebuild or extra capture.
- Environment inspection accidentally printed the disposable original bootstrap password in tool output; the current retained login credential was not printed. No secret is included in canonical evidence.

## Owned runtime for parent

- PID`258596`,startticks`625430`,exe`/tmp/kydrive-r3-recreated-sgjfh9md/server-r5`,SHA256`633a0ef4456b3192416e5b88a34388cc528e68bbaebba828c99d4a20554e1a7e`;origin`http://127.0.0.1:5878`. Reverified post-capture; server left running,all browser tabs closed.
- Private record `/tmp/kydrive-r3-recreated-sgjfh9md/owned-process-r5.json`;old record/binary remain `owned-process-r4-focus.json`/`server-r4-focus`. Environment reused verbatim from`environment-r4-focus.json`;credentials.json0600 retained privately. No old PID reused.

## Complete new evidence list

- `manifest-04-after.json` (driver),`inventory-v5.json` (same canonical keys),`near-duplicate-audit-v5.json`,`source-provenance-r5.json`,`capture-provenance-r5.json`,`round5-capture-checks.md`.
- No native probe PNGs added; no quality/control-evidence mix.40 formal originals:

- `screenshots/kydrive-04-after-login-desktop-light.png` — SHA256`1af48eba21fe36b5f159757119f58543a46a62f6ba2513f756172a9cfdc1c9f7`; 1280x900.
- `screenshots/kydrive-04-after-files-desktop-light.png` — SHA256`052a484da192a7310fff808881c67356c156d627af08d6f95ce78c332265cb2f`; 1280x900.
- `screenshots/kydrive-04-after-folder-desktop-light.png` — SHA256`d3e4687986e4059d23ef70bff2de1226253e683b0b179771a70cd002236a9f6c`; 1280x900.
- `screenshots/kydrive-04-after-new-folder-desktop-light.png` — SHA256`fc9b6c813ace200e25cad6dd77acb2df4a5b928d12195eae6ee543787c6a4315`; 1280x900.
- `screenshots/kydrive-04-after-preview-desktop-light.png` — SHA256`10959554868801732422d8c97e15ccae0d6b0a8ef15ec29714ec14eeb6cec9fb`; 1280x900.
- `screenshots/kydrive-04-after-share-desktop-light.png` — SHA256`01313adf3e5e733c256e033d486811b6d37a24478a7e379f9ca5dc7c46c4cc49`; 1280x900.
- `screenshots/kydrive-04-after-versions-desktop-light.png` — SHA256`4e3f9fca447d3062c581719af8aa352e583056d36cc89873b5ab02437eb534e7`; 1280x900.
- `screenshots/kydrive-04-after-trash-desktop-light.png` — SHA256`0a9e16e083fd12dc1b7776f0aae718ab23c5e0bcb474f81e1b9a00815e5cf5f8`; 1280x900.
- `screenshots/kydrive-04-after-appearance-desktop-light.png` — SHA256`b8b123730b7ae9568b4851e5e1112ef7b1761f00e32c3df1f0a1d0c83b14f0e1`; 1280x900.
- `screenshots/kydrive-04-after-account-desktop-light.png` — SHA256`6859670bde323807ebb4f77e80a21b86e826bf2dfe22b2bf36c7b0c9980df83c`; 1280x900.
- `screenshots/kydrive-04-after-login-mobile-light.png` — SHA256`b0518f1d23b00c0b3e6f059569dcdf43b8476c3e3dd1c761f7872142a4453091`; 390x844.
- `screenshots/kydrive-04-after-files-mobile-light.png` — SHA256`3efd80db99d7ef8bd1e61ea7654d817c652dbb70d3688a00ad9f9ab33442fd87`; 390x844.
- `screenshots/kydrive-04-after-folder-mobile-light.png` — SHA256`13ecf9da983198ab1519123e99d36639143adf0d63f0a18eac20e0b675468cfa`; 390x844.
- `screenshots/kydrive-04-after-new-folder-mobile-light.png` — SHA256`4d8920e6ba083e713bc2cc33ae5efe9cbd3a50154f423344e2329d8e5f6ef5de`; 390x844.
- `screenshots/kydrive-04-after-preview-mobile-light.png` — SHA256`7becac086f743bd9c73aed03b8accdc1f8d30508e34ee7b995b2c32679d210cc`; 390x844.
- `screenshots/kydrive-04-after-share-mobile-light.png` — SHA256`830e212d655da2bcd2b4e8824254a2a881c4e5118f4992eb2fc3c3591bfafc4c`; 390x844.
- `screenshots/kydrive-04-after-versions-mobile-light.png` — SHA256`64a93280bb0f4164335b79230b01a7a02fec3b37aa60a06a43919dd2b406f8bb`; 390x844.
- `screenshots/kydrive-04-after-trash-mobile-light.png` — SHA256`9c532a36186547555d6c8566fdfe607d0f3f955653f7d40ef3cab4b1d6d4dccd`; 390x844.
- `screenshots/kydrive-04-after-appearance-mobile-light.png` — SHA256`50faa391a5ac1cfa858106f101ef759eed8defa4b26409b000b9e953b1301a5d`; 390x844.
- `screenshots/kydrive-04-after-account-mobile-light.png` — SHA256`90e62fb380096c9314a2341407e1ff55dca178d14a99b5418c455bf371e9d678`; 390x844.
- `screenshots/kydrive-04-after-login-desktop-dark.png` — SHA256`c7223bbbe49d527a1a9564221220b5b0fe27b8ae8edc13b71c4cc58a8982f15e`; 1280x900.
- `screenshots/kydrive-04-after-files-desktop-dark.png` — SHA256`6ec0f8c6e9d9027dfbb9d5ecafa55d9a03bbdad09b9cf42209644294f695f823`; 1280x900.
- `screenshots/kydrive-04-after-folder-desktop-dark.png` — SHA256`4e0760279984416fd2744441c0ae663a1789aa94c092dbd6f56309f4db5215e9`; 1280x900.
- `screenshots/kydrive-04-after-new-folder-desktop-dark.png` — SHA256`f753ccd58dfa137b63c91062bf7cc4b190da6ee1be2795bb57f9f93a41b7ff90`; 1280x900.
- `screenshots/kydrive-04-after-preview-desktop-dark.png` — SHA256`d0aab28ab13b706607b3d1dbc158c5ed70a13d780b196ab145fb1f62627efc0a`; 1280x900.
- `screenshots/kydrive-04-after-share-desktop-dark.png` — SHA256`1c2e379b8cc14524846bb20ca1f579b151166506843de5eb7c2eb16b28bebac3`; 1280x900.
- `screenshots/kydrive-04-after-versions-desktop-dark.png` — SHA256`dc1d36befa94160dad144c5c9369f363af6ae8964efcb2c68723962c8a20d46f`; 1280x900.
- `screenshots/kydrive-04-after-trash-desktop-dark.png` — SHA256`f9109a344ecd6aa41ede35a5c33bca08bfe9f345cae556e6a8c4ea02cc780038`; 1280x900.
- `screenshots/kydrive-04-after-appearance-desktop-dark.png` — SHA256`fc8e979a6fbae5823a34f85368ce4a31a8905f86ad61c2c2aca639025f2b3cf2`; 1280x900.
- `screenshots/kydrive-04-after-account-desktop-dark.png` — SHA256`d17a41ecc21c77d4069c6d16a091b47aff2c503f22ff90e1b1a101cce5349cb1`; 1280x900.
- `screenshots/kydrive-04-after-login-mobile-dark.png` — SHA256`4977b22500e4e7c4c1e3d340f1c1f372e71c38857821588f673af5fe331cc6b3`; 390x844.
- `screenshots/kydrive-04-after-files-mobile-dark.png` — SHA256`dadf0e44f44915498810636d3a9fe34903b2d5464d4c05a138dd4220ecdaab67`; 390x844.
- `screenshots/kydrive-04-after-folder-mobile-dark.png` — SHA256`0263e98fa49236030d0911bb3cf464f75b096810cc35976fb2bc41511ec85843`; 390x844.
- `screenshots/kydrive-04-after-new-folder-mobile-dark.png` — SHA256`91d7ec08e4af76dac7b50d02f3293ad01841fdfcc34355bf35147809c38f9464`; 390x844.
- `screenshots/kydrive-04-after-preview-mobile-dark.png` — SHA256`5a83c12744c751b4ca2143864480ad3e227b0aa5fe4b4089ef68c72fcb0d2e1a`; 390x844.
- `screenshots/kydrive-04-after-share-mobile-dark.png` — SHA256`cbc3e2cd64b000223526c7438801d5ef769330981972fbd917016f56f7d4c667`; 390x844.
- `screenshots/kydrive-04-after-versions-mobile-dark.png` — SHA256`eb878ee06f41ce7a9d1e589ff792ff1bb6cd48413a9789b0042d0b42366e3de8`; 390x844.
- `screenshots/kydrive-04-after-trash-mobile-dark.png` — SHA256`c2b73f146763bb063b3ff471ebcf01e69852d32fe515998bb77ac543a944f3e2`; 390x844.
- `screenshots/kydrive-04-after-appearance-mobile-dark.png` — SHA256`dbb28d6b810fea13022e334449fdd125241ee4f093086b4642bb83be1a327689`; 390x844.
- `screenshots/kydrive-04-after-account-mobile-dark.png` — SHA256`cde8fef69b44f3ebdfe89a4a8cf90da4889ffe94910ff3013c7fbe87a00033ea`; 390x844.

## Closeout scope

- No product/tests/AGENTS/driver edits,commits,agents or MySlop requests. Existing contracts intentionally unchanged: evidence-only batch under explicit freeze; parent owns any DOX wording update.
- Full required repository checks omitted by owner machine hardlock. No scores,full accessibility/behavior gate,ordinary/shared/OIDC/editor or production claims. Parent retains the sole bounded confirmation decision; no autofix or extra capture performed.
