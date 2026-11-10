package cpi

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/LucianoR23/valuo_api/internal/testutil/testdb"
)

func TestMain(m *testing.M) { os.Exit(testdb.Main(m)) }

func month(y int, m time.Month) time.Time { return time.Date(y, m, 1, 0, 0, 0, 0, time.UTC) }

func dec(s string) decimal.Decimal { return decimal.RequireFromString(s) }

func TestSeriesFactor(t *testing.T) {
	t.Parallel()
	s := NewSeries([]Point{
		{month(2026, 3), dec("125")},
		{month(2026, 1), dec("100")},
		{month(2026, 2), dec("110")},
	})
	if got := s.Through(); !got.Equal(month(2026, 3)) {
		t.Fatalf("through = %v", got)
	}
	cases := []struct {
		name string
		at   time.Time
		want string
	}{
		{"a published month: last over its own", time.Date(2026, 1, 20, 15, 0, 0, 0, time.Local), "1.25"},
		{"the last published month", month(2026, 3), "1"},
		{"a month not published yet counts as the last", month(2026, 5), "1"},
		{"a month before the series counts as the first", month(2025, 6), "1.25"},
	}
	for _, c := range cases {
		if got := s.Factor(c.at); !got.Equal(dec(c.want)) {
			t.Errorf("%s: factor = %s, want %s", c.name, got, c.want)
		}
	}
	if f := s.Factor(month(2026, 2)); f.Mul(dec("20000")).Round(2).String() != "22727.27" {
		t.Errorf("February: 20000 x %s", f)
	}
	if f := (Series{}).Factor(month(2026, 2)); !f.Equal(dec("1")) || !(Series{}).Empty() {
		t.Errorf("empty series factor = %s", f)
	}
}

// indecCSV builds INDEC's file: Latin-1, semicolons, decimal comma, every
// region and COICOP division. Only Codigo 0 / Nacional is the series.
func indecCSV(months int) string {
	var b strings.Builder
	b.WriteString("Codigo;Descripcion;Clasificador;Periodo;Indice_IPC;v_m_IPC;v_i_a_IPC;Region\r\n")
	start := month(2025, 9)
	for i := 0; i < months; i++ {
		p := start.AddDate(0, i, 0).Format("200601")
		fmt.Fprintf(&b, "0;NIVEL GENERAL;Nivel general y divisiones COICOP;%s;%d,5;NA;NA;GBA\r\n", p, 9000+i)
		fmt.Fprintf(&b, "0;NIVEL GENERAL;Nivel general y divisiones COICOP;%s;%d,1508;2,1;31,8;Nacional\r\n", p, 10000+i*100)
		fmt.Fprintf(&b, "10;Educaci\xf3n;Nivel general y divisiones COICOP;%s;1,0;NA;NA;Nacional\r\n", p)
	}
	return b.String()
}

func seriesJSON(months int) string {
	rows := make([]string, 0, months+1)
	start := month(2025, 8)
	for i := 0; i < months; i++ {
		rows = append(rows, fmt.Sprintf(`["%s", %d.766]`, start.AddDate(0, i, 0).Format("2006-01-02"), 20000+i))
	}
	rows = append(rows, `["2026-12-01", null]`)
	return `{"data": [` + strings.Join(rows, ",") + `], "count": 1}`
}

type upstream struct {
	status int
	body   string
}

// fakeUpstreams serves INDEC at /indec and datos.gob.ar at /series; a
// missing route answers 502.
func fakeUpstreams(t *testing.T, routes map[string]upstream) *Fetcher {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, ok := routes[r.URL.Path]
		if !ok {
			u = upstream{http.StatusBadGateway, "down"}
		}
		w.WriteHeader(u.status)
		_, _ = io.WriteString(w, u.body)
	}))
	t.Cleanup(srv.Close)
	f := NewFetcher(srv.Client())
	f.indecURL = srv.URL + "/indec"
	f.seriesURL = srv.URL + "/series"
	return f
}

func TestFetch(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	t.Run("INDEC's national nivel general", func(t *testing.T) {
		points, err := fakeUpstreams(t, map[string]upstream{
			"/indec":  {200, "\xef\xbb\xbf" + indecCSV(13)},
			"/series": {200, seriesJSON(13)},
		}).Fetch(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if len(points) != 13 {
			t.Fatalf("got %d points", len(points))
		}
		if p := points[12]; !p.Month.Equal(month(2026, 9)) || !p.Value.Equal(dec("11200.1508")) {
			t.Errorf("last point = %+v", p)
		}
	})

	t.Run("INDEC down: datos.gob.ar, with INDEC's error to log", func(t *testing.T) {
		points, err := fakeUpstreams(t, map[string]upstream{"/series": {200, seriesJSON(13)}}).Fetch(ctx)
		if err == nil || !strings.Contains(err.Error(), "status 502") {
			t.Errorf("err = %v, want INDEC's 502", err)
		}
		if len(points) != 13 || !points[0].Month.Equal(month(2025, 8)) || !points[0].Value.Equal(dec("20000.766")) {
			t.Errorf("points = %d, first %+v", len(points), points[0])
		}
	})

	t.Run("a truncated INDEC file falls back", func(t *testing.T) {
		points, err := fakeUpstreams(t, map[string]upstream{
			"/indec":  {200, indecCSV(3)},
			"/series": {200, seriesJSON(13)},
		}).Fetch(ctx)
		if err == nil || !strings.Contains(err.Error(), "only 3 months") || len(points) != 13 {
			t.Errorf("err = %v, points = %d", err, len(points))
		}
	})

	t.Run("a changed INDEC file falls back", func(t *testing.T) {
		_, err := fakeUpstreams(t, map[string]upstream{
			"/indec":  {200, "Code;Period;Value\n0;202609;1\n"},
			"/series": {200, seriesJSON(13)},
		}).Fetch(ctx)
		if err == nil || !strings.Contains(err.Error(), "column Codigo missing") {
			t.Errorf("err = %v", err)
		}
	})

	t.Run("both down", func(t *testing.T) {
		points, err := fakeUpstreams(t, map[string]upstream{"/series": {200, `{"data": []}`}}).Fetch(ctx)
		if err == nil || points != nil {
			t.Errorf("points = %v, err = %v", points, err)
		}
	})
}

func TestRefresh(t *testing.T) {
	t.Parallel()
	pool := testdb.New(t)
	ctx := context.Background()
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))

	routes := map[string]upstream{"/indec": {200, indecCSV(13)}}
	svc := NewService(pool, fakeUpstreams(t, routes), quiet)
	if err := svc.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	s, err := svc.Series(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !s.Through().Equal(month(2026, 9)) || !s.Factor(month(2025, 9)).Equal(dec("11200.1508").Div(dec("10000.1508"))) {
		t.Errorf("stored series through %v, factor %s", s.Through(), s.Factor(month(2025, 9)))
	}

	t.Run("a new month and a revised one", func(t *testing.T) {
		csv := strings.Replace(indecCSV(14), "202509;10000,1508", "202509;10001", 1)
		svc := NewService(pool, fakeUpstreams(t, map[string]upstream{"/indec": {200, csv}}), quiet)
		if err := svc.Refresh(ctx); err != nil {
			t.Fatal(err)
		}
		var n int
		var first string
		if err := pool.QueryRow(ctx, `SELECT count(*), min(value)::text FROM cpi_index`).Scan(&n, &first); err != nil {
			t.Fatal(err)
		}
		if n != 14 || first != "10001.0000" {
			t.Errorf("rows = %d, first = %s", n, first)
		}
	})

	t.Run("both upstreams down keeps what is stored", func(t *testing.T) {
		svc := NewService(pool, fakeUpstreams(t, nil), quiet)
		if err := svc.Refresh(ctx); err == nil {
			t.Error("want an error")
		}
		s, err := svc.Series(ctx)
		if err != nil || !s.Through().Equal(month(2026, 10)) {
			t.Errorf("series through %v, err %v", s.Through(), err)
		}
	})
}
