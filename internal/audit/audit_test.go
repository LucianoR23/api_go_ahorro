package audit

import (
	"context"
	"log/slog"
	"os"
	"testing"

	"github.com/LucianoR23/valuo_api/internal/testutil/testdb"
)

func TestMain(m *testing.M) { os.Exit(testdb.Main(m)) }

func TestDeviceLabel(t *testing.T) {
	for ua, want := range map[string]string{
		"": "Dispositivo desconocido",
		"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/141.0 Safari/537.36":                "Chrome en Windows",
		"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/141.0 Safari/537.36 Edg/141.0":      "Edge en Windows",
		"Mozilla/5.0 (Macintosh; Intel Mac OS X 15_0) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.0 Safari/605.1.15":         "Safari en macOS",
		"Mozilla/5.0 (iPhone; CPU iPhone OS 18_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Mobile/15E148 Safari/604.1": "Safari en iOS",
		"Mozilla/5.0 (Linux; Android 15) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/141.0 Mobile Safari/537.36":                   "Chrome en Android",
		"Mozilla/5.0 (X11; Linux x86_64; rv:140.0) Gecko/20100101 Firefox/140.0":                                                     "Firefox en Linux",
		"curl/8.5.0": "Navegador",
	} {
		if got := DeviceLabel(ua); got != want {
			t.Errorf("DeviceLabel(%q) = %q, want %q", ua, got, want)
		}
	}
}

func TestRetention(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	exec := func(sql string) {
		t.Helper()
		if _, err := pool.Exec(ctx, sql); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO audit_events (action, ip, created_at) VALUES
		('recent', '1.1.1.1', now() - interval '1 day'),
		('week_old', '2.2.2.2', now() - interval '8 days'),
		('year_old', '3.3.3.3', now() - interval '13 months')`)

	if err := ApplyRetention(ctx, pool, slog.New(slog.DiscardHandler)); err != nil {
		t.Fatal(err)
	}

	rows, err := pool.Query(ctx, `SELECT action, ip IS NOT NULL FROM audit_events ORDER BY created_at DESC`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	got := map[string]bool{}
	for rows.Next() {
		var action string
		var hasIP bool
		if err := rows.Scan(&action, &hasIP); err != nil {
			t.Fatal(err)
		}
		got[action] = hasIP
	}
	if len(got) != 2 || !got["recent"] || got["week_old"] {
		t.Errorf("after retention = %v, want recent with IP, week_old without, year_old gone", got)
	}
	if _, ok := got["year_old"]; ok {
		t.Error("an event older than 12 months survived")
	}
}

// A nil Recorder is a no-op: handlers call it without checking.
func TestNilRecorder(t *testing.T) {
	var r *Recorder
	r.Record(context.Background(), nil, Event{Action: "x"})
	if err := r.DeleteForUser(context.Background(), [16]byte{}); err != nil {
		t.Errorf("nil DeleteForUser = %v", err)
	}
}
