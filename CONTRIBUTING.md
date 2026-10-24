# Contributing to KyDrive

[Product overview](README.md) · [Development](docs/DEVELOPMENT.md) · [Assurance case](docs/ASSURANCE-CASE.md)

## Reporting bugs and asking for features

Open a [GitHub issue](https://github.com/Busnes-app/kydrive-server/issues). Say which version
you run (the image tag, or `internal/appversion/VERSION` for a source build), what you did, what
you expected and what happened. Leave passwords, tokens, keys and file contents out of the issue.

**Security problems are not issues.** Report them privately through the repository's
**Security** tab → **Report a vulnerability**, as the
[Busnes-app security policy](https://github.com/Busnes-app/.github/blob/main/SECURITY.md)
describes.

## Making a change

1. Branch from `master` and open a pull request against `master`. `master` takes changes only
   through pull requests.
2. Read [AGENTS.md](AGENTS.md) and every AGENTS.md on the way to the files you change. They are
   binding: a change that alters a contract, workflow or verification updates the nearest owning
   AGENTS.md (and its parents' indexes) in the same pull request, and a changed procedure updates
   its topic guide under `docs/`.
3. The `test` CI job must pass before a pull request can merge. Run the checks in
   [Development → Verification](docs/DEVELOPMENT.md#verification) locally first.
4. Describe what changed and how you checked it in the pull request.

## Tests

New behaviour comes with a test that exercises it: Go tests next to the code, Vitest for web
logic, and Playwright (`npm run test:browser`) for browser flows. A security change also needs a
test for the attack it stops, not only the happy path; the tests named in the
[assurance case](docs/ASSURANCE-CASE.md#common-weaknesses-and-how-they-are-countered) show the
pattern. CI runs `go test -race ./...`, `npm test` and the browser tests on every push and pull
request.

## Coding standard

Contributions follow these style guides. CI enforces the parts a tool can check, so a style
failure is a red build, not a review comment.

| Language | Style guide | Enforced by (`test` job) |
|---|---|---|
| Go | [Effective Go](https://go.dev/doc/effective_go) and the [Go Code Review Comments](https://go.dev/wiki/CodeReviewComments) | `gofmt -l` (must print nothing), `go vet ./...`, and `go mod tidy` with no diff |
| TypeScript (`web/`) | The TypeScript compiler in `strict` mode | `tsc -b` in `npm run build` |
| Shared browser UI (`web/src/ky-ui/`) | Generated from Busnes-app/ky-ui; never edited here | `node src/ky-ui/check-vendor.mjs`, run before `npm test` and `npm run build` |
| Python (`deploy/`, `scripts/`) | [PEP 8](https://peps.python.org/pep-0008/) | No linter; `python -m unittest discover -s deploy -p '*_test.py'` runs the rendering tests |

Beyond what the tools check: keep to the comment density and naming of the file you are editing,
and prefer the standard library and the shared `ky-primitives` packages to new dependencies.
