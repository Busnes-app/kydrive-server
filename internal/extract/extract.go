// Package extract turns Office Open XML (Word, Excel, PowerPoint) and OpenDocument files into
// plain text, in process, with the standard library only. Documents are untrusted: every read is
// bounded (entries, bytes per part, bytes per document, text produced), XML is streamed so
// nesting costs no stack, and Go's decoder expands no custom or external entities.
package extract

import (
	"archive/zip"
	"context"
	"encoding/xml"
	"errors"
	"io"
	"path"
	"strconv"
	"strings"
)

var (
	// ErrUnsupported means the file is not a document of its kind: not a zip, or a required part
	// is missing or not well-formed XML.
	ErrUnsupported = errors.New("not a readable document")
	// ErrTooLarge means a limit stopped the read before any text could be trusted complete.
	ErrTooLarge = errors.New("document too large to extract")
)

// Limits. A document needing more is refused, never partly decompressed into memory.
const (
	MaxParts      = 10000     // zip entries
	MaxPartBytes  = 64 << 20  // decompressed bytes of one part
	MaxTotalBytes = 256 << 20 // decompressed bytes of all parts read
	MaxText       = 16 << 20  // bytes of text produced; beyond it the result is Truncated
	maxRepeat     = 100       // OpenDocument repeated rows and columns written out
	maxGap        = 100       // empty spreadsheet columns written as tabs
)

// The limits as enforced; tests lower them rather than build gigabytes.
var (
	maxParts     = MaxParts
	maxPartBytes = int64(MaxPartBytes)
	maxTotal     = int64(MaxTotalBytes)
	maxText      = MaxText
)

// Result is a document's text. Truncated is set when it was cut at MaxText.
type Result struct {
	Text      string
	Truncated bool
}

type format struct {
	kind string
	read func(*doc) error
}

var formats = map[string]format{
	"docx": {"Word document", readDOCX}, "docm": {"Word document", readDOCX}, "dotx": {"Word document", readDOCX},
	"xlsx": {"Excel workbook", readXLSX}, "xlsm": {"Excel workbook", readXLSX}, "xltx": {"Excel workbook", readXLSX},
	"pptx": {"PowerPoint presentation", readPPTX}, "pptm": {"PowerPoint presentation", readPPTX}, "potx": {"PowerPoint presentation", readPPTX},
	"odt": {"OpenDocument text", readODF}, "ott": {"OpenDocument text", readODF},
	"ods": {"OpenDocument spreadsheet", readODS}, "ots": {"OpenDocument spreadsheet", readODS},
	"odp": {"OpenDocument presentation", readODP}, "otp": {"OpenDocument presentation", readODP},
}

// Kind names the document type of extension ext (no dot, any case), or "" when Text cannot read it.
func Kind(ext string) string { return formats[strings.ToLower(ext)].kind }

// Text extracts the text of the document of extension ext held in r (size bytes).
func Text(ctx context.Context, r io.ReaderAt, size int64, ext string) (Result, error) {
	f, ok := formats[strings.ToLower(ext)]
	if !ok {
		return Result{}, ErrUnsupported
	}
	z, err := zip.NewReader(r, size)
	if err != nil {
		return Result{}, ErrUnsupported
	}
	if len(z.File) > maxParts {
		return Result{}, ErrTooLarge
	}
	d := &doc{ctx: ctx, parts: map[string]*zip.File{}}
	for _, p := range z.File {
		d.parts[strings.ToLower(strings.TrimPrefix(strings.ReplaceAll(p.Name, `\`, "/"), "/"))] = p
	}
	err = f.read(d)
	if errors.Is(err, errFull) {
		err = nil
	}
	if err != nil {
		return Result{}, err
	}
	text := d.out.String()
	if !d.full {
		if text = strings.TrimRight(text, "\n"); text != "" {
			text += "\n"
		}
	}
	return Result{Text: text, Truncated: d.full}, nil
}

// errFull stops a read once MaxText is reached; Text reports it as Truncated.
var errFull = errors.New("text limit reached")

type doc struct {
	ctx   context.Context
	parts map[string]*zip.File
	read  int64
	out   strings.Builder
	full  bool
}

func (d *doc) write(s string) error {
	if d.full {
		return errFull
	}
	if d.out.Len()+len(s) > maxText {
		s = strings.ToValidUTF8(s[:maxText-d.out.Len()], "")
		d.full = true
	}
	d.out.WriteString(strings.ReplaceAll(s, "\x00", ""))
	if d.full {
		return errFull
	}
	return nil
}

// has reports whether the document holds part name.
func (d *doc) has(name string) bool { _, ok := d.parts[strings.ToLower(name)]; return ok }

// part streams one part's XML, counting its bytes against the limits.
func (d *doc) part(name string) (*xml.Decoder, func(), error) {
	p, ok := d.parts[strings.ToLower(name)]
	if !ok {
		return nil, nil, ErrUnsupported
	}
	if p.UncompressedSize64 > uint64(maxPartBytes) {
		return nil, nil, ErrTooLarge
	}
	rc, err := p.Open()
	if err != nil {
		return nil, nil, ErrUnsupported
	}
	dec := xml.NewDecoder(&counted{r: rc, d: d, left: maxPartBytes})
	return dec, func() { rc.Close() }, nil
}

// counted enforces the per-part and per-document byte limits on what a part decompresses to,
// whatever its header claimed, and stops when the context ends.
type counted struct {
	r    io.Reader
	d    *doc
	left int64
}

func (c *counted) Read(p []byte) (int, error) {
	if err := c.d.ctx.Err(); err != nil {
		return 0, err
	}
	n, err := c.r.Read(p)
	c.left -= int64(n)
	c.d.read += int64(n)
	if c.left < 0 || c.d.read > maxTotal {
		return n, ErrTooLarge
	}
	return n, err
}

// walk calls fn for each token of part name until the end of the part. A malformed part is
// ErrUnsupported; a limit or the context passes through.
func (d *doc) walk(name string, fn func(xml.Token) error) error {
	dec, done, err := d.part(name)
	if err != nil {
		return err
	}
	defer done()
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			if errors.Is(err, ErrTooLarge) || d.ctx.Err() != nil {
				return errors.Join(err, d.ctx.Err())
			}
			return ErrUnsupported
		}
		if err = fn(tok); err != nil {
			return err
		}
	}
}

func attr(e xml.StartElement, local string) string {
	for _, a := range e.Attr {
		if a.Name.Local == local {
			return a.Value
		}
	}
	return ""
}

// relID is an element's r:id; PowerPoint's sldId also has a plain numeric id.
func relID(e xml.StartElement) string {
	for _, a := range e.Attr {
		if a.Name.Local == "id" && (a.Name.Space == nsR || a.Name.Space == nsRS) {
			return a.Value
		}
	}
	return ""
}

// skipper skips a subtree: alternate content's fallback copy, deleted text, notes, comments.
type skipper struct{ depth int }

// skip reports whether tok is inside a subtree skipped since start returned true for its root.
func (s *skipper) skip(tok xml.Token, start func(xml.StartElement) bool) bool {
	switch t := tok.(type) {
	case xml.StartElement:
		if s.depth > 0 || start(t) {
			s.depth++
			return true
		}
	case xml.EndElement:
		if s.depth > 0 {
			s.depth--
			return true
		}
	}
	return s.depth > 0
}

const (
	nsW      = "http://schemas.openxmlformats.org/wordprocessingml/2006/main"
	nsWS     = "http://purl.oclc.org/ooxml/wordprocessingml/main"
	nsA      = "http://schemas.openxmlformats.org/drawingml/2006/main"
	nsAS     = "http://purl.oclc.org/ooxml/drawingml/main"
	nsMC     = "http://schemas.openxmlformats.org/markup-compatibility/2006"
	nsRel    = "http://schemas.openxmlformats.org/package/2006/relationships"
	nsR      = "http://schemas.openxmlformats.org/officeDocument/2006/relationships"
	nsRS     = "http://purl.oclc.org/ooxml/officeDocument/relationships"
	nsText   = "urn:oasis:names:tc:opendocument:xmlns:text:1.0"
	nsTable  = "urn:oasis:names:tc:opendocument:xmlns:table:1.0"
	nsDraw   = "urn:oasis:names:tc:opendocument:xmlns:drawing:1.0"
	nsOffice = "urn:oasis:names:tc:opendocument:xmlns:office:1.0"
	nsPres   = "urn:oasis:names:tc:opendocument:xmlns:presentation:1.0"
)

// fallback is mc:Fallback, which repeats mc:Choice's content for older readers.
func fallback(e xml.StartElement) bool { return e.Name.Space == nsMC && e.Name.Local == "Fallback" }

// readDOCX writes the body's paragraphs: w:t text, tabs and breaks, and tables as tab-separated
// rows. Deleted text (w:delText), field codes, headers, footnotes and comments are left out.
func readDOCX(d *doc) error {
	var s skipper
	var row []string
	var cell strings.Builder
	inText, inCell := false, 0
	text := func(v string) error {
		if inCell > 0 {
			if cell.Len() < maxText {
				cell.WriteString(v)
			}
			return nil
		}
		return d.write(v)
	}
	return d.walk("word/document.xml", func(tok xml.Token) error {
		if s.skip(tok, fallback) {
			return nil
		}
		switch t := tok.(type) {
		case xml.StartElement:
			if t.Name.Space != nsW && t.Name.Space != nsWS {
				return nil
			}
			switch t.Name.Local {
			case "t":
				inText = true
			case "tab":
				return text("\t")
			case "br", "cr":
				return text("\n")
			case "tr":
				if inCell == 0 {
					row = row[:0]
				}
			case "tc":
				if inCell == 0 {
					cell.Reset()
				}
				inCell++ // a nested table's cells join their outer cell
			}
		case xml.EndElement:
			if t.Name.Space != nsW && t.Name.Space != nsWS {
				return nil
			}
			switch t.Name.Local {
			case "t":
				inText = false
			case "p":
				if inCell > 0 {
					return text(" ") // paragraphs of one cell share its line
				}
				return d.write("\n")
			case "tc":
				if inCell--; inCell == 0 {
					row = append(row, strings.TrimSpace(strings.NewReplacer("\t", " ", "\n", " ").Replace(cell.String())))
				}
			case "tr":
				if inCell == 0 {
					return writeRow(d, row)
				}
			}
		case xml.CharData:
			if inText {
				return text(string(t))
			}
		}
		return nil
	})
}

// rels maps the relationship IDs of part's .rels file to part names.
func (d *doc) rels(part string) (map[string]string, error) {
	dir, file := path.Split(part)
	name := dir + "_rels/" + file + ".rels"
	out := map[string]string{}
	if !d.has(name) {
		return out, nil
	}
	err := d.walk(name, func(tok xml.Token) error {
		if e, ok := tok.(xml.StartElement); ok && e.Name.Local == "Relationship" && (e.Name.Space == nsRel || e.Name.Space == "") && attr(e, "TargetMode") != "External" {
			target := attr(e, "Target")
			if strings.HasPrefix(target, "/") {
				target = strings.TrimPrefix(target, "/")
			} else {
				target = path.Join(dir, target)
			}
			out[attr(e, "Id")] = target
		}
		return nil
	})
	return out, err
}

// ordered lists the parts that part's elements named local point to with r:id, in document order.
func (d *doc) ordered(part, local string) ([]string, []xml.StartElement, error) {
	rels, err := d.rels(part)
	if err != nil {
		return nil, nil, err
	}
	var names []string
	var elems []xml.StartElement
	err = d.walk(part, func(tok xml.Token) error {
		if e, ok := tok.(xml.StartElement); ok && e.Name.Local == local {
			if target := rels[relID(e)]; target != "" && d.has(target) {
				names = append(names, target)
				elems = append(elems, e.Copy())
			}
		}
		return nil
	})
	return names, elems, err
}

// readXLSX writes each sheet, in workbook order, as "## name" and tab-separated rows.
func readXLSX(d *doc) error {
	var shared []string
	if d.has("xl/sharedStrings.xml") {
		var cur strings.Builder
		var s skipper
		inT := false
		err := d.walk("xl/sharedStrings.xml", func(tok xml.Token) error {
			if s.skip(tok, func(e xml.StartElement) bool { return e.Name.Local == "rPh" }) { // phonetic guides
				return nil
			}
			switch t := tok.(type) {
			case xml.StartElement:
				if t.Name.Local == "si" {
					cur.Reset()
				}
				inT = t.Name.Local == "t"
			case xml.EndElement:
				inT = false
				if t.Name.Local == "si" {
					shared = append(shared, cur.String())
				}
			case xml.CharData:
				if inT && cur.Len() < maxText {
					cur.Write(t)
				}
			}
			return nil
		})
		if err != nil {
			return err
		}
	}
	sheets, elems, err := d.ordered("xl/workbook.xml", "sheet")
	if err != nil {
		return err
	}
	for i, sheet := range sheets {
		if err = d.write("## " + attr(elems[i], "name") + "\n"); err != nil {
			return err
		}
		if err = readSheet(d, sheet, shared); err != nil {
			return err
		}
	}
	return nil
}

func readSheet(d *doc, sheet string, shared []string) error {
	var row []string
	var cell strings.Builder
	cellType, inValue, col := "", false, 0
	return d.walk(sheet, func(tok xml.Token) error {
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "row":
				row, col = row[:0], 0
			case "c":
				cellType = attr(t, "t")
				cell.Reset()
				// Empty columns before this cell keep the row aligned, up to maxGap of them.
				if c := column(attr(t, "r")); c > col {
					for range min(c-col-1, maxGap) {
						row = append(row, "")
					}
					col = c
				} else {
					col++
				}
			case "v", "t":
				inValue = true
			}
		case xml.EndElement:
			switch t.Name.Local {
			case "v", "t":
				inValue = false
			case "c":
				v := cell.String()
				switch cellType {
				case "s":
					if i, err := strconv.Atoi(strings.TrimSpace(v)); err == nil && i >= 0 && i < len(shared) {
						v = shared[i]
					} else {
						v = ""
					}
				case "b":
					v = map[string]string{"0": "FALSE", "1": "TRUE"}[strings.TrimSpace(v)]
				}
				row = append(row, strings.ReplaceAll(strings.ReplaceAll(v, "\t", " "), "\n", " "))
			case "row":
				return writeRow(d, row)
			}
		case xml.CharData:
			if inValue && cell.Len() < maxText {
				cell.Write(t)
			}
		}
		return nil
	})
}

// column is the 1-based column of a cell reference such as "C7", or 0.
func column(ref string) int {
	n := 0
	for _, r := range ref {
		if r < 'A' || r > 'Z' {
			break
		}
		if n = n*26 + int(r-'A'+1); n > 1<<20 {
			return 0
		}
	}
	return n
}

// writeRow writes a row's cells tab-separated, without trailing empty cells; empty rows vanish.
func writeRow(d *doc, row []string) error {
	end := len(row)
	for end > 0 && strings.TrimSpace(row[end-1]) == "" {
		end--
	}
	if end == 0 {
		return nil
	}
	return d.write(strings.Join(row[:end], "\t") + "\n")
}

// readPPTX writes each slide, in presentation order, as "## Slide n" and its text. Notes are
// separate parts and are left out.
func readPPTX(d *doc) error {
	slides, _, err := d.ordered("ppt/presentation.xml", "sldId")
	if err != nil {
		return err
	}
	for i, slide := range slides {
		if err = d.write("## Slide " + strconv.Itoa(i+1) + "\n"); err != nil {
			return err
		}
		var s skipper
		inText := false
		err = d.walk(slide, func(tok xml.Token) error {
			if s.skip(tok, fallback) {
				return nil
			}
			switch t := tok.(type) {
			case xml.StartElement:
				if t.Name.Space != nsA && t.Name.Space != nsAS {
					return nil
				}
				switch t.Name.Local {
				case "t":
					inText = true
				case "br":
					return d.write("\n")
				}
			case xml.EndElement:
				if t.Name.Space != nsA && t.Name.Space != nsAS {
					return nil
				}
				switch t.Name.Local {
				case "t":
					inText = false
				case "p":
					return d.write("\n")
				}
			case xml.CharData:
				if inText {
					return d.write(string(t))
				}
			}
			return nil
		})
		if err != nil {
			return err
		}
	}
	return nil
}

func readODF(d *doc) error { return readOpenDocument(d, "") }
func readODS(d *doc) error { return readOpenDocument(d, "sheet") }
func readODP(d *doc) error { return readOpenDocument(d, "slide") }

// odfSkipped are subtrees whose text is not the document's: notes and comments, tracked
// deletions, and presentation speaker notes.
func odfSkipped(e xml.StartElement) bool {
	switch {
	case e.Name.Space == nsText && (e.Name.Local == "note" || e.Name.Local == "tracked-changes"):
	case e.Name.Space == nsOffice && e.Name.Local == "annotation":
	case e.Name.Space == nsPres && e.Name.Local == "notes":
	default:
		return false
	}
	return true
}

func repeat(e xml.StartElement, local string) int {
	n, err := strconv.Atoi(attr(e, local))
	if err != nil || n < 1 {
		return 1
	}
	return min(n, maxRepeat)
}

// readOpenDocument writes content.xml's paragraphs and headings. Tables become tab-separated
// rows; each spreadsheet table is headed "## name" (heading "sheet") and each drawing page
// "## Slide n" (heading "slide").
func readOpenDocument(d *doc, heading string) error {
	var s skipper
	var row []string
	var cell strings.Builder
	inPara, inCell, slide := 0, false, 0
	rowRepeat, cellRepeat := 1, 1
	text := func(v string) error {
		if inCell {
			if cell.Len() < maxText {
				cell.WriteString(v)
			}
			return nil
		}
		return d.write(v)
	}
	return d.walk("content.xml", func(tok xml.Token) error {
		if s.skip(tok, odfSkipped) {
			return nil
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch {
			case t.Name.Space == nsText && (t.Name.Local == "p" || t.Name.Local == "h"):
				if inCell && cell.Len() > 0 && inPara == 0 {
					cell.WriteString(" ") // paragraphs of one cell share its line
				}
				inPara++
			case t.Name.Space == nsText && t.Name.Local == "tab":
				return text("\t")
			case t.Name.Space == nsText && t.Name.Local == "line-break":
				return text("\n")
			case t.Name.Space == nsText && t.Name.Local == "s":
				n, err := strconv.Atoi(attr(t, "c"))
				if err != nil || n < 1 {
					n = 1
				}
				return text(strings.Repeat(" ", min(n, maxRepeat)))
			case t.Name.Space == nsTable && t.Name.Local == "table":
				if heading == "sheet" {
					return d.write("## " + attr(t, "name") + "\n")
				}
			case t.Name.Space == nsTable && t.Name.Local == "table-row":
				row, rowRepeat = row[:0], repeat(t, "number-rows-repeated")
			case t.Name.Space == nsTable && (t.Name.Local == "table-cell" || t.Name.Local == "covered-table-cell"):
				inCell, cellRepeat = true, repeat(t, "number-columns-repeated")
				cell.Reset()
			case t.Name.Space == nsDraw && t.Name.Local == "page" && heading == "slide":
				slide++
				return d.write("## Slide " + strconv.Itoa(slide) + "\n")
			}
		case xml.EndElement:
			switch {
			case t.Name.Space == nsText && (t.Name.Local == "p" || t.Name.Local == "h"):
				inPara--
				if !inCell && inPara == 0 {
					return d.write("\n")
				}
			case t.Name.Space == nsTable && (t.Name.Local == "table-cell" || t.Name.Local == "covered-table-cell"):
				v := strings.ReplaceAll(strings.ReplaceAll(cell.String(), "\t", " "), "\n", " ")
				for range cellRepeat {
					if len(row) < maxParts {
						row = append(row, v)
					}
				}
				inCell = false
			case t.Name.Space == nsTable && t.Name.Local == "table-row":
				for range rowRepeat {
					if err := writeRow(d, row); err != nil {
						return err
					}
				}
			}
		case xml.CharData:
			if inPara > 0 {
				return text(strings.ToValidUTF8(string(t), "\uFFFD"))
			}
		}
		return nil
	})
}
