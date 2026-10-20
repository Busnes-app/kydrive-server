#!/bin/sh
# Regenerates the LibreOffice-made fixtures from the flat OpenDocument sources in src/.
# Run from this directory; needs soffice (LibreOffice). The outputs are committed.
set -eu
out=$(mktemp -d)
for pair in report.fodt:docx report.fodt:odt budget.fods:xlsx budget.fods:ods deck.fodp:pptx deck.fodp:odp; do
  soffice --headless -env:UserInstallation=file://$out/profile --convert-to "${pair#*:}" --outdir "$out" "src/${pair%%:*}" >/dev/null
done
cp "$out"/report.docx "$out"/report.odt "$out"/budget.xlsx "$out"/budget.ods "$out"/deck.pptx "$out"/deck.odp .
rm -rf "$out"
