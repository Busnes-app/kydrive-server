# objects

## Purpose
Stores KyDrive's named, immutable objects: blobs and the caches derived from them (thumbnails, document text, KyPost viewing copies). `Dir` keeps them as files in one directory. The hosted install's S3-compatible backend (tenancy spec R2) implements the same `Store`.

## Ownership
Owns the `Store`, `Draft`, `Object` and `Local` interfaces and the directory backend. Imports only the standard library.

## Local Contracts
- **Names.** A name is one path element that doesn't start with a dot. Callers choose names (blob IDs) and validate them first (`drive.BlobPath`). `Dir` refuses anything else with `fs.ErrInvalid`.
- **Publishing.** `Create` starts a draft; nothing is visible under any name until `Publish(name)`. `Publish` makes the bytes durable first, then replaces any object of that name. Blob names are always new UUIDs; cache entries are rewritten in place. After a failed `Publish` nothing new is visible. `Discard` is safe after `Publish`, so callers `defer w.Discard()`.
- **`Dir` writes files as KyDrive always has.**
  - A draft is a `.upload-*` temp file (mode 0600) in the store's directory, so `drive.SweepOrphans` finds what a crash left.
  - `Publish` fsyncs the file, renames it, then fsyncs the directory. If that last fsync fails, the new file is removed.
  - `Remove` deletes the named files, ignores missing ones, and fsyncs the directory.
  - The directory is created on the first `Create`.
- `Open` returns an `Object` (`ReaderAt`, `Size`, `Close`), which is all the sealed-blob reader needs; a missing name is `fs.ErrNotExist`. `OpenFile` wraps a file outside any store (resumable-upload staging).
- `List` reports every entry, drafts included (dot names), with its last write time; a store never written to lists nothing.
- `Tree` (`DirTree`) is a set of Stores removed whole: resumable-upload staging, one `Sub` per session. `Sub` names follow the object-name rule, and a bad one yields a Store whose every call fails. `List` reports each Sub with its last change; `RemoveAll` deletes a Sub and its contents, and a missing one is already gone. `DirTree`'s subs are directories of its root, so staging stays `<data>/staging/<session>/<segment>`.
- `Local` adds `Root()`, the directory itself. Only single-tenant maintenance jobs use it: legacy sealing, the KYB1 format upgrade (both rewrite a file in place), and Restic's bulk backup, which reads files.

## Verification
- `go test ./internal/objects` runs `contract` against `Dir` and `treeContract` against `DirTree`; every new backend must pass the same functions.

## Child DOX Index
None.
