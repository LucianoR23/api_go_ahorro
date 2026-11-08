package paymethods

import (
	"errors"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/LucianoR23/valuo_api/internal/db/sqlc"
	"github.com/LucianoR23/valuo_api/internal/domain"
)

func bankToDomain(b sqlcgen.Bank) domain.Bank {
	return domain.Bank{
		ID:          b.ID,
		OwnerUserID: b.OwnerUserID,
		Name:        b.Name,
		IsActive:    b.IsActive,
		CreatedAt:   b.CreatedAt.Time,
	}
}

func pmToDomain(pm sqlcgen.PaymentMethod) domain.PaymentMethod {
	return domain.PaymentMethod{
		ID:                 pm.ID,
		OwnerUserID:        pm.OwnerUserID,
		BankID:             pm.BankID,
		Name:               pm.Name,
		Kind:               domain.PaymentMethodKind(pm.Kind),
		AllowsInstallments: pm.AllowsInstallments,
		IsActive:           pm.IsActive,
		CreatedAt:          pm.CreatedAt.Time,
	}
}

func ccToDomain(cc sqlcgen.CreditCard) domain.CreditCard {
	var lastFour *string
	if cc.LastFour.Valid {
		v := cc.LastFour.String
		lastFour = &v
	}
	return domain.CreditCard{
		ID:                   cc.ID,
		PaymentMethodID:      cc.PaymentMethodID,
		Alias:                cc.Alias,
		LastFour:             lastFour,
		DefaultClosingDay:    int(cc.DefaultClosingDay),
		DefaultDueDay:        int(cc.DefaultDueDay),
		DebitPaymentMethodID: cc.DebitPaymentMethodID,
		Network:              textToPtr(cc.Network),
		CreatedAt:            cc.CreatedAt.Time,
	}
}

// textToPtr: NULL → nil.
func textToPtr(t pgtype.Text) *string {
	if !t.Valid {
		return nil
	}
	v := t.String
	return &v
}

// textToPG converts an optional string (lastFour, network) to pgtype.Text.
// nil o puntero a "" → Valid=false (NULL en DB).
func textToPG(s *string) pgtype.Text {
	if s == nil || *s == "" {
		return pgtype.Text{Valid: false}
	}
	return pgtype.Text{String: *s, Valid: true}
}

// isUniqueViolation: mismo patrón que users/households. Detecta 23505
// (violación de UNIQUE) para traducir a ErrConflict.
func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code == "23505"
	}
	return false
}

// isCheckViolation: 23514. Se dispara si el service manda una combinación
// inválida de kind/allows_installments y el CHECK del schema la rechaza.
// Con nuestras validaciones de service no debería ocurrir, pero si pasa
// (ej: race con cambio de kind futuro) lo mapeamos a validación.
func isCheckViolation(err error) bool {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code == "23514"
	}
	return false
}
