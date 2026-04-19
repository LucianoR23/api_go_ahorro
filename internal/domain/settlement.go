package domain

import (
	"time"

	"github.com/google/uuid"
)

// SettlementPayment: "A le pagó X a B" — registro del libro de deudas.
// No descuenta de ningún payment_method: la plata se movió afuera.
type SettlementPayment struct {
	ID           uuid.UUID
	HouseholdID  uuid.UUID
	FromUser     uuid.UUID
	ToUser       uuid.UUID
	AmountBase   float64
	BaseCurrency string
	Note         *string
	PaidAt       time.Time
	CreatedAt    time.Time
}

// BalanceRow: una fila de la matriz de deudas del hogar.
// Amount > 0 → From debe a To. Amount = 0 nunca se devuelve (el service
// filtra los saldados).
type BalanceRow struct {
	From   uuid.UUID `json:"from"`
	To     uuid.UUID `json:"to"`
	Amount float64   `json:"amount"`
}

// PairBalance: balance neto entre dos miembros, firmado.
// Positivo → From debe a To. Negativo → To debe a From. Cero → saldado.
type PairBalance struct {
	From   uuid.UUID
	To     uuid.UUID
	Amount float64
}
