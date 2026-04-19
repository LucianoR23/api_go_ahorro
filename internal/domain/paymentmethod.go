package domain

import (
	"time"

	"github.com/google/uuid"
)

// PaymentMethodKind representa el tipo de medio de pago.
// Matchea 1-a-1 con el CHECK de la tabla payment_methods.
type PaymentMethodKind string

const (
	KindCash     PaymentMethodKind = "cash"
	KindDebit    PaymentMethodKind = "debit"
	KindCredit   PaymentMethodKind = "credit"
	KindWallet   PaymentMethodKind = "wallet"
	KindTransfer PaymentMethodKind = "transfer"
)

// IsValid chequea que el kind sea uno de los soportados.
// Fallar acá temprano da mejor mensaje que dejar que la DB rechace con CHECK.
func (k PaymentMethodKind) IsValid() bool {
	switch k {
	case KindCash, KindDebit, KindCredit, KindWallet, KindTransfer:
		return true
	}
	return false
}

// AllowsInstallmentsForced devuelve (valor, esForzado) según el kind.
// Si esForzado=true, el service debe usar ese valor e ignorar el input.
// Si esForzado=false, el kind es configurable (wallet).
//
// Matriz (plan §3):
//
//	cash, debit, transfer → false forzado
//	credit                → true  forzado
//	wallet                → configurable
func (k PaymentMethodKind) AllowsInstallmentsForced() (bool, bool) {
	switch k {
	case KindCash, KindDebit, KindTransfer:
		return false, true
	case KindCredit:
		return true, true
	case KindWallet:
		return false, false // el default si el cliente no especifica
	}
	return false, true
}

// Bank: cuenta bancaria del user. No es compartida entre miembros del hogar.
// Soft-delete vía IsActive.
type Bank struct {
	ID          uuid.UUID `json:"id"`
	OwnerUserID uuid.UUID `json:"ownerUserId"`
	Name        string    `json:"name"`
	IsActive    bool      `json:"isActive"`
	CreatedAt   time.Time `json:"createdAt"`
}

// PaymentMethod: medio de pago propiedad de un user. Referenciado por expenses.
// BankID es opcional: efectivo y algunas wallets no tienen banco atrás.
type PaymentMethod struct {
	ID                 uuid.UUID         `json:"id"`
	OwnerUserID        uuid.UUID         `json:"ownerUserId"`
	BankID             *uuid.UUID        `json:"bankId,omitempty"`
	Name               string            `json:"name"`
	Kind               PaymentMethodKind `json:"kind"`
	AllowsInstallments bool              `json:"allowsInstallments"`
	IsActive           bool              `json:"isActive"`
	CreatedAt          time.Time         `json:"createdAt"`
}

// CreditCard: detalle asociado 1-a-1 a un PaymentMethod de kind=credit.
// LastFour es opcional (puede no haberse cargado).
// DebitPaymentMethodID apunta a la cuenta de la que se debita el resumen.
type CreditCard struct {
	ID                   uuid.UUID  `json:"id"`
	PaymentMethodID      uuid.UUID  `json:"paymentMethodId"`
	Alias                string     `json:"alias"`
	LastFour             *string    `json:"lastFour,omitempty"`
	DefaultClosingDay    int        `json:"defaultClosingDay"`
	DefaultDueDay        int        `json:"defaultDueDay"`
	DebitPaymentMethodID *uuid.UUID `json:"debitPaymentMethodId,omitempty"`
	CreatedAt            time.Time  `json:"createdAt"`
}

// PaymentMethodWithCard: vista combinada método + tarjeta (opcional).
// Se usa en listados donde el frontend necesita ambos.
type PaymentMethodWithCard struct {
	PaymentMethod
	CreditCard *CreditCard        `json:"creditCard,omitempty"`
	Periods    []CreditCardPeriod `json:"periods,omitempty"`
}
