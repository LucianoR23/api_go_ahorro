package apitest

import (
	"net/http"
	"testing"
)

// PaymentMethod is a payment method as the API returns it.
type PaymentMethod struct {
	ID                 string  `json:"id"`
	OwnerUserID        string  `json:"ownerUserId"`
	BankID             *string `json:"bankId"`
	Name               string  `json:"name"`
	Kind               string  `json:"kind"`
	AllowsInstallments bool    `json:"allowsInstallments"`
	IsActive           bool    `json:"isActive"`
	CreditCard         *struct {
		ID                   string  `json:"id"`
		PaymentMethodID      string  `json:"paymentMethodId"`
		Alias                string  `json:"alias"`
		LastFour             *string `json:"lastFour"`
		DefaultClosingDay    int     `json:"defaultClosingDay"`
		DefaultDueDay        int     `json:"defaultDueDay"`
		DebitPaymentMethodID *string `json:"debitPaymentMethodId"`
	} `json:"creditCard"`
	Periods []struct {
		PeriodYM    string `json:"periodYm"`
		ClosingDate string `json:"closingDate"`
		DueDate     string `json:"dueDate"`
	} `json:"periods"`
}

// CreatePaymentMethod posts body to /payment-methods as u and expects 201.
func (s *Server) CreatePaymentMethod(t testing.TB, u User, body map[string]any) PaymentMethod {
	t.Helper()
	var pm PaymentMethod
	s.Do(t, Req{Method: http.MethodPost, Path: "/payment-methods", Token: u.Token, Body: body}).
		Status(t, http.StatusCreated).JSON(t, &pm)
	return pm
}

// CreateCreditCard creates a credit card payment method named name, closing
// on closingDay and due on dueDay, with the given current period
// (YYYY-MM-DD dates).
func (s *Server) CreateCreditCard(t testing.TB, u User, name string, closingDay, dueDay int, closing, due string) PaymentMethod {
	t.Helper()
	return s.CreatePaymentMethod(t, u, map[string]any{
		"name": name, "kind": "credit",
		"creditCard": map[string]any{
			"alias": name, "defaultClosingDay": closingDay, "defaultDueDay": dueDay,
			"currentPeriod": map[string]string{"closingDate": closing, "dueDate": due},
		},
	})
}

// Cash returns u's bootstrap "Efectivo" payment method.
func (s *Server) Cash(t testing.TB, u User) PaymentMethod {
	t.Helper()
	var list []PaymentMethod
	s.Do(t, Req{Method: http.MethodGet, Path: "/payment-methods", Token: u.Token}).Status(t, http.StatusOK).JSON(t, &list)
	for _, pm := range list {
		if pm.Kind == "cash" {
			return pm
		}
	}
	t.Fatalf("apitest: %s has no cash payment method", u.Email)
	return PaymentMethod{}
}
