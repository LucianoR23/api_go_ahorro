package xlsx_test

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/LucianoR23/valuo_api/internal/xlsx"
)

func TestColumnName(t *testing.T) {
	for i, want := range map[int]string{0: "A", 1: "B", 25: "Z", 26: "AA", 27: "AB", 51: "AZ", 52: "BA", 701: "ZZ", 702: "AAA"} {
		if got := xlsx.ColumnName(i); got != want {
			t.Errorf("ColumnName(%d) = %q, want %q", i, got, want)
		}
	}
}

func TestSerial(t *testing.T) {
	cases := map[string]int64{"1900-03-01": 61, "2024-01-01": 45292, "2026-11-03": 46329}
	for day, want := range cases {
		d, _ := time.Parse("2006-01-02", day)
		if got := xlsx.Serial(d); got != want {
			t.Errorf("Serial(%s) = %d, want %d", day, got, want)
		}
	}
	// The time of day and the zone don't move the date.
	late := time.Date(2024, 1, 1, 23, 59, 0, 0, time.FixedZone("ART", -3*3600))
	if got := xlsx.Serial(late); got != 45292 {
		t.Errorf("Serial(late) = %d", got)
	}
}

// cell is a parsed <c> of the sheet.
type cell struct {
	Ref    string `xml:"r,attr"`
	Type   string `xml:"t,attr"`
	Style  string `xml:"s,attr"`
	Value  string `xml:"v"`
	Inline string `xml:"is>t"`
}

type sheetXML struct {
	Rows []struct {
		Ref   string `xml:"r,attr"`
		Cells []cell `xml:"c"`
	} `xml:"sheetData>row"`
	Pane struct {
		State string `xml:"state,attr"`
	} `xml:"sheetViews>sheetView>pane"`
	Cols []struct {
		Width string `xml:"width,attr"`
	} `xml:"cols>col"`
	AutoFilter struct {
		Ref string `xml:"ref,attr"`
	} `xml:"autoFilter"`
}

func parts(t *testing.T, raw []byte) map[string][]byte {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		t.Fatalf("not a zip: %v", err)
	}
	out := map[string][]byte{}
	for _, f := range zr.File {
		rc, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		b, err := io.ReadAll(rc)
		_ = rc.Close()
		if err != nil {
			t.Fatal(err)
		}
		// Every part must be well-formed XML.
		dec := xml.NewDecoder(bytes.NewReader(b))
		for {
			if _, err := dec.Token(); err == io.EOF {
				break
			} else if err != nil {
				t.Fatalf("%s is not well-formed: %v", f.Name, err)
			}
		}
		out[f.Name] = b
	}
	return out
}

func TestWrite(t *testing.T) {
	day := time.Date(2026, 11, 3, 0, 0, 0, 0, time.UTC)
	s := xlsx.Sheet{
		Name:    "Gastos",
		Columns: []xlsx.Column{{Header: "Fecha", Width: 11}, {Header: "Descripción"}, {Header: "Monto"}, {Header: "Cuotas"}, {Header: "Nota"}},
		Rows: [][]xlsx.Cell{
			{xlsx.Date(day), xlsx.Text("Súper <Coto> & \"más\""), xlsx.Money(decimal.RequireFromString("19450.5")), xlsx.Int(3), xlsx.Empty()},
			{xlsx.Date(day), xlsx.Text("=1+1"), xlsx.Money(decimal.RequireFromString("-20")), xlsx.Int(1)},
			{xlsx.Date(day), xlsx.Text("bell\x07 ok")},
		},
	}
	var buf bytes.Buffer
	if err := xlsx.Write(&buf, s); err != nil {
		t.Fatal(err)
	}
	p := parts(t, buf.Bytes())
	for _, name := range []string{"[Content_Types].xml", "_rels/.rels", "xl/workbook.xml", "xl/_rels/workbook.xml.rels", "xl/styles.xml", "xl/worksheets/sheet1.xml"} {
		if _, ok := p[name]; !ok {
			t.Errorf("missing part %s", name)
		}
	}
	if !strings.Contains(string(p["xl/workbook.xml"]), `<sheet name="Gastos"`) ||
		!strings.Contains(string(p["xl/workbook.xml"]), `&#39;Gastos&#39;!$A$1:$E$4`) {
		t.Errorf("workbook = %s", p["xl/workbook.xml"])
	}

	var ws sheetXML
	if err := xml.Unmarshal(p["xl/worksheets/sheet1.xml"], &ws); err != nil {
		t.Fatal(err)
	}
	if ws.Pane.State != "frozen" || ws.AutoFilter.Ref != "A1:E4" || len(ws.Cols) != 5 || ws.Cols[0].Width != "11" || ws.Cols[1].Width != "12" {
		t.Errorf("sheet setup: pane %q, filter %q, cols %+v", ws.Pane.State, ws.AutoFilter.Ref, ws.Cols)
	}
	if len(ws.Rows) != 4 {
		t.Fatalf("rows = %d", len(ws.Rows))
	}
	header := ws.Rows[0].Cells
	if len(header) != 5 || header[1].Inline != "Descripción" || header[1].Style != "1" || header[1].Type != "inlineStr" {
		t.Errorf("header = %+v", header)
	}

	r1 := ws.Rows[1].Cells
	want := []cell{
		{Ref: "A2", Style: "3", Value: "46329"},
		{Ref: "B2", Type: "inlineStr", Inline: `Súper <Coto> & "más"`},
		{Ref: "C2", Style: "2", Value: "19450.50"},
		{Ref: "D2", Value: "3"},
	}
	if len(r1) != len(want) {
		t.Fatalf("row 2 has %d cells (empty ones are skipped): %+v", len(r1), r1)
	}
	for i := range want {
		if r1[i] != want[i] {
			t.Errorf("row 2 cell %d = %+v, want %+v", i, r1[i], want[i])
		}
	}

	r2 := ws.Rows[2].Cells
	// Text that looks like a formula stays text.
	if r2[1].Type != "inlineStr" || r2[1].Inline != "=1+1" || r2[2].Value != "-20.00" {
		t.Errorf("row 3 = %+v", r2)
	}
	if got := ws.Rows[3].Cells[1].Inline; got != "bell� ok" {
		t.Errorf("control character = %q", got)
	}
}

func TestWriteEmptySheetKeepsHeader(t *testing.T) {
	var buf bytes.Buffer
	if err := xlsx.Write(&buf, xlsx.Sheet{Name: "Ingresos", Columns: []xlsx.Column{{Header: "Fecha"}, {Header: "Monto"}}}); err != nil {
		t.Fatal(err)
	}
	var ws sheetXML
	if err := xml.Unmarshal(parts(t, buf.Bytes())["xl/worksheets/sheet1.xml"], &ws); err != nil {
		t.Fatal(err)
	}
	if len(ws.Rows) != 1 || ws.AutoFilter.Ref != "A1:B1" {
		t.Errorf("rows %d, filter %q", len(ws.Rows), ws.AutoFilter.Ref)
	}
}

func TestWriteRejectsBadSheets(t *testing.T) {
	cols := []xlsx.Column{{Header: "A"}}
	for name, s := range map[string]xlsx.Sheet{
		"no name":       {Columns: cols},
		"long name":     {Name: strings.Repeat("x", 32), Columns: cols},
		"bad character": {Name: "a/b", Columns: cols},
		"no columns":    {Name: "x"},
		"row too wide":  {Name: "x", Columns: cols, Rows: [][]xlsx.Cell{{xlsx.Int(1), xlsx.Int(2)}}},
	} {
		if err := xlsx.Write(io.Discard, s); err == nil {
			t.Errorf("%s: want an error", name)
		}
	}
}
