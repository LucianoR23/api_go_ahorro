package ai

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	sqlcgen "github.com/LucianoR23/valuo_api/internal/db/sqlc"
)

// Mailer sends the budget alert.
type Mailer interface {
	Configured() bool
	Send(ctx context.Context, to []string, subject, html string) error
}

// EmailAlert returns a Config.Alert that emails every superadmin when the
// day's AI spend crosses a budget step, and logs it (errors reach
// GlitchTip at 100%). The mail goes out in the background so the user's
// request does not wait for it.
func EmailAlert(pool *pgxpool.Pool, mail Mailer, brand string, logger *slog.Logger) func(ctx context.Context, percent int, spent, budget int64) {
	q := sqlcgen.New(pool)
	return func(ctx context.Context, percent int, spent, budget int64) {
		attrs := []any{"percent", percent, "spent_usd", usd(spent), "budget_usd", usd(budget)}
		if percent >= 100 {
			logger.ErrorContext(ctx, "ai: daily budget reached, AI features paused until tomorrow", attrs...)
		} else {
			logger.WarnContext(ctx, "ai: daily budget step crossed", attrs...)
		}
		if mail == nil || !mail.Configured() {
			return
		}
		go func() {
			ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
			defer cancel()
			to, err := q.ListSuperadminEmails(ctx)
			if err != nil || len(to) == 0 {
				if err != nil {
					logger.ErrorContext(ctx, "ai: budget alert recipients", "err", err)
				}
				return
			}
			subject := fmt.Sprintf("%s: IA al %d%% del presupuesto diario", brand, percent)
			body := fmt.Sprintf("<p>Hoy la IA lleva gastados US$ %s de US$ %s (%d%%).</p>", usd(spent), usd(budget), percent)
			if percent >= 100 {
				body += "<p>Las funciones con IA quedan pausadas hasta mañana: la gente puede seguir cargando a mano. " +
					"Para subir el tope, cambiá AI_DAILY_BUDGET_USD.</p>"
			}
			if err := mail.Send(ctx, to, subject, body); err != nil {
				logger.ErrorContext(ctx, "ai: budget alert email", "err", err)
			}
		}()
	}
}

func usd(micro int64) string { return fmt.Sprintf("%.2f", float64(micro)/1e6) }
