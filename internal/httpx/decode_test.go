package httpx_test

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/LucianoR23/valuo_api/internal/domain"
	"github.com/LucianoR23/valuo_api/internal/httpx"
)

func TestDecodeJSON(t *testing.T) {
	type body struct {
		Name string    `json:"name"`
		Age  int       `json:"age"`
		ID   uuid.UUID `json:"id"`
	}
	for _, tc := range []struct {
		in, field, msg string
	}{
		{in: ``, field: "body", msg: "falta el cuerpo del pedido"},
		{in: `{"name":`, field: "body", msg: "JSON inválido"},
		{in: `{name}`, field: "body", msg: "JSON inválido"},
		{in: `{"age":"x"}`, field: "age", msg: "tipo de dato incorrecto"},
		{in: `{"nmae":"Ana"}`, field: "body", msg: "campo desconocido: nmae"},
		{in: `{"id":"not-a-uuid"}`, field: "body", msg: "algún campo tiene un valor inválido"},
	} {
		r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(tc.in))
		var b body
		err := httpx.DecodeJSON(r, &b)
		var v *domain.ValidationError
		if !errors.As(err, &v) {
			t.Fatalf("%q: error %v is not a validation error", tc.in, err)
		}
		if v.Field != tc.field || v.Message != tc.msg {
			t.Errorf("%q = %s: %s, want %s: %s", tc.in, v.Field, v.Message, tc.field, tc.msg)
		}
		if strings.Contains(v.Message, "json:") {
			t.Errorf("%q leaks the Go decoder text: %s", tc.in, v.Message)
		}
	}

	r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"name":"Ana","age":3}`))
	var ok body
	if err := httpx.DecodeJSON(r, &ok); err != nil || ok.Name != "Ana" || ok.Age != 3 {
		t.Errorf("valid body = %+v, %v", ok, err)
	}

	big := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"name":"`+strings.Repeat("a", 100)+`"}`))
	big.Body = http.MaxBytesReader(httptest.NewRecorder(), big.Body, 10)
	var v *domain.ValidationError
	if err := httpx.DecodeJSON(big, &ok); !errors.As(err, &v) || v.Message != "el pedido es demasiado grande" {
		t.Errorf("oversized body = %v", err)
	}
}
