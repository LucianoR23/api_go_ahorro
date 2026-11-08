package expenses

import (
	"testing"

	"github.com/shopspring/decimal"
)

// Plan 1.9: a push can show on a locked screen, so its body carries no
// amount; the in-app notification keeps it.
func TestSharedExpenseMessage(t *testing.T) {
	lock, full := sharedExpenseMessage("Ana", "Súper", decimal.RequireFromString("1234.5"), "ARS")
	if want := `Ana cargó "Súper". Tocá para ver tu parte.`; lock != want {
		t.Errorf("lock screen = %q, want %q", lock, want)
	}
	if want := `Ana cargó "Súper". Te toca 1234.50 ARS.`; full != want {
		t.Errorf("full = %q, want %q", full, want)
	}
}
