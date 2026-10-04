# Euro-Office pilot artifact verification — 2026-10-04

Selected amd64 artifact: `ghcr.io/euro-office/documentserver@sha256:beca380debb9b4eadb7e4c662d112a325c5df0f2179453a4affd8e6ba5d00562`.

[Successful release workflow 33166218952](https://github.com/Euro-Office/DocumentServer/actions/runs/33166218952) is a push of tag `v9.3.4-hotfix.1` at source commit `4ba0c0f202ae484eb202f24c59ddf5f4a7004161`. Its manifest logs copy that exact amd64 digest and publish release index `sha256:889e681923d2dcc8bdfb92fe128d10e185fcff880d302b6a0c0c7bf339499290`. A fresh registry inspection of the release tag contains the same amd64 digest. This links the tested image to the published Euro-owned release workflow; it is stronger than an image version label.

The registry also supplies BuildKit SLSA provenance in its platform attestation. The GitHub attestations API has no attestation for this digest (404); do not describe BuildKit metadata as independently signed GitHub provenance. No independent reproducible rebuild was performed. Build logs, registry manifests and provenance are retained under `~/.local/state/kydrive-pilot/` for inspection.

[Release source](https://github.com/Euro-Office/DocumentServer/tree/v9.3.4-hotfix.1), [submodule mapping](https://github.com/Euro-Office/DocumentServer/blob/v9.3.4-hotfix.1/.gitmodules) and [build workflow](https://github.com/Euro-Office/DocumentServer/blob/v9.3.4-hotfix.1/.github/workflows/build.yml) are under Euro-Office ownership. The tested distribution reports 9.3.4 Build 37 and carries AGPLv3. Required editing works without a proprietary extension. Upstream-derived names remain explicit; this is the Euro-Office distribution, not a separately deployed ONLYOFFICE product.

Recheck release ownership, source submodules and the exact workflow/digest when upgrading. Keep the artifact digest fixed during the pilot. Published workflow correspondence is established; independent reproducibility is not.
