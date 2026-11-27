# objects

## Purpose
Stores KyDrive's named, immutable objects: blobs and the caches derived from them (thumbnails, document text, KyPost viewing copies), and resumable-upload staging. `Dir` and `DirTree` keep them as files. `Bucket` keeps them in an S3-compatible bucket for the hosted install (tenancy spec R2); the server selects it for hosted startup; single-tenant stays on files.

## Ownership
Owns the `Store`, `Draft`, `Object` and `Local` interfaces and the directory backend. Imports the standard library and AWS's S3 client (`aws-sdk-go-v2/service/s3`, whose modules are all AWS's).

## Local Contracts
- **Names.** A name is one path element that doesn't start with a dot. Callers choose names (blob IDs) and validate them first (`drive.BlobPath`). `Dir` refuses anything else with `fs.ErrInvalid`.
- **Publishing.** `Create` starts a draft; nothing is visible under any name until `Publish(name)`. `Publish` makes the bytes durable first, then replaces any object of that name. Blob names are always new UUIDs; cache entries are rewritten in place. After a failed `Publish` nothing new is visible. `Discard` is safe after `Publish`, so callers `defer w.Discard()`.
- **`Dir` writes files as KyDrive always has.**
  - A draft is a `.upload-*` temp file (mode 0600) in the store's directory, so `drive.SweepOrphans` finds what a crash left.
  - `Publish` fsyncs the file, renames it, then fsyncs the directory. If that last fsync fails, the new file is removed.
  - `Remove` deletes the named files, ignores missing ones, and fsyncs the directory.
  - The directory is created on the first `Create`.
- `Open` returns an `Object` (`ReaderAt`, `Size`, `Close`), which is all the sealed-blob reader needs; a missing name is `fs.ErrNotExist`. `OpenFile` wraps a file outside any store: only the single-tenant KYB1 format upgrade, which reads a blob file and its staged copy in place (`drive/upgrade.go`).
- `List` reports every entry, drafts included (dot names), with its last write time; a store never written to lists nothing.
- `Tree` (`DirTree`) is a set of Stores removed whole: resumable-upload staging, one `Sub` per session. `Sub` names follow the object-name rule, and a bad one yields a Store whose every call fails. `List` reports each Sub with its last change; `RemoveAll` deletes a Sub and its contents, and a missing one is already gone. `DirTree`'s subs are directories of its root, so staging stays `<data>/staging/<session>/<segment>`.
- **S3 (`NewBucket`, `Bucket.Store(prefix)`, `Bucket.Tree(prefix)`):**
  - The endpoint must be a bare `https://host[:port]`; `http` is accepted only for a loopback host (tests). A region, bucket, access key ID and secret are required. Credentials come from the config struct, never from the SDK's environment or file chain.
  - A draft is a file in the bucket's private local spool directory, uploaded on `Publish`: one PUT up to `partSize` (64 MiB), else a multipart upload with parts sized to stay within 10,000, aborted on any failure, cancellation included (`TestS3PublishesInParts`). Either is atomic, so a reader never sees half an object. A process killed mid-upload leaves its parts until the bucket's lifecycle rule for incomplete multipart uploads removes them, so set one on the bucket.
  - `Open` is a HEAD; reads are ranged GETs of at least 4 MiB (`readAhead`), pinned to the ETag the HEAD saw, so an object replaced meanwhile fails instead of mixing versions.
  - A Tree's Sub is the key prefix `<prefix><name>/`; `List` dates a Sub by its newest object, and an empty Sub is not listed (S3 has no empty directories).
  - Errors name the operation, the key and the service's error code, never a request or the secret; NotFound and NoSuchKey are `fs.ErrNotExist` (`TestS3ErrorsNeverCarryTheSecret`).
- `Bucket.PurgePrefix` erases all accessible object versions and delete markers, repeatedly listing the first page so deletion never invalidates its cursor. Org erasure requires version-list/delete permissions; provider backup copies and unfinished multipart uploads remain provider-policy responsibilities.
- `ScopedStore`/`ScopedTree` select an org namespace through `Scope`/`ScopeTree`. Unscoped reads and writes fail closed; maintenance alone enumerates/removes all served namespaces by generated UUID. `Tree.RemoveAll` removes nested objects recursively, including an org's staging sessions and derived caches.
- Runtime keys are `cells/<home_region>/orgs/<org_id>/<kind>/<object>`; `internal/api/objects.go` resolves authoritative cells through the drive store. The private spool contains sealed drafts only.
- `Local` adds `Root()`, the directory itself. Only single-tenant maintenance jobs use it: legacy sealing, the KYB1 format upgrade (both rewrite a file in place), and Restic's bulk backup, which reads files.

## Verification
- `go test ./internal/objects` runs `contract` against `Dir` and `treeContract` against `DirTree`; every new backend must pass the same functions.
- With `KY_TEST_S3_ENDPOINT`, `KY_TEST_S3_BUCKET`, `KY_TEST_S3_ACCESS_KEY` and `KY_TEST_S3_SECRET_KEY` set, the same contracts run against S3. CI's `postgres` job starts SeaweedFS (pinned by digest; it checks signatures, so the wrong-secret test is real) from `.github/ci/seaweedfs-s3.json`.

## Child DOX Index
None.
