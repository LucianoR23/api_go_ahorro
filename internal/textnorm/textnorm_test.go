package textnorm

import "testing"

func TestKey(t *testing.T) {
	cases := map[string]string{
		"Súper":           "super",
		"  SUPER ":        "super",
		"Servicios/Luz":   "servicios luz",
		"Pingüino Café!!": "pinguino cafe",
		"":                "",
	}
	for in, want := range cases {
		if got := Key(in); got != want {
			t.Errorf("Key(%q) = %q, want %q", in, got, want)
		}
	}
}
