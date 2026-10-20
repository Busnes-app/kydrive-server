# Document text extraction

## Purpose
Turn Office Open XML (`docx`, `xlsx`, `pptx`, their macro and template variants) and OpenDocument (`odt`, `ods`, `odp`, templates) files into plain text for the MCP `read_file` tool.

## Ownership
Owns parsing. Where the bytes come from, caching and PDF conversion belong to `internal/api` (`document_text.go`).

## Local Contracts
- Standard library only: `archive/zip` and streamed `encoding/xml`. No dependency, no subprocess, no PDF.
- Inputs are untrusted. Limits (`MaxParts`, `MaxPartBytes`, `MaxTotalBytes`, `MaxText`) bound every document whatever its zip headers claim; `counted` enforces them on decompressed bytes and stops on context end. XML is tokenized, never recursed, and no entity is expanded. Over a limit is `ErrTooLarge`; anything malformed is `ErrUnsupported`; text beyond `MaxText` sets `Truncated`.
- Output: paragraphs as lines, tables and sheets as tab-separated rows without trailing empty cells, `## <sheet name>` and `## Slide n` headings, sheets and slides in workbook and presentation order (relationship IDs, never file names). Skipped: `mc:Fallback`, `w:delText`, field codes, ODF notes, annotations, tracked changes and presentation notes; footnotes, comments, headers and speaker notes live in other parts and are not read.

## Verification
- `go test -race ./internal/extract` (`testdata/` fixtures made by LibreOffice with `testdata/generate.sh` from `testdata/src`, hostile hand-built zips, Euro-Office's blank templates).

## Child DOX Index
None.
