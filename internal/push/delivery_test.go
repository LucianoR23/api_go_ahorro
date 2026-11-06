package push_test

import (
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	webpush "github.com/SherClockHolmes/webpush-go"

	"github.com/LucianoR23/api_go_ahorra/internal/config"
	"github.com/LucianoR23/api_go_ahorra/internal/testutil/apitest"
)

// browserKeys returns subscription keys a real browser would send: an
// uncompressed P-256 public key and a 16-byte auth secret, base64url.
func browserKeys(t *testing.T) map[string]string {
	t.Helper()
	priv, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	secret := make([]byte, 16)
	_, _ = rand.Read(secret)
	return map[string]string{
		"p256dh": base64.RawURLEncoding.EncodeToString(priv.PublicKey().Bytes()),
		"auth":   base64.RawURLEncoding.EncodeToString(secret),
	}
}

// eventually polls cond until it holds or 5s pass (delivery is async).
func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestPushDelivery(t *testing.T) {
	t.Parallel()
	priv, pub, err := webpush.GenerateVAPIDKeys()
	if err != nil {
		t.Fatal(err)
	}

	// Local stand-in for FCM/Mozilla: /ok accepts, /gone answers 410.
	var okHits, goneHits atomic.Int32
	var authHeader atomic.Value
	pushSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authHeader.Store(r.Header.Get("Authorization"))
		if strings.HasSuffix(r.URL.Path, "/gone") {
			goneHits.Add(1)
			w.WriteHeader(http.StatusGone)
			return
		}
		okHits.Add(1)
		w.WriteHeader(http.StatusCreated)
	}))
	t.Cleanup(pushSrv.Close)

	s := apitest.New(t, apitest.WithConfig(func(c *config.Config) {
		c.VAPIDPublicKey, c.VAPIDPrivateKey = pub, priv
		c.PushExtraHosts = []string{strings.TrimPrefix(pushSrv.URL, "http://")} // local stand-in (plan 0.6 allowlist)
	}))
	admin := s.Register(t)
	s.MakeSuperadmin(t, admin)

	var k struct {
		PublicKey string `json:"publicKey"`
		Enabled   bool   `json:"enabled"`
	}
	s.Do(t, apitest.Req{Method: http.MethodGet, Path: "/push/vapid-public-key"}).Status(t, http.StatusOK).JSON(t, &k)
	if !k.Enabled || k.PublicKey != pub {
		t.Fatalf("vapid = %+v", k)
	}

	var r struct {
		Sent   bool   `json:"sent"`
		Reason string `json:"reason"`
	}
	s.Do(t, apitest.Req{Method: http.MethodPost, Path: "/admin/push/test", Token: admin.Token}).Status(t, http.StatusOK).JSON(t, &r)
	if r.Sent || !strings.Contains(r.Reason, "no hay dispositivos") {
		t.Errorf("test push without devices = %+v", r)
	}

	for _, path := range []string{"/ok", "/gone"} {
		s.Do(t, apitest.Req{Method: http.MethodPost, Path: "/push/subscriptions", Token: admin.Token,
			Body: map[string]any{"endpoint": pushSrv.URL + path, "keys": browserKeys(t)}}).Status(t, http.StatusCreated)
	}

	s.Do(t, apitest.Req{Method: http.MethodPost, Path: "/admin/push/test", Token: admin.Token}).Status(t, http.StatusOK).JSON(t, &r)
	if !r.Sent {
		t.Fatalf("test push = %+v, want sent", r)
	}

	eventually(t, "both endpoints to be hit", func() bool { return okHits.Load() == 1 && goneHits.Load() == 1 })
	if h, _ := authHeader.Load().(string); !strings.HasPrefix(h, "vapid t=") {
		t.Errorf("Authorization = %q, want a VAPID header", h)
	}
	eventually(t, "the 410 subscription to be removed", func() bool {
		subs := list(t, s, admin)
		return len(subs) == 1 && strings.HasSuffix(subs[0].Endpoint, "/ok")
	})
}
