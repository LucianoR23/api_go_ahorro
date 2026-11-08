package push_test

import (
	"encoding/json"
	"testing"

	"github.com/LucianoR23/valuo_api/internal/push"
)

// Plan 1.9: the service worker reads bodyWithAmounts only on devices that
// opted in; without one, the field is left out.
func TestPayloadJSON(t *testing.T) {
	cases := []struct {
		in   push.Payload
		want string
	}{
		{push.Payload{Title: "Pago registrado", Body: "Ana registró un pago.", BodyWithAmounts: "Ana registró un pago de 10.00 ARS"},
			`{"title":"Pago registrado","body":"Ana registró un pago.","bodyWithAmounts":"Ana registró un pago de 10.00 ARS"}`},
		{push.Payload{Title: "Hola", Body: "Sin montos"}, `{"title":"Hola","body":"Sin montos"}`},
	}
	for _, c := range cases {
		got, err := json.Marshal(c.in)
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != c.want {
			t.Errorf("got %s, want %s", got, c.want)
		}
	}
}
