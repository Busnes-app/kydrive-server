# Desktop access

KyDrive has no desktop app yet (roadmap M7). Until then, a computer reaches it over WebDAV at
`https://<your drive>/dav/`, signing in with your KyDrive username and an **app password**.
Create one per computer on your Account page. It is shown once, works only for WebDAV, and
can be revoked on its own: revoke it if the computer is lost.

| You want | Use |
|---|---|
| Browse and open files, online only | Your file manager: see [WebDAV in the README](../README.md#webdav) |
| A drive or folder with a local cache | `rclone mount` |
| A folder that works offline and syncs both ways | `rclone bisync` (small folders, see below) |
| A file-transfer app | Cyberduck, or Mountain Duck (paid) for a drive with offline files |

## Limits today

These apply to every method:

- A file's date shows when it was uploaded, not when you last changed it. Folders show no
  date.
- KyDrive offers no checksums over WebDAV, so sync tools cannot cheaply tell whether a file
  changed.
- **Shared with me** is not visible. Open shared items in the browser.
- Workspaces cannot be created, renamed or deleted from a desktop. Deleting a file moves it
  to the KyDrive trash.
- Saving over a file keeps the old one in its version history, so an overwrite can be
  undone from the browser.

## rclone

[rclone](https://rclone.org/) is free and runs on Windows, macOS and Linux. Version 1.75.2
was tested against KyDrive on Linux. That covered config encryption with a password command,
`mount` and `bisync`. The keychain commands below, and the Windows and macOS steps, follow
rclone's own documentation and were not tested here.

### 1. Protect the saved password

rclone's config file stores passwords [obscured, not
encrypted](https://rclone.org/commands/rclone_obscure/): anyone who can read the file can
recover your app password. Encrypt the config, and keep its key in your system keychain.

Store a key of your choice, then tell rclone how to fetch it:

- **Linux** (GNOME Keyring or KWallet):
  ```sh
  secret-tool store --label='rclone config' service rclone
  export RCLONE_PASSWORD_COMMAND='secret-tool lookup service rclone'
  ```
- **macOS:**
  ```sh
  security add-generic-password -a rclone -s config -w
  export RCLONE_PASSWORD_COMMAND='security find-generic-password -a rclone -s config -w'
  ```
- **Windows:** see [rclone's configuration
  encryption](https://rclone.org/docs/#configuration-encryption) for its PowerShell
  example.

Then encrypt the config:

```sh
rclone config encryption set
```

Put the `export` line in your shell profile. Without it, rclone cannot open the config.

### 2. Add KyDrive

```sh
rclone config create --obscure kydrive webdav \
  url=https://drive.example.com/dav/ vendor=other user=<username> pass=<app password>
```

Keep `--obscure`. Without it, rclone can mistake a KyDrive app password for an
already-obscured value and save a wrong one; every request then fails with
`401 Unauthorized`. The password also lands in your shell history: clear it afterwards, or
run `rclone config` and answer its questions instead.

Check it works. This lists your workspaces:

```sh
rclone lsd kydrive:
```

### 3a. A drive with a local cache: `rclone mount`

```sh
rclone mount "kydrive:My files" ~/KyDrive --vfs-cache-mode writes
```

- Saved files upload a few seconds after you close them (`--vfs-write-back`, default 5 s).
  Leave rclone running until uploads finish.
- Changes made elsewhere appear within 5 minutes (`--dir-cache-time`).
- This is not offline access. When the server is unreachable, the drive is too.
- **Windows** needs [WinFsp](https://winfsp.dev/). Mount to a drive letter:
  `rclone mount "kydrive:My files" K: --vfs-cache-mode writes`.
- **macOS:** use `rclone nfsmount` with the same arguments. It needs no kernel extension
  ([rclone nfsmount](https://rclone.org/commands/rclone_nfsmount/)).

### 3b. An offline folder: `rclone bisync`

bisync keeps a full local copy and syncs changes both ways each time it runs. Run it by
hand, or every few minutes from cron, a systemd timer or Task Scheduler.

The first run must include `--resync`:

```sh
rclone bisync "kydrive:My files/Projects" ~/KyDrive-Projects \
  --compare size,checksum --download-hash --resync
```

Every later run drops `--resync`:

```sh
rclone bisync "kydrive:My files/Projects" ~/KyDrive-Projects \
  --compare size,checksum --download-hash --resilient --recover --max-lock 2m
```

- **Keep `--download-hash`.** Without it, rclone compares sizes only and silently misses an
  edit that leaves a file the same size; the stale copy can later overwrite the newer one.
  The cost: **every run downloads every file in the folder** to hash it. Use bisync for
  small folders of documents, not for photos or video.
- **Conflicts.** When a file changed on both sides since the last run, both versions are
  kept as `name.conflict1` (KyDrive's) and `name.conflict2` (yours). Merge them and delete
  the one you don't want.
- **Large deletions.** A run that would delete more than half the files stops instead
  (`--max-delete`, default 50%).
- Don't edit files while a run is in progress. rclone's docs warn this can lose the edit.

## Cyberduck and Mountain Duck

Download [`KyDrive.cyberduckprofile`](KyDrive.cyberduckprofile) and double-click it. Then
open a new connection, choose **KyDrive**, and enter your drive's server name, your
username and an app password.

- [Cyberduck](https://cyberduck.io/) is free and browses, uploads and downloads.
- [Mountain Duck](https://mountainduck.io/) is paid. It uses the same profile, mounts
  KyDrive as a drive, and has a smart-sync mode that keeps files offline.

Neither app was tested here.
