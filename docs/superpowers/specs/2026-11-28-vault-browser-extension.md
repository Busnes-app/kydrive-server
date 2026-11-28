# KyDrive vault browser extension

Draft for Q13 of the [whole-drive E2E decisions](2026-11-28-whole-drive-e2e-decisions.md).
Desktop is the first release client. This design covers Chrome, Firefox and Edge.

## Trust boundary

The server hosts file management. Its pages can request an open by opaque file ID; the
browser extension shows the authenticated account, vault, file and requested action in
extension UI before opening. With the extension absent, file management still works and
decryption is unavailable. KyDrive and the relay see ciphertext, routing metadata and
short-lived grants. The witness sees opaque membership receipts.

The signed extension contains the vault crypto, an isolated editor page and the editor
assets. The server page receives only coarse action results. Keep the vault identity,
decrypted content, clipboard handling and CryptPad session inside the extension and its
isolated editor frame. Derive and wrap per-vault keys using the same vault client source
as the native app. The extension presents plaintext only inside the editor it owns.

## Release verification

Use a release manifest signed by the release key embedded in the extension. Start with the
manifest embedded in each store package. A manifest binds its release and channel to the
hash and exact path of every executable editor resource, including scripts, workers and
WASM. Also record the compatible web release identifiers and approved management-page
script hashes. A release identifier reported by the web app is a compatibility hint.

Before opening a document, verify the release manifest and hash the actual packaged
editor resources. Start only the verified extension-owned editor page. Its policy permits
only packaged executable resources and a fixed allowlist of data endpoints. Disable
remote script loading, dynamic code evaluation, arbitrary navigation, external message
bridges and network fetches to file-derived URLs. Treat malformed or missing hashes as an
error and show it in extension UI. Pin the release channel and retain the newest accepted
manifest sequence so a stale manifest cannot silently roll back the editor. Document
store-signed key rotation and how a revoked release is disabled.

An extension cannot establish the integrity of a live server page by hashing filenames,
rehashing separately fetched responses, or observing DOM scripts after they execute.
Chrome and Edge Manifest V3 do not provide the same response-body hook as Firefox.
If Q13 requires direct plaintext handoff to the server-origin page, that mode is gated
on proving the bytes that actually executed, before any plaintext or key operation, on
each target browser. The extension-owned editor flow keeps that page outside the
decryption boundary. [Chrome webRequest](https://developer.chrome.com/docs/extensions/reference/api/webRequest) ·
[Firefox filterResponseData](https://developer.mozilla.org/en-US/docs/Mozilla/Add-ons/WebExtensions/API/webRequest/filterResponseData)

## Device link and editing

1. The extension generates a device key and starts a short-lived, single-use approval
   linked to the exact server origin and account. The server approval yields an
   extension-scoped credential. Use desktop approval with explicit device fingerprint
   confirmation when adding existing vault keys. Hosted recovery uses the recovery key
   or member re-add per Q12. The extension never sends a passphrase copy to the server.
2. On launch, use the extension-verified account and pinned witness to verify membership
   records and the file's signed revision. Verify the signed editor binding before
   decrypting any room capability. Give the editor frame the document and room grant for
   exactly that file. Keep the device credential and vault keys in the extension's
   privileged component. Restrict all editor messages by sender frame, session and type.
3. Exchange encrypted live room traffic only through the configured gateway. Recheck
   membership when renewing a grant. Close the frame and clear its session on revocation,
   logout or grant refusal.
4. Turn editor output into a signed, encrypted immutable version. Preserve pending
   changes on conflict and require a checked re-open before continuing under a new
   binding. Q7 sets the automatic export interval after measuring the client, relay and
   server at 1, 2 and 5 minutes. Measure peak RAM, CPU, storage and egress, save latency,
   recovery lag and errors with the same documents and edit pattern at each interval.
5. Q10 converts ODT and RTF to DOCX, ODS and CSV to XLSX, and ODP to PPTX on the device
   when editing is requested. Show the formatting-loss warning before conversion.
   Render PDF for viewing only. Q14 starts with an empty vault catalogue.

## Gates before enabling browser decryption

- Prove store-packaged editor start and live DOCX/XLSX/PPTX co-editing on Chrome, Firefox
  and Edge. Package size, CryptPad and converter licensing, WASM and worker policy, and
  store signing and update latency need evidence.
- Verify refusals for changed, omitted and extra executable assets, stale or wrong-channel
  manifests, unexpected script/worker/module loads, untrusted navigation and iframe
  messages. Instrument a substituted server page and confirm its entire traffic stays
  outside the extension's key and plaintext interfaces.
- Exercise link cancellation, wrong account/origin, revoked device, rotation, reset and
  lost-device recovery, including signed-state replay and concurrent tabs.
- Verify active lease renewal, removed editor closure, local conversion fidelity and
  recovery from the last export. Publish the Q7 cost results before fixing the interval.
