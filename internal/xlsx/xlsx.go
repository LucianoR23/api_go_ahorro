// Package xlsx writes a single-sheet Excel workbook (Office Open XML) with
// the standard library only: a zip with a handful of XML parts.
//
// It covers what an export needs and nothing more: text, numbers with two
// decimals, whole numbers and dates, a bold frozen header row with an
// autofilter, and column widths. Number and date cells use Excel's built-in
// formats (4 and 14), which Excel shows in the reader's locale, so an es-AR
// reader sees 19.450,50 and 03/11/2026.
package xlsx

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/shopspring/decimal"
)

type kind uint8

const (
	kindEmpty kind = iota
	kindText
	kindMoney
	kindInt
	kindDate
)

// Cell is one value of a row. The zero value is an empty cell.
type Cell struct {
	kind kind
	text string
	num  decimal.Decimal
	n    int64
	date time.Time
}

// Text is a string cell. Text is never read as a formula.
func Text(s string) Cell { return Cell{kind: kindText, text: s} }

// Money is a number shown with two decimals and thousands separators.
func Money(d decimal.Decimal) Cell { return Cell{kind: kindMoney, num: d} }

// Int is a whole number.
func Int(n int64) Cell { return Cell{kind: kindInt, n: n} }

// Date is a calendar date (the time of day is ignored).
func Date(t time.Time) Cell { return Cell{kind: kindDate, date: t} }

// Empty is a blank cell.
func Empty() Cell { return Cell{} }

// Column is a header and its width in characters.
type Column struct {
	Header string
	Width  float64
}

// Sheet is the workbook's only sheet.
type Sheet struct {
	Name    string
	Columns []Column
	Rows    [][]Cell
}

// Style ids in styles.xml (cellXfs order).
const (
	styleDefault = 0
	styleHeader  = 1
	styleMoney   = 2
	styleDate    = 3
)

// maxSheetName is Excel's limit on sheet names.
const maxSheetName = 31

// Write writes the workbook to w.
func Write(w io.Writer, s Sheet) error {
	if err := validate(s); err != nil {
		return err
	}
	zw := zip.NewWriter(w)
	parts := []struct {
		name string
		body []byte
	}{
		{"[Content_Types].xml", []byte(contentTypes)},
		{"_rels/.rels", []byte(rootRels)},
		{"xl/workbook.xml", workbook(s)},
		{"xl/_rels/workbook.xml.rels", []byte(workbookRels)},
		{"xl/styles.xml", []byte(styles)},
		{"xl/worksheets/sheet1.xml", worksheet(s)},
	}
	for _, p := range parts {
		f, err := zw.Create(p.name)
		if err != nil {
			return fmt.Errorf("xlsx: %s: %w", p.name, err)
		}
		if _, err := f.Write(p.body); err != nil {
			return fmt.Errorf("xlsx: %s: %w", p.name, err)
		}
	}
	return zw.Close()
}

func validate(s Sheet) error {
	if s.Name == "" || len([]rune(s.Name)) > maxSheetName || strings.ContainsAny(s.Name, `[]:*?/\'`) {
		return fmt.Errorf("xlsx: invalid sheet name %q", s.Name)
	}
	if len(s.Columns) == 0 {
		return errors.New("xlsx: a sheet needs at least one column")
	}
	for i, r := range s.Rows {
		if len(r) > len(s.Columns) {
			return fmt.Errorf("xlsx: row %d has %d cells for %d columns", i+1, len(r), len(s.Columns))
		}
	}
	return nil
}

// ColumnName turns a zero-based column index into its letters: 0 -> A, 26 -> AA.
func ColumnName(i int) string {
	name := ""
	for i++; i > 0; i = (i - 1) / 26 {
		name = string(rune('A'+(i-1)%26)) + name
	}
	return name
}

// excelEpoch is day 0 of Excel's 1900 date system (with its 1900 leap-year
// bug folded in, which is why it isn't December 31).
var excelEpoch = time.Date(1899, 12, 30, 0, 0, 0, 0, time.UTC)

// Serial is the Excel date number for t's calendar date.
func Serial(t time.Time) int64 {
	d := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
	return int64(d.Sub(excelEpoch).Hours() / 24)
}

func lastRef(s Sheet) string {
	return ColumnName(len(s.Columns)-1) + strconv.Itoa(len(s.Rows)+1)
}

func workbook(s Sheet) []byte {
	var b bytes.Buffer
	b.WriteString(xml.Header)
	b.WriteString(`<workbook xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships"><sheets><sheet name="`)
	escape(&b, s.Name)
	b.WriteString(`" sheetId="1" r:id="rId1"/></sheets><definedNames><definedName name="_xlnm._FilterDatabase" localSheetId="0" hidden="1">`)
	escape(&b, "'"+s.Name+"'!$A$1:$"+ColumnName(len(s.Columns)-1)+"$"+strconv.Itoa(len(s.Rows)+1))
	b.WriteString(`</definedName></definedNames></workbook>`)
	return b.Bytes()
}

func worksheet(s Sheet) []byte {
	var b bytes.Buffer
	b.WriteString(xml.Header)
	b.WriteString(`<worksheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships">`)
	b.WriteString(`<sheetViews><sheetView workbookViewId="0"><pane ySplit="1" topLeftCell="A2" activePane="bottomLeft" state="frozen"/></sheetView></sheetViews>`)
	b.WriteString(`<sheetFormatPr defaultRowHeight="15"/><cols>`)
	for i, c := range s.Columns {
		w := c.Width
		if w <= 0 {
			w = 12
		}
		fmt.Fprintf(&b, `<col min="%d" max="%d" width="%s" customWidth="1"/>`, i+1, i+1, strconv.FormatFloat(w, 'f', -1, 64))
	}
	b.WriteString(`</cols><sheetData><row r="1">`)
	for i, c := range s.Columns {
		textCell(&b, ColumnName(i)+"1", c.Header, styleHeader)
	}
	b.WriteString(`</row>`)
	for r, row := range s.Rows {
		n := strconv.Itoa(r + 2)
		b.WriteString(`<row r="` + n + `">`)
		for i, c := range row {
			ref := ColumnName(i) + n
			switch c.kind {
			case kindText:
				textCell(&b, ref, c.text, styleDefault)
			case kindMoney:
				fmt.Fprintf(&b, `<c r="%s" s="%d"><v>%s</v></c>`, ref, styleMoney, c.num.StringFixed(2))
			case kindInt:
				fmt.Fprintf(&b, `<c r="%s"><v>%d</v></c>`, ref, c.n)
			case kindDate:
				fmt.Fprintf(&b, `<c r="%s" s="%d"><v>%d</v></c>`, ref, styleDate, Serial(c.date))
			}
		}
		b.WriteString(`</row>`)
	}
	b.WriteString(`</sheetData><autoFilter ref="A1:` + lastRef(s) + `"/></worksheet>`)
	return b.Bytes()
}

func textCell(b *bytes.Buffer, ref, text string, style int) {
	fmt.Fprintf(b, `<c r="%s" t="inlineStr"`, ref)
	if style != styleDefault {
		fmt.Fprintf(b, ` s="%d"`, style)
	}
	b.WriteString(`><is><t xml:space="preserve">`)
	escape(b, text)
	b.WriteString(`</t></is></c>`)
}

// escape writes s as XML text. Characters XML can't carry become U+FFFD.
func escape(b *bytes.Buffer, s string) {
	_ = xml.EscapeText(b, []byte(s))
}

const contentTypes = xml.Header + `<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">` +
	`<Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/>` +
	`<Default Extension="xml" ContentType="application/xml"/>` +
	`<Override PartName="/xl/workbook.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.sheet.main+xml"/>` +
	`<Override PartName="/xl/worksheets/sheet1.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.worksheet+xml"/>` +
	`<Override PartName="/xl/styles.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.styles+xml"/>` +
	`</Types>`

const rootRels = xml.Header + `<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">` +
	`<Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="xl/workbook.xml"/>` +
	`</Relationships>`

const workbookRels = xml.Header + `<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">` +
	`<Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/worksheet" Target="worksheets/sheet1.xml"/>` +
	`<Relationship Id="rId2" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/styles" Target="styles.xml"/>` +
	`</Relationships>`

// styles: cellXfs 0 default, 1 bold header, 2 money (#,##0.00), 3 date (14).
const styles = xml.Header + `<styleSheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main">` +
	`<fonts count="2"><font><sz val="11"/><name val="Calibri"/></font><font><b/><sz val="11"/><name val="Calibri"/></font></fonts>` +
	`<fills count="2"><fill><patternFill patternType="none"/></fill><fill><patternFill patternType="gray125"/></fill></fills>` +
	`<borders count="1"><border><left/><right/><top/><bottom/><diagonal/></border></borders>` +
	`<cellStyleXfs count="1"><xf numFmtId="0" fontId="0" fillId="0" borderId="0"/></cellStyleXfs>` +
	`<cellXfs count="4">` +
	`<xf numFmtId="0" fontId="0" fillId="0" borderId="0" xfId="0"/>` +
	`<xf numFmtId="0" fontId="1" fillId="0" borderId="0" xfId="0" applyFont="1"/>` +
	`<xf numFmtId="4" fontId="0" fillId="0" borderId="0" xfId="0" applyNumberFormat="1"/>` +
	`<xf numFmtId="14" fontId="0" fillId="0" borderId="0" xfId="0" applyNumberFormat="1"/>` +
	`</cellXfs>` +
	`<cellStyles count="1"><cellStyle name="Normal" xfId="0" builtinId="0"/></cellStyles>` +
	`</styleSheet>`
