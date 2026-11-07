package audit

import (
	"context"
	"errors"
	"log/slog"

	"github.com/jackc/pgx/v5/pgxpool"

	sqlcgen "github.com/LucianoR23/valuo_api/internal/db/sqlc"
)

// ApplyRetention applies the audit log retention once (plan 0.17, privacy
// policy): IPs older than 7 days are cleared and events older than 12 months
// deleted. The jobs package runs it daily.
func ApplyRetention(ctx context.Context, pool *pgxpool.Pool, logger *slog.Logger) error {
	q := sqlcgen.New(pool)
	ips, errIPs := q.ClearOldAuditIPs(ctx)
	gone, errEvents := q.DeleteOldAuditEvents(ctx)
	if err := errors.Join(errIPs, errEvents); err != nil {
		return err
	}
	logger.InfoContext(ctx, "audit retention: ok", "ips_cleared", ips, "events_deleted", gone)
	return nil
}
