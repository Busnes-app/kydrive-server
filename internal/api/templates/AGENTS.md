# Blank Document and Office templates

## Purpose
Embedded starting bytes for new DOCX, XLSX, PPTX, MD, RTF and Excalidraw files.

## Ownership
The parent API package owns `driveNewDocument`, publication and authentication. This directory owns blank template assets.

## Local Contracts
- Keep templates blank, macro-free and without external relationships or user metadata.
- `blank.docx` is minimal Office Open XML. XLSX/PPTX were generated as blank files with the pinned Euro-Office local fixture editor; core metadata was cleared and ZIP timestamps normalized.
- `blank.md` and `blank.rtf` are minimal text/markdown templates.
- `blank.excalidraw` is minimal standard Excalidraw scene JSON.
- Template bytes are published through normal workspace authorization, quotas and immutable versions.

## Work Guidance

## Verification
- `TestNewDocumentsUseWorkspacePermissions` exercises template types through the real API and rejects other workspace credentials. Verify editor opening/saving on the running integration when replacing a template.

## Child DOX Index
None.
