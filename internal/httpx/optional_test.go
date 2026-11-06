package httpx_test

import (
	"encoding/json"
	"testing"

	"github.com/LucianoR23/api_go_ahorra/internal/httpx"
)

func TestOptional(t *testing.T) {
	type body struct {
		Name httpx.Optional[string] `json:"name"`
		Age  httpx.Optional[int]    `json:"age"`
	}
	for _, tc := range []struct {
		in        string
		set, null bool
		value     string
	}{
		{in: `{}`},
		{in: `{"name":null}`, set: true, null: true},
		{in: `{"name":"Ana"}`, set: true, value: "Ana"},
		{in: `{"name":""}`, set: true, value: ""},
	} {
		var b body
		if err := json.Unmarshal([]byte(tc.in), &b); err != nil {
			t.Fatalf("%s: %v", tc.in, err)
		}
		if b.Name.Set != tc.set || b.Name.Null != tc.null || b.Name.Value != tc.value {
			t.Errorf("%s = %+v", tc.in, b.Name)
		}
		if b.Age.Set {
			t.Errorf("%s: absent age reported as set", tc.in)
		}
	}
	var b body
	if err := json.Unmarshal([]byte(`{"age":"x"}`), &b); err == nil {
		t.Error("a string for an int field must fail")
	}
}
