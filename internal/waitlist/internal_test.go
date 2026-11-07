package waitlist

import (
	"context"
	"log/slog"
	"testing"

	"github.com/LucianoR23/valuo_api/internal/testutil/testdb"
)

func TestSanitizeAttribution(t *testing.T) {
	s := func(v string) *string { return &v }
	for in, want := range map[string]string{
		"Instagram":         "instagram",
		"  Mercado Libre  ": "mercado-libre",
		"news letter!!!":    "news-letter",
		"--a__b..c--":       "a__b..c",
		"ＦＡＣＥＢＯＯＫ":          "facebook", // NFKC folds full-width letters
		"Ñandú 2026":        "ñandú-2026",
		"!!!":               "",
		"   ":               "",
	} {
		got := sanitizeAttribution(s(in))
		if want == "" {
			if got != nil {
				t.Errorf("%q -> %q, want nil", in, *got)
			}
			continue
		}
		if got == nil || *got != want {
			t.Errorf("%q -> %v, want %q", in, got, want)
		}
	}
	long := sanitizeAttribution(s("a" + repeat("b", 80)))
	if long == nil || len([]rune(*long)) != 64 {
		t.Errorf("not cut to 64 runes: %v", long)
	}
	if sanitizeAttribution(nil) != nil {
		t.Error("nil in, non-nil out")
	}
}

func repeat(s string, n int) string {
	out := ""
	for range n {
		out += s
	}
	return out
}

func TestUnsubscribeTokens(t *testing.T) {
	a := &Service{cfg: Config{UnsubscribeKey: []byte("k1")}}
	b := &Service{cfg: Config{UnsubscribeKey: []byte("k2")}}
	id := [16]byte{1, 2, 3}
	tok := a.unsubscribeToken(id)
	if got, ok := a.parseUnsubscribeToken(tok); !ok || got != id {
		t.Errorf("own token rejected: %v %v", got, ok)
	}
	if a.unsubscribeToken(id) != tok {
		t.Error("tokens are not stable")
	}
	if _, ok := b.parseUnsubscribeToken(tok); ok {
		t.Error("a token signed with another key was accepted")
	}
	if _, ok := a.parseUnsubscribeToken("short"); ok {
		t.Error("garbage accepted")
	}
}

func TestCleanup(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `INSERT INTO waitlist_entries (email, form, notice_version, created_at, confirmed_at) VALUES
		('old-unconfirmed@x.test', 'hero', 'v', now() - interval '31 days', NULL),
		('old-confirmed@x.test',   'hero', 'v', now() - interval '31 days', now() - interval '30 days'),
		('new-unconfirmed@x.test', 'hero', 'v', now() - interval '2 days', NULL)`); err != nil {
		t.Fatal(err)
	}
	svc := NewService(pool, Config{}, nil, nil, slog.New(slog.DiscardHandler))
	if n, err := svc.Cleanup(ctx); err != nil || n != 1 {
		t.Fatalf("cleanup = %d, %v", n, err)
	}
	var left int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM waitlist_entries`).Scan(&left); err != nil || left != 2 {
		t.Errorf("left = %d, %v", left, err)
	}
}

func TestCSVSafe(t *testing.T) {
	for in, want := range map[string]string{"=SUM(A1)": "'=SUM(A1)", "+1": "'+1", "-1": "'-1", "@x": "'@x", "ana@x.test": "ana@x.test", "": ""} {
		if got := csvSafe(in); got != want {
			t.Errorf("csvSafe(%q) = %q, want %q", in, got, want)
		}
	}
}
