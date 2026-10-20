# Development

[Product overview](../README.md) · [Standalone installation](../INSTALL.md)

Commands and unlinked file paths below are relative to the repository root.

## Local development

Requires Go matching `go.mod`, Node/npm, Docker for editor tests and Restic for bulk recovery checks. Restic is included in the shipped container.

```sh
make build-web
make build
KY_DATA_DIR=./data KY_DB_DRIVER=sqlite KY_APP_NAME=KyDrive ./kydrive-server
```

Choose a random `KY_ADMIN_PASSWORD` in a private environment file before starting, or leave it empty: the first start then writes a generated password to `<KY_DATA_DIR>/initial-admin-password` (mode 0600, never logged); delete that file after the first sign-in. Bootstrap requires password replacement at first login. Startup refuses any secret still holding an example `replace-with` placeholder, an unparseable number or boolean, and a `KY_ENV` other than `production` or `development`. Use the configured OIDC provider for staff; SCIM never supplies passwords. Production uses `KY_ENV=production`, HTTPS, an explicit durable `KY_SESSION_SECRET` and explicitly configured integrations. Read/write timeouts default to 5m, configurable with `KY_READ_TIMEOUT`/`KY_WRITE_TIMEOUT` between 15s and 15m. `KYDRIVE_MAX_FILE_SIZE` caps one resumable upload and `KYDRIVE_MAX_ZIP_SIZE` one folder zip download; both default to `10GiB` and take whole `MiB`, `GiB` or `TiB` up to `1TiB`.

## Verification

```sh
PATH=/path/containing/restic:$PATH go test -race ./...
go vet ./...
cd web && npm test && npm run build
python -m unittest discover -s deploy -p '*_test.py'
docker build --platform linux/amd64 -t kydrive-server:local .
```

The browser shell and actual Euro-Office were exercised through the native T3 collaborative browser. Evidence and limits are in `docs/ACCEPTANCE.md` and `UI-VERIFICATION.md`. Live identity, HTTPS, NAS storage, spreadsheet save/reopen and recovery drill evidence is recorded separately in `docs/PILOT.md`. Suite-wide one-prompt installation is not implemented.
