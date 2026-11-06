package push

import "testing"

func TestEndpointAllowed(t *testing.T) {
	for ep, want := range map[string]bool{
		"https://fcm.googleapis.com/fcm/send/abc":                true,
		"https://updates.push.services.mozilla.com/wpush/v2/abc": true,
		"https://web.push.apple.com/QGx":                         true,
		"https://wns2-bl2p.notify.windows.com/w/?token=x":        true,
		"http://fcm.googleapis.com/fcm/send/abc":                 false, // not https
		"https://fcm.googleapis.com:8443/fcm/send/abc":           false, // odd port
		"https://fcm.googleapis.com.evil.test/x":                 false,
		"https://evilnotify.windows.com.attacker.test/x":         false,
		"http://169.254.169.254/latest/meta-data":                false,
		"https://user:pass@fcm.googleapis.com/x":                 false,
		"not a url":                                              false,
		"http://127.0.0.1:9999/ok":                               false, // only when listed as extra
	} {
		if got := endpointAllowed(ep, nil); got != want {
			t.Errorf("endpointAllowed(%q) = %v, want %v", ep, got, want)
		}
	}
	if !endpointAllowed("http://127.0.0.1:9999/ok", []string{"127.0.0.1:9999"}) {
		t.Error("an extra host was refused")
	}
}
