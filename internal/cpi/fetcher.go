package cpi

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/shopspring/decimal"
)

// Upstreams, both public and without auth:
//   - INDEC's own CSV, the main source: it has a month the day INDEC
//     publishes it (around the 12th of the next month).
//   - The Series API of datos.gob.ar, the fallback: the same series
//     (148.3_INIVELNAL_DICI_M_26), republished weeks later.
const (
	indecURL  = "https://www.indec.gob.ar/ftp/cuadros/economia/serie_ipc_divisiones.csv"
	seriesURL = "https://apis.datos.gob.ar/series/api/series/?ids=148.3_INIVELNAL_DICI_M_26&format=json&limit=1000"
)

// minPoints guards against a truncated or changed file: the series has
// had over a hundred months since 2016.
const minPoints = 12

// Fetcher downloads the index. The *http.Client is injected so main sets
// the timeout and tests point it at a local server.
type Fetcher struct {
	client    *http.Client
	indecURL  string
	seriesURL string
}

func NewFetcher(client *http.Client) *Fetcher {
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	return &Fetcher{client: client, indecURL: indecURL, seriesURL: seriesURL}
}

// Fetch returns the whole series from INDEC, or from datos.gob.ar when
// INDEC fails. It fails only when neither answered; when the fallback did,
// the points come with INDEC's error so the caller can log it.
func (f *Fetcher) Fetch(ctx context.Context) ([]Point, error) {
	points, errMain := f.fetchINDEC(ctx)
	if errMain == nil {
		return points, nil
	}
	points, errBackup := f.fetchSeries(ctx)
	if errBackup != nil {
		return nil, errors.Join(errMain, errBackup)
	}
	return points, errMain
}

func (f *Fetcher) get(ctx context.Context, url string, limit int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("cpi.Fetch %s: build request: %w", url, err)
	}
	resp, err := f.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("cpi.Fetch %s: do: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("cpi.Fetch %s: status %d: %s", url, resp.StatusCode, string(body))
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, limit))
	if err != nil {
		return nil, fmt.Errorf("cpi.Fetch %s: read: %w", url, err)
	}
	return body, nil
}

// fetchINDEC reads the national nivel general rows (Codigo 0, Region
// Nacional) of INDEC's CSV: semicolon separated, Latin-1, decimal comma.
func (f *Fetcher) fetchINDEC(ctx context.Context) ([]Point, error) {
	body, err := f.get(ctx, f.indecURL, 32<<20)
	if err != nil {
		return nil, err
	}
	points, err := parseINDEC(body)
	if err != nil {
		return nil, fmt.Errorf("cpi.Fetch %s: %w", f.indecURL, err)
	}
	return points, nil
}

func parseINDEC(body []byte) ([]Point, error) {
	r := csv.NewReader(bytes.NewReader(bytes.TrimPrefix(body, []byte("\xef\xbb\xbf"))))
	r.Comma = ';'
	r.FieldsPerRecord = -1
	r.LazyQuotes = true
	header, err := r.Read()
	if err != nil {
		return nil, fmt.Errorf("read header: %w", err)
	}
	col := map[string]int{}
	for i, h := range header {
		col[strings.TrimSpace(h)] = i
	}
	for _, name := range []string{"Codigo", "Periodo", "Indice_IPC", "Region"} {
		if _, ok := col[name]; !ok {
			return nil, fmt.Errorf("column %s missing", name)
		}
	}
	var points []Point
	for {
		rec, err := r.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("read row: %w", err)
		}
		if len(rec) < len(header) || rec[col["Codigo"]] != "0" || rec[col["Region"]] != "Nacional" {
			continue
		}
		month, err := time.Parse("200601", rec[col["Periodo"]])
		if err != nil {
			return nil, fmt.Errorf("period %q: %w", rec[col["Periodo"]], err)
		}
		v, err := decimal.NewFromString(strings.ReplaceAll(rec[col["Indice_IPC"]], ",", "."))
		if err != nil {
			return nil, fmt.Errorf("index %q: %w", rec[col["Indice_IPC"]], err)
		}
		points = append(points, Point{Month: month, Value: v})
	}
	return checked(points)
}

// fetchSeries reads the datos.gob.ar answer: {"data": [["2026-08-01", 12276.766], ...]}.
func (f *Fetcher) fetchSeries(ctx context.Context) ([]Point, error) {
	body, err := f.get(ctx, f.seriesURL, 1<<20)
	if err != nil {
		return nil, err
	}
	var resp struct {
		Data [][2]json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("cpi.Fetch %s: decode: %w", f.seriesURL, err)
	}
	points := make([]Point, 0, len(resp.Data))
	for _, row := range resp.Data {
		var day string
		if err := json.Unmarshal(row[0], &day); err != nil {
			return nil, fmt.Errorf("cpi.Fetch %s: date: %w", f.seriesURL, err)
		}
		month, err := time.Parse("2006-01-02", day)
		if err != nil {
			return nil, fmt.Errorf("cpi.Fetch %s: date %q: %w", f.seriesURL, day, err)
		}
		if string(row[1]) == "null" {
			continue
		}
		v, err := decimal.NewFromString(string(row[1]))
		if err != nil {
			return nil, fmt.Errorf("cpi.Fetch %s: value %s: %w", f.seriesURL, row[1], err)
		}
		points = append(points, Point{Month: MonthOf(month), Value: v})
	}
	p, err := checked(points)
	if err != nil {
		return nil, fmt.Errorf("cpi.Fetch %s: %w", f.seriesURL, err)
	}
	return p, nil
}

// checked rejects a series too short to be the real one, or with a
// non-positive or repeated month.
func checked(points []Point) ([]Point, error) {
	if len(points) < minPoints {
		return nil, fmt.Errorf("only %d months, want at least %d", len(points), minPoints)
	}
	seen := make(map[time.Time]bool, len(points))
	for _, p := range points {
		if !p.Value.IsPositive() {
			return nil, fmt.Errorf("month %s: index %s", p.Month.Format("2006-01"), p.Value)
		}
		if seen[p.Month] {
			return nil, fmt.Errorf("month %s repeated", p.Month.Format("2006-01"))
		}
		seen[p.Month] = true
	}
	return points, nil
}
