# Document text extraction

## Purpose
Turn Office Open XML (`docx`, `xlsx`, `pptx`, their macro and template variants) and OpenDocument (`odt`, `ods`, `odp`, templates) files into plain text for the MCP `read_file` tool.

## Ownership
Owns parsing. Where the bytes come from, caching and PDF conversion belong to `internal/api` (`document_text.go`).

## Local Contracts
- Standard library only: `archive/zip` and streamed `encoding/xml`. No dependency, no subprocess, no PDF.
- Inputs are untrusted, and memory must stay bounded by the limits, never by the document. `MaxParts`, `MaxPartBytes`, `MaxTotalBytes` and `MaxText` bound every document whatever its zip headers claim; `counted` enforces them on decompressed bytes and stops on context end.
- encoding/xml holds a tag's attributes and the open-element stack in memory (about 20x their bytes) before a caller sees them, so `lexer` tracks markup boundaries in the bytes first: at most `MaxTag` bytes in one tag and `MaxDepth` open elements, and DOCTYPE or any other declaration is refused (so no entity is ever processed). It must stay at least as strict as the decoder on what is inside a tag (quotes included).
- Rows (`row`) are built within the text still allowed, and cell buffers (`appendCapped`) stop at `MaxText`, so shared strings and repeated cells cannot make a row larger than the output. A part listed twice in a workbook or presentation is read once.
- Over a limit is `ErrTooLarge`; anything malformed is `ErrUnsupported`; text beyond `MaxText` sets `Truncated`. Each limit has a test that measures allocation, so a fix that only caps output after building it fails.
- Output: paragraphs as lines, tables and sheets as tab-separated rows without trailing empty cells, `## <sheet name>` and `## Slide n` headings, sheets and slides in workbook and presentation order (relationship IDs, never file names). Skipped: `mc:Fallback`, `w:delText`, field codes, ODF notes, annotations, tracked changes and presentation notes; footnotes, comments, headers and speaker notes live in other parts and are not read.

## Verification
- `go test -race ./internal/extract` (`testdata/` fixtures made by LibreOffice with `testdata/generate.sh` from `testdata/src`, hostile hand-built zips, Euro-Office's blank templates).

## Child DOX Index
None.
