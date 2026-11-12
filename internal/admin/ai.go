package admin

import (
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/LucianoR23/valuo_api/internal/ai"
	sqlcgen "github.com/LucianoR23/valuo_api/internal/db/sqlc"
	"github.com/LucianoR23/valuo_api/internal/httpx"
)

// AIStatus is what the panel shows about the AI layer's settings.
type AIStatus interface {
	Enabled() bool
	Limits() ai.Limits
}

// WithAI adds the AI spend section (plan 8.1, §8.6).
func (h *Handler) WithAI(s AIStatus) *Handler {
	h.ai = s
	return h
}

// Amounts are millionths of a dollar, as stored in ai_usage.
type aiTaskDTO struct {
	Task    string `json:"task"`
	Calls   int32  `json:"calls"`
	Errors  int32  `json:"errors"`
	Blocked int32  `json:"blocked"`
	Cost    int64  `json:"costMicroUsd"`
}

type aiUserDTO struct {
	UserID  uuid.UUID `json:"userId"`
	Email   string    `json:"email"`
	Calls   int32     `json:"calls"`
	Blocked int32     `json:"blocked"`
	Cost    int64     `json:"costMicroUsd"`
}

type aiDTO struct {
	Enabled bool `json:"enabled"`
	Limits  struct {
		DailyBudget int64 `json:"dailyBudgetMicroUsd"`
		UserDaily   int64 `json:"userDailyMicroUsd"`
		PerMinute   int   `json:"perMinute"`
		PerDay      int   `json:"perDay"`
	} `json:"limits"`
	Today struct {
		Cost         int64 `json:"costMicroUsd"`
		Calls        int32 `json:"calls"`
		Errors       int32 `json:"errors"`
		Blocked      int32 `json:"blocked"`
		AvgLatencyMs int32 `json:"avgLatencyMs"`
	} `json:"today"`
	Month struct {
		Cost  int64 `json:"costMicroUsd"`
		Calls int32 `json:"calls"`
	} `json:"month"`
	ByTask   []aiTaskDTO `json:"byTask"`
	TopUsers []aiUserDTO `json:"topUsers"`
}

// AI: today's and this month's AI spend, by task and by person.
func (h *Handler) AI(w http.ResponseWriter, r *http.Request) {
	now := h.now().In(h.loc)
	dayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, h.loc)
	monthStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, h.loc)
	t, err := h.q.AIAdminTotals(r.Context(), sqlcgen.AIAdminTotalsParams{DayStart: ts(dayStart), MonthStart: ts(monthStart)})
	if err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	tasks, err := h.q.AIAdminByTask(r.Context(), ts(monthStart))
	if err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	users, err := h.q.AIAdminTopUsers(r.Context(), ts(monthStart))
	if err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	var out aiDTO
	if h.ai != nil {
		l := h.ai.Limits()
		out.Enabled = h.ai.Enabled()
		out.Limits.DailyBudget, out.Limits.UserDaily = l.DailyMicroUSD, l.UserDailyMicroUSD
		out.Limits.PerMinute, out.Limits.PerDay = l.PerMinute, l.PerDay
	}
	out.Today.Cost, out.Today.Calls, out.Today.Errors = t.CostDay, t.CallsDay, t.ErrorsDay
	out.Today.Blocked, out.Today.AvgLatencyMs = t.BlockedDay, t.AvgLatencyDay
	out.Month.Cost, out.Month.Calls = t.CostMonth, t.CallsMonth
	out.ByTask = make([]aiTaskDTO, len(tasks))
	for i, x := range tasks {
		out.ByTask[i] = aiTaskDTO{Task: x.Task, Calls: x.Calls, Errors: x.Errors, Blocked: x.Blocked, Cost: x.Cost}
	}
	out.TopUsers = make([]aiUserDTO, 0, len(users))
	for _, u := range users {
		if u.UserID == nil {
			continue
		}
		out.TopUsers = append(out.TopUsers, aiUserDTO{UserID: *u.UserID, Email: u.Email, Calls: u.Calls, Blocked: u.Blocked, Cost: u.Cost})
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}
