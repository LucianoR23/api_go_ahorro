package insights

import "testing"

// Plan 1.9: the push body has no amount; the in-app notification keeps it.
func TestInstallmentDueMessage(t *testing.T) {
	lock, full := installmentDueMessage(InstallmentDueForReminder{
		Description: "Heladera", InstallmentNumber: 2, TotalInstallments: 6,
		AmountBase: 15000, BaseCurrency: "ARS",
	})
	if want := `En 3 días vence la cuota 2/6 de "Heladera".`; lock != want {
		t.Errorf("lock screen = %q, want %q", lock, want)
	}
	if want := `En 3 días vence la cuota 2/6 de "Heladera" (15000.00 ARS).`; full != want {
		t.Errorf("full = %q, want %q", full, want)
	}
}
