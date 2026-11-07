package monitoring

import "testing"

func TestScrub(t *testing.T) {
	t.Parallel()
	cases := []struct{ name, in, want string }{
		{"empty", "", ""},
		{"plain", "settlements.Create: context canceled", "settlements.Create: context canceled"},
		{"email", "no user ana.perez+x@example.com.ar", "no user [email]"},
		{"uuid", "household 3f2b8c1e-9a4d-4c7e-8f00-1b2c3d4e5f60 not found", "household [id] not found"},
		{"amount", "excede la deuda actual (1234.56 ARS)", "excede la deuda actual ([n] ARS)"},
		{"amount with comma", "monto 1.234,56", "monto [n]"},
		{"integer", "paid 300", "paid [n]"},
		{"jwt", "bad token eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxIn0.sig-nature_x", "bad token [token]"},
		{"opaque token", "session 9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08 gone", "session [token] gone"},
		{"constraint name kept", "violates expense_installments_household_id_fkey", "violates expense_installments_household_id_fkey"},
		{"pg detail", "duplicate key (SQLSTATE 23505): Key (email)=(Juan Perez) already exists", "duplicate key (SQLSTATE 23505): Key (email)=([redacted]) already exists"},
		{"quoted", `invalid input syntax for type numeric: "cena con Ana"`, `invalid input syntax for type numeric: "[redacted]"`},
		{"two sqlstates", "SQLSTATE 40001 then SQLSTATE 23503 at 12", "SQLSTATE 40001 then SQLSTATE 23503 at [n]"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			if got := Scrub(c.in); got != c.want {
				t.Errorf("Scrub(%q)\n got %q\nwant %q", c.in, got, c.want)
			}
		})
	}
}
