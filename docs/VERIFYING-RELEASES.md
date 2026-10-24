# Verifying releases

[Product overview](../README.md) · [Deployment](DEPLOYMENT.md) · [Recovery and encryption](RECOVERY-AND-ENCRYPTION.md)

A KyDrive release is the container image `ghcr.io/busnes-app/kydrive-server:X.Y.Z` (or `X.Y.Z.N`
for a daily dependency-update release). This page shows how to check that an image was built by
this repository's release workflow from the tagged commit, and how to rebuild its binary yourself
and get the same bytes. Commands run from an empty directory unless they say otherwise.

## What is signed, and by whom

- `release-image.yml` builds each release from its tag for `linux/amd64` and publishes a SLSA
  build-provenance attestation for the image digest through GitHub's Sigstore integration. It
  refuses a commit on which the `test` CI job did not pass, refuses to overwrite a version tag that
  already exists, and verifies the attestation before it moves `:stable`.
- Every push to `master` publishes `:<commit sha>` only, attested by `ci.yml` on `master`. There is
  no `latest`.
- There is no long-lived signing key to download. The signature is tied to a workflow identity,
  and that identity is the thing you check:
  `https://github.com/Busnes-app/kydrive-server/.github/workflows/release-image.yml@refs/tags/v<version>`.
  `gh` fetches Sigstore's public trust root itself.
- Each image also carries an SPDX software bill of materials made by BuildKit. The SBOM sits in
  the image index and is **not** separately signed; trust it as far as you trust the digest you
  verified.

## Verify a release image

You need `gh` (logged in) and `docker` with buildx. Name the version yourself, and verify the
version tag, not `:stable`: tags can move, so the check resolves the digest first and then requires
the attestation to name the commit the release tag points at.

```bash
v=<release version, e.g. 0.4.3>
sha=$(gh api repos/Busnes-app/kydrive-server/commits/refs/tags/v$v --jq .sha) \
  && d=$(docker buildx imagetools inspect ghcr.io/busnes-app/kydrive-server:$v --format '{{.Manifest.Digest}}') \
  && gh attestation verify "oci://ghcr.io/busnes-app/kydrive-server@$d" --repo Busnes-app/kydrive-server \
       --cert-identity https://github.com/Busnes-app/kydrive-server/.github/workflows/release-image.yml@refs/tags/v$v \
  && [ "$(gh attestation verify "oci://ghcr.io/busnes-app/kydrive-server@$d" --repo Busnes-app/kydrive-server \
       --cert-identity https://github.com/Busnes-app/kydrive-server/.github/workflows/release-image.yml@refs/tags/v$v \
       --format json --jq '.[0].verificationResult.statement.predicate.buildDefinition.resolvedDependencies[0].digest.gitCommit')" = "$sha" ] \
  && echo "OK: ghcr.io/busnes-app/kydrive-server@$d is release v$v, built from $sha"
```

Then run that digest, not the tag: `KY_IMAGE=ghcr.io/busnes-app/kydrive-server@$d` in `.env`
([Deployment](DEPLOYMENT.md#nas-package)).

Any failure means: do not run that image. A different workflow, a branch instead of the tag,
another tag, or another repository all fail the `--cert-identity` or `--repo` check.

To read the SBOM:

```bash
docker buildx imagetools inspect ghcr.io/busnes-app/kydrive-server@$d --format '{{ json .SBOM }}'
```

## Rebuild the binary and compare

The binary embeds the web interface (`web/dist`), which is built from source in the image, not
committed. Releases after 0.4.3 are built with `-trimpath`, so the same commit, Go version, Node
version and version string give the same binary on any machine and in any directory. CI checks
this on every push. Images up to 0.4.3 recorded their build directory (`/app`) and Go's install
path in the binary, so a build elsewhere does not compare with them.

You need the Go and Node versions the Dockerfile names: Go from its `FROM golang:` line (the
command below fetches it through `GOTOOLCHAIN`), and Node from its `FROM node:` line on your
`PATH`.

```bash
v=<release version>
git clone --branch v$v https://github.com/Busnes-app/kydrive-server.git kydrive-v$v && cd kydrive-v$v
sed -n 's/^FROM node:\([0-9.]*\)-.*/Node \1 required; you have:/p' Dockerfile && node --version
(cd web && npm ci --no-audit --no-fund && npm run build)
go_version=$(sed -n 's/^FROM golang:\([0-9.]*\)-.*/\1/p' Dockerfile)
GOTOOLCHAIN=go$go_version CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -buildvcs=false -trimpath \
  -ldflags="-s -w -X github.com/Busnes-app/kydrive-server/internal/appversion.release=$v" \
  -o kydrive-server-from-source ./cmd/server
cid=$(docker create ghcr.io/busnes-app/kydrive-server@$d) \
  && docker cp "$cid:/app/kydrive-server" kydrive-server-from-image && docker rm "$cid" >/dev/null
sha256sum kydrive-server-from-source kydrive-server-from-image
```

The two hashes must match. `-buildvcs=false` is needed only because a clone has a `.git`
directory; the image build context excludes `.git`, so Go stamps nothing there. `podman` works in
place of `docker` for the last two commands. The image layers around the binary (Alpine, CA
certificates, time zones, Restic, the whiteboard fonts) are not rebuilt bit for bit; the binary is
what you are checking.
