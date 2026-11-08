package settlements

import (
	"testing"

	"github.com/shopspring/decimal"

	"github.com/LucianoR23/valuo_api/internal/domain"
)

// Plan 1.9: the push body has no amount; the in-app notification keeps it.
func TestSettlementMessage(t *testing.T) {
	lock, full := settlementMessage("Ana", domain.SettlementPayment{
		AmountBase: decimal.RequireFromString("2500"), BaseCurrency: "ARS",
	})
	if want := "Ana registró un pago."; lock != want {
		t.Errorf("lock screen = %q, want %q", lock, want)
	}
	if want := "Ana registró un pago de 2500.00 ARS"; full != want {
		t.Errorf("full = %q, want %q", full, want)
	}
}
