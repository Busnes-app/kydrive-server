# Content search index

## Purpose
Own the content search index: the tokenizer, the blind index database and the snippet maker. File text reaches this package only in memory; nothing it writes holds a word, a name, a file type or a blob ID.

## Ownership
Owns `<KY_DATA_DIR>/search/index.db` (schema, keys, queue, postings, scoring) and the text-to-token rules. Where text comes from (extraction, PDF conversion, the sealed text cache), the worker, permissions and HTTP/MCP shapes belong to `internal/api` (`search_index.go`); which files a user may search belongs to `internal/drive` (`SearchContent`).

## Local Contracts
- The index is its own SQLite file with its own connection (WAL, `synchronous=NORMAL`, `secure_delete=ON`), never the main database: the capsule caps one member at 64 MiB and the main pool has one connection. It is in no backup and can always be rebuilt; `Open` deletes and restarts an index built under another deployment key or `TokenizerVersion` (`meta.check`).
- Keys are `crypto.DeriveKey` subkeys of the deployment key: `kydrive:search-token` (term HMAC, first 8 bytes as a signed integer), `kydrive:search-doc` (`Ref` = HMAC of `blob:kind`, 16 bytes), `kydrive:search-check` (fingerprint). Term hashes are deterministic: equal words share a token across the drive, so frequency and co-occurrence patterns leak to whoever reads the file, words do not. Document rows carry no blob ID or kind, only `Ref`.
- Tables: `docs(ref, state 1 done / 2 failed, len, err code, attempts, updated)`, `postings(token, doc, tf) WITHOUT ROWID` (no index by document: `Keep` deletes in batched scans), `queue(file, first, not_before)` holding random file IDs until processed, `meta` (`check`, `cursor` = last drive change seq queued). `Put` and `Fail` drop the file's queue row in their transaction, so a crash repeats work and never loses it. A requeued file waits for its latest debounce but at most 60 s after it was first queued.
- Tokens (`scan`): per rune NFKD, combining marks dropped, lower case, `ß`→`ss`, final sigma→sigma; CJK (Han, Hiragana, Katakana, Hangul) runes are left as they are and index as overlapping bigrams (a lone one as itself); other tokens are runs of letters and digits, 2 runes to 64 bytes. No stemming, no stop words. A document keeps its first `MaxDocTerms` (50 000) distinct terms, frequencies capped at 255; a query its first `MaxQueryTerms` (8). Change any rule: bump `TokenizerVersion`.
- `Match` is AND of whole terms, scored BM25 (k1 1.2, b 0.75) with N, document frequency and average length over the refs it is given only, so documents the caller cannot read never match nor shape the order. `pending` counts given refs not yet settled (done, or failed `MaxAttempts` times).
- `MakeSnippet` reads at most 4 MiB of text, picks the first window with the most distinct terms, collapses white space and control characters, and returns `[start,end)` highlight offsets in UTF-16 code units of its text, overlapping ones merged. It is plain text, never markup.

## Verification
- `go test -race ./internal/search` (folding, CJK, caps, UTF-16 highlights, BM25 scope, queue debounce, key change rebuild, no plaintext in `index.db` or its WAL); `go test -fuzz=FuzzSnippet ./internal/search` for highlight ranges.

## Child DOX Index
None.
