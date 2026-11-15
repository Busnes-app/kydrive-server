# Content search: options for prefixes and phrases, shared items and PDFs

Status: options study for the owner to choose from, 2026-11-15, against `master` at `dd9bba4` (after the file-type filter, PR #139).
No product code. Numbers marked **measured** come from throwaway prototypes run on Major
(listed under [Method](#method)); everything else is an **estimate** or cited from a primary
source. Anything a source did not confirm says **unconfirmed**.

## Summary

Today content search matches whole words only, in the current version of Office,
OpenDocument and text files, plus PDFs when Euro-Office is configured, and only in the
user's own workspaces. The index stores keyed hashes (HMAC) of words, never the words.

| Limit | Recommended option | New leakage to someone holding the disk | Extra index size | Code size (estimate) | New dependency |
|---|---|---|---|---|---|
| 2. Items shared with the user | **A. Widen the per-request candidate list** to what Shared with me shows | None | None | S, about 150 lines | No |
| 1. Prefixes (`conf*`) | **C. Expand against a sealed vocabulary** of whole words, then look up their hashes | None (one sealed file, its size only) | About 6 MB per 15 000 documents, sealed | M, about 250 lines with phrases | No |
| 1. Phrases (`"kernel module"`) | **C. Check the top candidates against the sealed text cache** | None | None | (in the line above) | No |
| 3. PDFs without Euro-Office | **B. PDFium compiled to WebAssembly, in process** (`go-pdfium`), with a wasm memory cap (process peak measured at about 330 MB plus 2 to 3 times the cap), a deadline and no file access | None (text goes to the existing sealed text cache) | None | M, about 150 lines | Yes: go-pdfium (MIT), PDFium (BSD-3 / Apache-2.0), wazero (Apache-2.0) |

**Recommended order:** shared items first (smallest, no new risk), then prefix and phrase
through the sealed vocabulary and text cache, then PDFium. Options that store prefix, pair or
position hashes on disk are not recommended: they make the index 1.3 to 3.6 times bigger and
leak more than today (measured below). If the drive outgrows option C, the next step is a
page-encrypted SQLite FTS5 index (option 1D), which is smaller and faster than today's index
but adds a second SQLite engine.

**Smallest first step:** make `drive.SearchContent` also search the live items in the
user's Shared with me list, by name and by content, with no index change (section 2,
option A).

How KyDrive would compare after all three steps:

| | Google Drive | OneDrive / SharePoint | Nextcloud full-text search | KyDrive today | KyDrive with recommendations |
|---|---|---|---|---|---|
| Prefix match in content | No: whole tokens only [g1] | Yes, trailing `*` [m1] | Yes, by default (`match_phrase_prefix`) [n1] | No | Yes, trailing `*`, 3+ letters |
| Exact phrase | Yes, quotes [g2] | Yes, quotes, plus `NEAR` [m1] | Yes; the last word is a prefix [n1] | No | Yes, within a time budget |
| Items shared with you | Yes [g2] | Yes, security-trimmed [m2] | Yes, from ACLs stored at index time [n1] | No | Yes, checked per request |
| Revoked access disappears | Unconfirmed | Unconfirmed | When the file is reindexed [n2] | Immediately (own workspaces) | Immediately |
| PDF text | Yes | Yes, parsed by default [m3] | Through Elasticsearch ingest-attachment (Apache Tika) [n3] | Only with Euro-Office | Yes, in process |
| What the index server holds | Plaintext, at Google | Plaintext, at Microsoft | Plaintext Elasticsearch index | Keyed hashes | Keyed hashes (unchanged) |

## What exists today

- `GET /api/drive/search` (`internal/api/drive_handlers.go` `driveSearch`) and MCP
  `searchFiles` (`internal/api/mcp.go`) call `drive.SearchContent`
  (`internal/drive/search.go`). It lists the workspaces where the user holds a role
  (`Workspaces`, `internal/drive/store.go:214`), decrypts every live name in them, and gives
  the live files there to the content matcher. Folder and file shares are not in that list,
  so **items shared with the user are found neither by name nor by content**.
- The index (`internal/search`, `<data>/search/index.db`) is **drive-wide**: the worker
  (`internal/api/search_index.go`) indexes every current file it can extract, whoever owns
  it. A document is keyed by `Ref` = HMAC of `blob:kind`; a word by the first 8 bytes of an
  HMAC. `Match` scores BM25 using only the refs it is given, so files the caller cannot read
  never match or shape the ranking.
- Text comes from `internal/extract` (Office Open XML, OpenDocument), plain-text files, or
  Euro-Office's `txt` conversion for PDFs. Each version's text is kept sealed in
  `<data>/text/<blob>`, and snippets come from there, after a fresh read check.
- Leakage today, as documented in `docs/RECOVERY-AND-ENCRYPTION.md`: someone with the disk
  but not the key sees which documents share words, roughly how long each is, and how often
  each hashed word occurs, and can guess the commonest words from frequencies.

### How much today's index leaks, measured

A frequency attack: the attacker knows plaintext word statistics from similar text (half
the test corpus) and sees only the hashed document frequencies of the other half. They guess
each hash as the word of the same frequency rank. This is the weakest published attack; real
attacks also use co-occurrence and do much better. Naveed et al. recovered over 60% of
records' values in most hospitals from deterministic encryption [p1], and Islam et al.
identified about 80% of queries from co-occurrence [p3].

| Index | Hashes seen | Right guesses among the 100 most frequent | Share of all word occurrences correctly read |
|---|---|---|---|
| Whole words (today) | 349 775 | 36 | 17.5% |
| 3-letter prefixes | 20 990 | 26 | 21.8% |

**Measured.** Rank matching alone recovers mostly function words ("the", "and", "of"). That
is a sixth of the running text, so the attacker learns little from it on its own. The danger
grows with any structure added on top: positions put those words in place, pairs chain them
into phrases, and prefixes reveal word stems and lengths.

## 1. Prefix and phrase matching

Corpus for every number here: 14 588 documents of 1 500 words, 25.1 million tokens, about
13.6 KB of text each, from `/usr/share/doc` and PDF manuals on Major (**measured**). The
50 000 and 500 000 columns scale linearly and assume every file is a text document of that
size, so they are an upper bound: photos and media add no index rows.

| Option | Index size per document | 50 000 files | 500 000 files | Index CPU per document | Prefix | Phrase | New disk leakage |
|---|---|---|---|---|---|---|---|
| Today: whole-word hashes | 10.1 KB | 0.5 GB | 5.1 GB | 5.4 ms | No | No | — |
| A1. Prefix hashes, 3 to 5 letters | 28.5 KB | 1.4 GB | 14 GB | 14.1 ms | Up to 5 letters | No | High |
| A2. Prefix hashes, 3 to 8 letters | 36.8 KB | 1.8 GB | 18 GB | 20.6 ms | Up to 8 letters | No | High |
| B1. Word-pair hashes | 32.2 KB | 1.6 GB | 16 GB | 18.3 ms | No | 2 words exact, longer approximate | High |
| B2. Positional postings (hashes plus positions) | 13.4 KB | 0.7 GB | 6.7 GB | 8.1 ms | No | Yes | Very high |
| C. Sealed vocabulary plus text-cache check | 10.1 KB (unchanged), plus vocabulary | 0.5 GB plus about 15 MB | 5.1 GB plus about 50 MB | 5.4 ms plus a set insert | Yes | Yes, top candidates | None |
| D. SQLite FTS5 in a page-encrypted file | 4.5 KB | 0.2 GB | 2.2 GB | 0.5 ms | Yes | Yes | Lower than today |
| D′. FTS5 trigram (substring) | 26.4 KB | 1.3 GB | 13 GB | 3.3 ms | Any substring | Yes | Lower than today |
| E. In-memory positional index, nothing on disk | 18.6 KB of RAM | 0.9 GB RAM | 9.3 GB RAM | 4.9 ms | Yes | Yes | None |

Sizes, CPU per document and E's heap are **measured** at 14 588 documents. The vocabulary
sizes at 50 000 and 500 000 are **estimates** (see C). The prefix and phrase cells for A
to E describe what each design can answer, not measured recall.

**Rebuild time** (after a restore, or when the index is lost) is not set by the index
format. The worker sleeps `max(50 ms, half the work)` after every file
(`drainSearch`, `internal/api/search_index.go`) to keep to about two thirds of a core. At
about 0.6 ms to extract a DOCX (**measured**, 2 000 generated documents) plus the CPU column
above, every disk-backed option rebuilds at about 51 to 72 ms per file. That is **about 45
to 60 minutes for 50 000 files and 7 to 10 hours for 500 000** (estimate from measured parts;
search works on the partial index meanwhile, and `contents.pending` says so). E must also
rebuild at every restart.

**Query time** at 14 588 documents (**measured**): today 20 to 30 ms for any query, of which
about 20 ms is reading the whole `docs` table, which `Match` does on every query
(`internal/search/index.go:353`). That part grows with the drive, to about 0.7 s at 500 000
documents (estimate). Prefix hashes (A2) answered `conf*` in 32 ms; FTS5 answered a phrase in
1 ms and `conf*` in 6 ms. Whatever is chosen, `Match` should look up postings first and read
only the documents they name, rather than scan every document row.

### A. Keyed hashes of word prefixes (A1, A2)

- **How:** also index HMAC("prefix" + the first 3, 4, … N letters) of each word. `conf*`
  becomes one hash lookup.
- **Reveals:** everything today's index does, plus each word's stem and length. Prefix hashes
  nest: every document holding the hash of `confi` also holds `conf` and `con`. An attacker
  can therefore link the chain to one word and read off its length. Short prefixes are few
  (20 990 three-letter ones in the corpus) and frequent, so they are easier to guess. The
  rank attack read 21.8% of occurrences from three-letter prefixes alone, more than from
  whole words (17.5%), and a guessed stem narrows every longer hash beneath it (not
  measured).
- **Costs:** 2.8 to 3.6 times the index size and CPU (measured). Rebuild is barely slower,
  because the throttle dominates it.
- **Code:** S (about 40 lines in `Count`, `Terms` and query parsing), plus a `TokenizerVersion`
  bump that rebuilds every index.
- **Risks:** the index outgrows a 4 GB homelab disk budget at a few hundred thousand files,
  and leakage grows for every user, even those who never use prefixes.
- **Verdict:** not recommended.

### B. Word pairs or positions (B1, B2)

- **How:** B1 also indexes HMAC(word₁ + " " + word₂) for each adjacent pair, so a phrase
  becomes an AND of its pairs. B2 stores each hashed word's positions in the document
  (varint deltas), so phrases and proximity are exact.
- **Reveals:** B1 adds a hashed bigram table. Bigram frequencies are a far stronger
  fingerprint than word frequencies, and chained pairs rebuild common phrases. B2 puts every
  recovered word in its place. Every word the frequency attack recovers can be read in
  order, and the gaps between them can be filled by language models (not measured). B2 is
  close to storing the document under a substitution cipher.
- **Costs:** B1 is 3.2 times today's size; B2 is 1.3 times (measured).
- **Code:** S to M, about 100 to 200 lines.
- **Verdict:** not recommended. The sealed text cache already holds positions, encrypted
  (option C).

### C. Sealed vocabulary for prefixes, sealed text cache for phrases (recommended)

- **How, prefixes:** keep the set of distinct indexed words, sorted, in memory, and seal it
  to `<data>/search/vocab` with the deployment key the way the text cache is sealed (AES-GCM,
  so only its size shows).
  - A query `conf*` (at least 3 letters) is expanded by binary search into whole words. Those
    words' hashes are OR-ed into the existing whole-word lookup.
  - Cap the expansion at, say, the 100 words with the most postings, and report
    `contents.expanded_truncated` when the cap cuts. That needs a per-word document count kept
    beside each word.
  - **Measured:** 529 296 distinct words took 6.2 MB as raw strings and 20 MB of Go heap as
    a sorted slice. `co*` expands to 6 284 words, `con*` to 2 531, `conf*` to 403 and `kern*`
    to 44, each in under 0.2 ms.
  - Vocabulary grows more slowly than the corpus (Heaps' law), so we estimate about 1 million
    words at 50 000 files and 3 to 4 million at 500 000: about 15 MB and 50 MB packed, or
    40 MB and 150 MB as a plain Go slice (estimate).
- **How, phrases:** run the phrase's words as today's AND query. Then take the best-scoring
  candidates (up to 200, within a 500 ms budget, like snippets today) and re-scan each one's
  sealed cached text for the words in order with the same tokenizer.
  - **Measured:** about 0.5 ms per candidate (13.6 KB of text, decryption not included).
    `"kernel module"` had 525 candidates and 174 true matches, checked in 269 ms.
  - `"the file"` had 9 370 candidates, which would take 4.6 s, so the budget matters. When the
    budget cuts, the response says how many were not checked (`contents.unverified`), as
    `pending` does today.
- **Reveals:** nothing new on disk. The sealed vocabulary shows its size, which tells about
  as much as today's count of distinct hashes. Plaintext words sit in server memory, which
  already holds plaintext text and names while it works.
- **Costs:**
  - Prefix: tens of MB of memory at most (estimate) and one extra sealed write per batch of
    index updates.
  - Phrase: CPU at query time, bounded by the budget.
  - Deleted words stay in the vocabulary until it is rebuilt from the text cache. Scanning
    the cached text takes about 0.5 ms per file: about 25 s for 50 000 files and about 4
    minutes for 500 000 (estimate). Do it in the hourly reconcile, at most daily.
  - A stale word only costs a wasted lookup, never a wrong hit: postings still decide.
- **Code:** M, about 250 lines (`internal/search`: vocabulary, sealing, query syntax `*` and
  `"…"`; `internal/api`: the verification pass) plus tests.
- **Risks:**
  - Phrases with common words are checked only in the top candidates, so a match ranked
    low can be missed, and the response says so.
  - Highlighting must use the same scan, so snippets show the phrase.
  - The query syntax change (`*`, quotes) must be explained in the UI help text and in the
    MCP tool description.
- **Verdict:** recommended. It is the only option that adds no disk leakage, keeps today's
  index format and needs no dependency.

### D. SQLite FTS5 in a page-encrypted database

- **How:** replace the hash index with a contentless FTS5 table (`content=''`, the text stays
  in the sealed text cache). FTS5 is compiled into the `modernc.org/sqlite` v1.62.0 we
  already use [s1], and its tokenizer gives phrases, `prefix*` and `NEAR` natively [s2].
  - FTS5 stores words in plain text, so the file must be encrypted page by page.
  - `modernc.org/sqlite` has only a read-only `fs.FS` VFS (its `vfs` package), so it cannot
    do this. `github.com/ncruces/go-sqlite3` v0.37.0 (2026-11-07, MIT) [s3] can. It ships
    `adiantum` and `xts` VFSs that encrypt every database, journal and WAL file in 4 KiB or
    512-byte blocks [s4][s5].
- **Reveals:**
  - From one disk image: the file's size, and no word frequencies. That is less than
    today's index.
  - From repeated snapshots (NAS snapshots, Restic of the data volume): which blocks changed,
    which did not and which reverted, because both VFSs are deterministic and unauthenticated
    [s4][s5]. It is also not tamper-evident.
- **Costs:** 4.5 KB per document, less than half today's 10.1 KB, and 0.5 ms of index CPU, a
  tenth of today's (measured, without encryption, whose cost is unmeasured). The trigram
  variant D′ adds substring search at 26.4 KB per document.
- **Code:** M to L. A second SQLite engine in the binary (ncruces translates SQLite to Go
  from WebAssembly with wasm2go [s3]), a rewrite of `internal/search` over FTS5, a new
  tokenizer configuration that must reproduce today's folding and CJK bigrams (FTS5
  `unicode61` does not do CJK bigrams), and migration.
- **Risks:** new dependency on a single-maintainer project, two SQLite engines, and leakage
  across snapshots that is harder to explain to operators than today's.
- **Verdict:** the upgrade path if C's query-time checks become too slow (for example
  phrase queries over drives far past 500 000 files). Not first.

### E. In-memory index only

- **How:** keep a positional index in RAM only, rebuilt from the sealed text cache at every
  start.
- **Reveals:** nothing on disk.
- **Costs:** 272 MB of heap for 14 588 documents (measured), so about 0.9 GB at 50 000 and
  9.3 GB at 500 000 (estimate), plus a full rebuild at every restart.
- **Verdict:** not viable on a 4 to 8 GB host.

## 2. Searching items shared with the user

The index is already drive-wide (see [What exists today](#what-exists-today)), so the work
here is choosing the candidates, not indexing. The rule to keep: a hit is returned only if
the user can read it at query time.

### A. Widen the per-request candidate list (recommended)

- **How:**
  - `drive.SearchContent` adds the user's `SharedWithMe` items
    (`internal/drive/shares.go:217`) to the workspaces it already searches. That covers shared
    files, and the live subtree of each shared folder walked under the share's root. Matching
    is by name and by content, as today.
  - Items already reachable through a workspace role are de-duplicated. A shared item's `Path`
    starts at the share root, never above it, and it carries the owner's display name (the
    same `WorkspaceName` the Shared with me list shows), never the owner's private folder
    names.
  - The matcher gets these files as further refs. BM25 statistics still come from the refs
    given only.
- **Propagation:** candidates are computed per request from the main database, so nothing
  needs to propagate. A revoked share, a removal from a group, a revoked or expired
  organisation link, a trashed item, a deactivated owner (`SharedWithMe` already drops
  them) or a move out of a shared folder all take effect on the next search.
  - Take the candidates in one read transaction.
  - Re-check every shared hit with `File(user, id, 1)` before answering, not only the 20 that
    get snippets. That costs about 200 small queries, tens of ms (estimate).
- **Link-only shares:** search what Shared with me shows, which is organisation links the
  user has opened (`drive_link_users`), while the link is live. Unopened links are not
  included, because the user does not know about them. Guest links have no account and do
  not apply.
- **Who gets it:** signed-in users. `SharedWithMe` already refuses service, device and KyPost
  peer contexts.
  - MCP stays unchanged unless the owner decides otherwise. Agent tokens limited to a folder
    must never widen, and WebDAV and MCP do not show Shared with me today (MCP spec,
    out-of-scope list).
  - Administration still grants nothing.
- **Reveals:** nothing on disk: no index change.
- **Costs:**
  - One more query plus a subtree walk per search. Name decryption grows with the shared
    items, at about 0.2 s per 50 000 names (README).
  - No rebuild.
  - The index already holds these files, except where `IndexFiles` skips them (none known).
- **Code:** S, about 150 lines in `internal/drive/search.go` plus tests.
- **Negative tests to write:** revoked share, group removal, revoked and expired link,
  unopened link, trashed item, deactivated owner, moved out of the shared folder, folder-
  limited agent token, service and device and peer contexts, an administrator who is not
  shared with, and BM25 scores that are unchanged by documents the user cannot read.
- **Verdict:** recommended first.

### B. Index per user

- **How:** postings carry the users who may read the document.
- **Reveals:** who can read what, to anyone holding the disk.
- **Costs:** every permission change reindexes (sharing a 10 000-file folder is 10 000 jobs).
  Size multiplies by readers.
- **Risks:** the index is stale between a revocation and the reindex, so it breaks the rule.
- **Verdict:** rejected.

### C. Index per workspace

- **How:** one index file per workspace.
- **Costs:** no saving: shares cross workspaces, so query-time trimming is still needed, and
  there are many more files to keep consistent. It only makes deleting a workspace's index
  simpler, which `Keep` already handles.
- **Verdict:** rejected.

### D. Access lists stored at index time (Nextcloud's design)

- **How:** each indexed document stores owner, users, groups and circles, and the query
  filters on them [n1].
- **Risks:** a share deletion only queues a metadata reindex [n2], so a revoked user can still
  match until it runs.
- **Verdict:** rejected for the same reason as B.

## 3. PDF text without Euro-Office

**Measured** on the 22 PDFs on Major (0.1 KB to 183 MB, including a 4 351-page manual), and
on 220 corrupted copies: 55 truncated, 165 with 1 to 100 random bytes changed. Word recall
is the share of `pdftotext`'s distinct words (3+ letters) that the candidate also produced;
whole-word search needs words split correctly.

| Candidate | Licence | Word recall vs pdftotext (12 parsed files) | Byte-flipped files extracted (all 165; a fixed 55 of them) | Decompression bomb (300 M characters) | Deep nesting (2 M levels) | Footprint |
|---|---|---|---|---|---|---|
| A. `ledongthuc/pdf` (pure Go) | BSD-3-Clause | median 78%, as low as 22% (words run together) | 54 of 165, 17 panics (recovered); 20 of 55 | 1.7 GB RSS, 48 s, no cap possible | Panic at depth 1000 (recovered) | 3.5 MB binary, about 8 MB RSS |
| B. `go-pdfium` WebAssembly (PDFium in wazero) | MIT; PDFium BSD-3/Apache-2.0; wazero Apache-2.0 | median 99% (69% to 100%) | 52 of 55, no crash or hang, at most 414 MB RSS | Stopped by the memory cap (wasm `unreachable`). Process peak grows with the cap: 564 MB at 128 MiB, 998 MB at 256 MiB, 1.86 GB at 512 MiB | Extracted | 15.4 MB binary, about 330 MB RSS, 3.4 to 6 s start |
| C. Poppler `pdftotext` (helper process) | GPL-2.0 or GPL-3.0 | reference | 161 of 165; 54 of 55 | 20 MB RSS, but 57 s: needs a time cap | Extracted, 175 MB RSS | Shared C libraries in the image |
| Today: Euro-Office `txt` conversion | AGPL-3.0 (separate service) | Not measured here; the MCP spec records it as lossier than `pdftotext` (dropped CJK text and a table column) | not measured | not measured | not measured | The whole editor service |

Truncated files: none of A, B or C extracted any (all refused cleanly). PDFium starts in 3 to 6 s
per process, so it ran on a fixed third of the byte-flipped files only.

### A. Pure Go library in process

- **Candidates (primary sources):**
  - `github.com/ledongthuc/pdf`: untagged, latest `v0.0.0-20261020125853-5e46805f718e`
    (2026-10-20), BSD-3-Clause, a fork of `rsc.io/pdf`. Open issues report missing spaces,
    failing AES-128, missing XObject text and CJK crashes [f1].
  - `rsc.io/pdf` v0.1.1 (2018, archived) has an unfixed advisory, GO-2026-5781 [f2]:
    unbounded recursion crashes the whole process with an uncatchable stack overflow.
  - `github.com/digitorus/pdf` v0.3.0 (2026-09-21, BSD-3-Clause) has no plain-text helper
    [f3].
  - `github.com/pdfcpu/pdfcpu` v0.16.4 (Apache-2.0) has resource limits but does not extract
    text, only raw content streams [f4].
  - `github.com/unidoc/unipdf` v5.2.0 is commercial, with a metered licence key [f5].
  - `github.com/gen2brain/folio` v0.2.10 (Apache-2.0) was created in August 2026, too young
    to judge [f6].
- **Reveals:** nothing new: text goes to the sealed text cache as today.
- **Malicious PDFs:**
  - Go cannot cap the memory or CPU of one call; `debug.SetMemoryLimit` is soft. The bomb took
    1.7 GB in our test, and a larger bomb takes more, without limit. On a 4 GB host that pushes
    the whole server to the OOM killer.
  - Panics are recoverable only on the calling goroutine.
  - Unbounded recursion (GO-2026-5781 in the parent library) is fatal.
  - It would need a sub-process anyway, which removes its only advantage.
- **Code:** S, about 80 lines.
- **Verdict:** rejected: recall too low for whole-word search, and unsafe in process.

### B. PDFium as WebAssembly, in process (recommended)

- **What:** `github.com/klippa-app/go-pdfium` v1.21.4 (2026-10-21, MIT) bundles PDFium
  build 8111 compiled to WebAssembly and runs it in `github.com/tetratelabs/wazero` v1.12.0
  (Apache-2.0) [f7][f8]. PDFium is Chrome's PDF engine, licensed BSD-3-Clause with
  Apache-2.0 text [f9].
- **How:**
  - One pool, started on first use and closed after a few idle minutes. The 330 MB resident
    cost is paid only while PDFs are being indexed.
  - Bounds: the wazero memory cap (`WithMemoryLimitPages`), the context deadline
    (`WithCloseOnContextDone`, so a runaway file is stopped), an empty `FSConfig` (by
    default go-pdfium exposes the whole disk [f7]), one PDF at a time, and the existing caps (documents over 128 MiB are not
    extracted, `maxDocumentFile`; at most 16 MiB of text).
  - The result goes through the same sealed text cache.
  - It replaces the Euro-Office route for indexing and MCP `read_file`. Euro-Office can stay
    the fallback, or be dropped for PDFs.
- **Measured:**
  - Median recall was 99%. The 4 351-page manual was stopped by the 30 s deadline, as
    intended.
  - Each new process spent 3.4 to 6 s compiling the module. A long-lived pool pays that once
    (wazero can also cache compiled code, unmeasured).
  - Extraction ran at about 10 ms per page, slower than `pdftotext` (about 1 ms).
  - Under the bomb, the wasm side hit its cap and aborted cleanly every time, but the process
    peaked well above the cap: 564 MB with a 128 MiB cap, 998 MB with 256 MiB and 1.86 GB
    with 512 MiB. That is about 330 MB of runtime plus 2 to 3 times the cap, probably from
    the host-side copies made as wasm memory grows (cause unconfirmed).
  - The cap therefore bounds the process, but not 1:1. A 128 MiB cap still extracted the
    268-page manual (414 MB peak), so 128 MiB is the proposed setting: a worst case of about
    600 MB on a 4 GB host. The pure-Go library, by contrast, has no bound: its peak grows with
    the bomb.
  - A bound to stake the server on still needs a check in the implementation's tests: a
    hostile-file test that asserts the peak RSS, plus the container's own memory limit.
- **Reveals:** nothing new.
- **Costs:**
  - About 12 MB more binary (measured: 15.4 MB against 3.5 MB for the same tiny program with
    the pure-Go library).
  - About 330 MB of RAM while the pool lives.
  - About 10 ms per page of CPU. A PDF-heavy 50 000-file drive therefore rebuilds slower than
    the DOCX figure above, by about 10 ms × pages per file (estimate).
- **Code:** M, about 150 lines in `internal/extract` or `internal/api`, plus fuzz and hostile
  file tests.
- **Risks:**
  - Three new modules, and a 5.8 MB embedded wasm blob to keep current: an NVD keyword search finds 33 PDFium
    entries in three years [f10]. Inside wasm a memory-safety bug becomes a contained fault,
    not code execution in KyDrive, but wazero itself becomes trusted code.
  - go-pdfium trails upstream PDFium by a few builds.
- **Verdict:** recommended. It is the only option with good recall and a memory bound in one
  process, though the bound is about 330 MB plus 2 to 3 times the configured cap, not the cap
  itself. It adds no C and fits the read-only, no-capabilities container. If the owner wants
  a bound equal to the setting, use option C, where `RLIMIT_AS` caps the helper process
  itself.

### C. Sandboxed helper process (Poppler `pdftotext`)

- **What:** Poppler 26.11.0 (2026-11-05), GPL-2.0 or GPL-3.0 [f11]. Xpdf 4.06 is GPL-2 or 3
  [f12]. MuPDF `mutool` is AGPL-3.0 or commercial [f13]. Calling a separate program through
  pipes is normally "mere aggregation" under the FSF FAQ [f14]. Shipping it in the image still
  obliges us to provide its source.
- **Sandbox, inside KyDrive's container (all capabilities dropped, Docker's default seccomp
  profile):**
  - New user or network namespaces are blocked: the profile refuses `clone` with namespace
    flags and `unshare` without `CAP_SYS_ADMIN` [f15].
  - What remains is a re-exec of the server binary as a small trampoline that sets
    `RLIMIT_AS`, `RLIMIT_CPU`, `RLIMIT_NOFILE` and `PR_SET_NO_NEW_PRIVS`, applies Landlock
    (read only its input, no TCP; Linux 5.13 or later, per host) and a seccomp filter, then
    `exec`s the helper [f16][f17].
  - `golang.org/x/sys` (already a direct dependency) has the constants. The seccomp BPF is
    hand-written.
  - Input goes in on stdin and text comes out on stdout, never a path.
- **Reveals:** nothing new.
- **Costs:** about 20 MB RSS per run and about 1 ms per page (measured). The image grows by
  Poppler and its shared libraries, about 5 to 10 MB (estimate), and CJK needs `poppler-data`.
- **Code:** M to L, about 300 lines: the trampoline, the seccomp filter, Landlock, the tests,
  and image changes.
- **Risks:**
  - A C parser with 22 entries in three years in an NVD keyword search (<https://nvd.nist.gov/vuln/search?query=poppler>), contained only by our own sandbox
    code.
  - Landlock may be missing on some hosts.
  - GPL source-offer duties for every image we publish.
- **Verdict:** the fallback if wazero or go-pdfium becomes a problem. Best recall per CPU
  second, most sandbox code to own.

## Recommended order

1. **Shared items (2A).** S, no index change, no new leakage. It closes the largest gap with
   Google Drive and OneDrive, and drops results faster than Nextcloud does. First step:
   `SearchContent` plus `SharedWithMe` candidates, the per-hit re-check, and the negative
   tests listed above.
2. **Prefix and phrase (1C).** M, no dependency, no disk leakage. In the same change, make
   `Match` read postings first instead of scanning every document row.
3. **PDF without Euro-Office (3B).** M, three new modules, which needs the owner's approval
   of the dependency. Ship it behind the existing content-search switch, and turn Euro-Office
   PDF conversion into the fallback.
4. **Later, only if needed:** page-encrypted FTS5 (1D) when query-time phrase checks or
   prefix fan-out no longer fit the budget, and OCR of scanned PDFs (out of scope here).

Each step changes a contract and so updates `internal/search/AGENTS.md`,
`internal/api/AGENTS.md`, `docs/RECOVERY-AND-ENCRYPTION.md` (leakage paragraph), `README.md`
and `docs/ACCEPTANCE.md` (the "whole words only … not in items only shared with them" line).

## Method

- **Host:** Major, Intel Core i9-10900KF, 20 threads, 31 GB RAM, Go 1.27.3. Other users'
  load tests ran at the same time (load average 1.4 to 3.8). Every prototype ran single
  threaded under `systemd-run --user --scope -p CPUQuota=100% -p MemoryMax=3G` or `4G`.
- **Disk:** indexes were written to `/tmp`, which is tmpfs, so fsync and disk speed are not
  included. A real disk adds write time, but rebuild time is bound by the worker's throttle,
  not by writes.
- **Corpus:** text from `/usr/share/doc` (plain text, HTML with tags stripped, PDFs through
  `pdftotext`) and a 4 351-page PDF manual, cut into 14 588 documents of 1 500 words. It is
  English technical prose. A company's own documents are more alike, which makes frequency
  attacks easier.
- **Index variants:** each was built through `search.Index.Put` and the real tokenizer
  (prefix and pair terms added to the counts), except positional (an extra `pos` table with
  varint deltas) and FTS5 (`modernc.org/sqlite` v1.62.0, contentless, `unicode61
  remove_diacritics 2` or `trigram`, then `optimize`). Size is the database directory after a
  WAL checkpoint. The test file was not committed.
- **Leakage:** documents alternated between attacker knowledge and target. Hashes were ranked
  by document frequency and matched to words of equal rank. A guess counted only on an exact
  match.
- **PDF:** recall used distinct `\w{3,}` words, lowercased.
  - Corrupted copies came from a fixed seed: every fourth copy truncated, the rest with 1, 10
    or 100 random bytes changed.
  - The bomb was one Flate stream of 300 M characters of `(A) Tj`, 3 MB compressed. The
    nesting file held 2 million `[` in a page dictionary.
  - go-pdfium ran with a 512 MiB wasm memory cap, a 30 s deadline and an empty `FSConfig`. The
    bomb and two normal PDFs were rerun at 128 MiB and 256 MiB caps.
  - PDFium ran on every third byte-flipped file (55); the other two ran on the same 55 and on all 165.

## Sources

- [g1] Google Drive API, search terms: <https://developers.google.com/workspace/drive/api/guides/ref-search-terms>
- [g2] Google Drive Help, search operators: <https://support.google.com/drive/answer/2375114>
- [m1] Microsoft, KQL syntax reference: <https://learn.microsoft.com/en-us/sharepoint/dev/general-development/keyword-query-language-kql-syntax-reference>
- [m2] Microsoft Search overview: <https://learn.microsoft.com/en-us/microsoftsearch/overview-microsoft-search>; SharePoint, make content searchable: <https://learn.microsoft.com/en-us/sharepoint/make-site-content-searchable>
- [m3] SharePoint, default crawled and parsed file types: <https://learn.microsoft.com/en-us/sharepoint/technical-reference/default-crawled-file-name-extensions-and-parsed-file-types>
- [n1] Nextcloud fulltextsearch_elasticsearch, `QueryContent.php` and `SearchMappingService.php`: <https://github.com/nextcloud/fulltextsearch_elasticsearch/blob/master/lib/Model/QueryContent.php>
- [n2] Nextcloud files_fulltextsearch, `ShareDeleted.php`: <https://github.com/nextcloud/files_fulltextsearch/blob/master/lib/Listeners/ShareDeleted.php>
- [n3] Nextcloud fulltextsearch_elasticsearch wiki: <https://github.com/nextcloud/fulltextsearch_elasticsearch/wiki>; Elastic attachment processor: <https://www.elastic.co/docs/reference/enrich-processor/attachment>
- [p1] Naveed, Kamara, Wright, Inference Attacks on Property-Preserving Encrypted Databases, CCS 2015: <https://cs.brown.edu/people/seny/pubs/edb.pdf>
- [p2] Cash, Grubbs, Perry, Ristenpart, Leakage-Abuse Attacks Against Searchable Encryption, CCS 2015: <https://eprint.iacr.org/2016/718>
- [p3] Islam, Kuzu, Kantarcioglu, Access Pattern Disclosure on Searchable Encryption, NDSS 2012: <https://www.ndss-symposium.org/wp-content/uploads/2017/09/06_1.pdf>
- [s1] modernc.org/sqlite: <https://pkg.go.dev/modernc.org/sqlite> (FTS5 confirmed in `compile_options` and by the prototype)
- [s2] SQLite FTS5: <https://www.sqlite.org/fts5.html>
- [s3] ncruces/go-sqlite3: <https://github.com/ncruces/go-sqlite3>; v0.37.0: <https://proxy.golang.org/github.com/ncruces/go-sqlite3/@latest>
- [s4] adiantum VFS: <https://github.com/ncruces/go-sqlite3/blob/main/vfs/adiantum/README.md>
- [s5] xts VFS: <https://github.com/ncruces/go-sqlite3/blob/main/vfs/xts/README.md>
- [f1] ledongthuc/pdf: <https://github.com/ledongthuc/pdf>, issues: <https://github.com/ledongthuc/pdf/issues>
- [f2] GO-2026-5781: <https://pkg.go.dev/vuln/GO-2026-5781>
- [f3] digitorus/pdf: <https://github.com/digitorus/pdf>
- [f4] pdfcpu extract: <https://pdfcpu.io/extract/extract>; text extraction issue: <https://github.com/pdfcpu/pdfcpu/issues/122>
- [f5] unipdf licence: <https://github.com/unidoc/unipdf/blob/master/LICENSE.md>
- [f6] folio: <https://github.com/gen2brain/folio>
- [f7] go-pdfium, WebAssembly notes: <https://github.com/klippa-app/go-pdfium#webassembly>; v1.21.4: <https://github.com/klippa-app/go-pdfium/releases/tag/v1.21.4>
- [f8] wazero licence: <https://github.com/wazero/wazero/blob/main/LICENSE>; `RuntimeConfig`: <https://pkg.go.dev/github.com/tetratelabs/wazero#RuntimeConfig>
- [f9] PDFium licence: <https://pdfium.googlesource.com/pdfium/+/refs/heads/main/LICENSE>
- [f10] NVD search, PDFium: <https://nvd.nist.gov/vuln/search?query=pdfium> (keyword count, approximate)
- [f11] Poppler: <https://poppler.freedesktop.org/>, tags: <https://gitlab.freedesktop.org/poppler/poppler/-/tags>, Fedora licence field: <https://src.fedoraproject.org/rpms/poppler/blob/rawhide/f/poppler.spec>
- [f12] Xpdf: <https://www.xpdfreader.com/download.html>
- [f13] MuPDF: <https://mupdf.com/releases>
- [f14] FSF GPL FAQ, mere aggregation: <https://www.gnu.org/licenses/gpl-faq.html#MereAggregation>
- [f15] Docker default seccomp profile: <https://github.com/moby/profiles/blob/main/seccomp/default.json>; <https://docs.docker.com/engine/security/seccomp/>
- [f16] Linux seccomp filter: <https://docs.kernel.org/userspace-api/seccomp_filter.html>
- [f17] Linux Landlock: <https://docs.kernel.org/userspace-api/landlock.html>
