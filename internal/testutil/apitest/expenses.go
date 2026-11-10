package apitest

import (
	"net/http"
	"testing"
)

// Expense is the expense object of the API.
type Expense struct {
	ID              string   `json:"id"`
	HouseholdID     string   `json:"householdId"`
	CreatedBy       string   `json:"createdBy"`
	CategoryID      *string  `json:"categoryId"`
	PaymentMethodID string   `json:"paymentMethodId"`
	Amount          float64  `json:"amount"`
	Currency        string   `json:"currency"`
	AmountBase      float64  `json:"amountBase"`
	BaseCurrency    string   `json:"baseCurrency"`
	RateUsed        *float64 `json:"rateUsed"`
	RateSource      *string  `json:"rateSource"`
	Description     string   `json:"description"`
	SpentAt         string   `json:"spentAt"`
	Installments    int      `json:"installments"`
	IsShared        bool     `json:"isShared"`
	Status          string   `json:"status"`
}

// Installment is one installment of an expense detail.
type Installment struct {
	ID                    string  `json:"id"`
	InstallmentNumber     int     `json:"installmentNumber"`
	InstallmentAmount     float64 `json:"installmentAmount"`
	InstallmentAmountBase float64 `json:"installmentAmountBase"`
	BillingDate           string  `json:"billingDate"`
	DueDate               *string `json:"dueDate"`
	IsPaid                bool    `json:"isPaid"`
	Shares                []struct {
		UserID         string  `json:"userId"`
		AmountBaseOwed float64 `json:"amountBaseOwed"`
	} `json:"shares"`
}

// ExpenseDetail is the response of creating or getting an expense.
type ExpenseDetail struct {
	Expense      Expense       `json:"expense"`
	Installments []Installment `json:"installments"`
}

// CreateExpense posts body to /expenses in household hID as u and expects 201.
// Missing fields get defaults: ARS, 1 installment, today's date is NOT
// assumed (spentAt is required), description "Gasto".
func (s *Server) CreateExpense(t testing.TB, u User, hID string, body map[string]any) ExpenseDetail {
	t.Helper()
	if _, ok := body["currency"]; !ok {
		body["currency"] = "ARS"
	}
	if _, ok := body["installments"]; !ok {
		body["installments"] = 1
	}
	if _, ok := body["description"]; !ok {
		body["description"] = "Gasto"
	}
	var d ExpenseDetail
	s.Do(t, Req{Method: http.MethodPost, Path: "/expenses", Token: u.Token, Household: hID, Body: body}).
		Status(t, http.StatusCreated).JSON(t, &d)
	return d
}
