package expenses

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"github.com/LucianoR23/valuo_api/internal/domain"
	"github.com/LucianoR23/valuo_api/internal/textnorm"
)

// Move takes an expense to another space of its author (plan 9.1): from Mi
// plata to a hogar, where it splits by the hogar's rules, or back, where it
// stops splitting. Only the author moves it; drafts are confirmed first.
// Installments keep their dates and paid state; the category becomes the
// one with the same name in the target space, if any.
func (s *Service) Move(ctx context.Context, householdID, id, actor, target uuid.UUID) (domain.ExpenseDetail, error) {
	prev, err := s.repo.GetDetail(ctx, id)
	if err != nil {
		return domain.ExpenseDetail{}, err
	}
	e := prev.Expense
	if e.HouseholdID != householdID {
		return domain.ExpenseDetail{}, domain.ErrNotFound
	}
	if e.CreatedBy != actor {
		return domain.ExpenseDetail{}, domain.ErrForbidden
	}
	if target == householdID {
		return domain.ExpenseDetail{}, domain.NewValidationError("householdId", "el gasto ya está en ese espacio")
	}
	if e.Status == "draft" {
		return domain.ExpenseDetail{}, domain.NewValidationError("status", "confirmá el monto antes de moverlo")
	}
	ok, err := s.households.IsMember(ctx, target, actor)
	if err != nil {
		return domain.ExpenseDetail{}, err
	}
	if !ok {
		return domain.ExpenseDetail{}, domain.ErrNotFound
	}
	hh, err := s.households.GetByID(ctx, target)
	if err != nil {
		return domain.ExpenseDetail{}, err
	}

	categoryID, err := s.matchCategoryIn(ctx, e.CategoryID, householdID, target)
	if err != nil {
		return domain.ExpenseDetail{}, err
	}

	isShared := hh.Kind == domain.HouseholdShared
	shares := map[uuid.UUID][]domain.InstallmentShare{}
	if isShared {
		weights, err := s.resolveShareWeights(ctx, CreateInput{HouseholdID: target, IsShared: true})
		if err != nil {
			return domain.ExpenseDetail{}, err
		}
		for _, iw := range prev.Installments {
			shares[iw.Installment.ID] = buildShares(iw.Installment.InstallmentAmountBase, weights)
		}
	}

	moved, err := s.repo.MoveTx(ctx, MoveBundle{
		ExpenseID:         id,
		TargetHouseholdID: target,
		CategoryID:        categoryID,
		IsShared:          isShared,
		Shares:            shares,
	})
	if err != nil {
		return domain.ExpenseDetail{}, err
	}

	// The members of the old split hear it left; the new ones get the usual
	// "new shared expense".
	s.notifyExpenseMutation(ctx, prev, actor, expenseMutationMoveOut)
	s.notifySharedExpense(ctx, moved, actor)
	return moved, nil
}

// matchCategoryIn finds the expense's category in the target space by name.
func (s *Service) matchCategoryIn(ctx context.Context, categoryID *uuid.UUID, from, to uuid.UUID) (*uuid.UUID, error) {
	if categoryID == nil {
		return nil, nil
	}
	src, err := s.repo.ListCategories(ctx, from)
	if err != nil {
		return nil, err
	}
	dst, err := s.repo.ListCategories(ctx, to)
	if err != nil {
		return nil, err
	}
	return matchCategory(src, *categoryID, dst), nil
}

// matchCategory: the target category with the same name and the same parent
// name ("Servicios > Luz"); without that subcategory, the parent; else none.
// Names compare with textnorm.Key, so "Súper" matches "super".
func matchCategory(src []Category, id uuid.UUID, dst []Category) *uuid.UUID {
	byID := func(cs []Category, id uuid.UUID) (Category, bool) {
		for _, c := range cs {
			if c.ID == id {
				return c, true
			}
		}
		return Category{}, false
	}
	path := func(cs []Category, c Category) string {
		key := textnorm.Key(c.Name)
		if c.ParentID != nil {
			if p, ok := byID(cs, *c.ParentID); ok {
				return textnorm.Key(p.Name) + ">" + key
			}
		}
		return key
	}

	cat, ok := byID(src, id)
	if !ok {
		return nil
	}
	want := path(src, cat)
	var parentWant string
	if i := strings.Index(want, ">"); i >= 0 {
		parentWant = want[:i]
	}
	var parentMatch *uuid.UUID
	for _, c := range dst {
		p := path(dst, c)
		if p == want {
			id := c.ID
			return &id
		}
		if parentWant != "" && c.ParentID == nil && p == parentWant {
			id := c.ID
			parentMatch = &id
		}
	}
	return parentMatch
}

// moveOutMessage: what the members of the old split read.
func moveOutMessage(actorName, description string) string {
	return fmt.Sprintf("%s sacó \"%s\" del hogar: ya no se divide.", actorName, description)
}
