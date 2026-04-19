package domain

import (
	"time"

	"github.com/google/uuid"
)

// SplitRule: peso de un miembro para dividir gastos compartidos del hogar.
// weight decimal libre (se normaliza al dividir). Default 1.0 = parejo.
type SplitRule struct {
	HouseholdID uuid.UUID
	UserID      uuid.UUID
	Weight      float64
	UpdatedAt   time.Time
}
