package telemetry

import (
	"context"
	"errors"
	"github.com/jackc/pgx/v5/pgxpool"
)

const readinessQuery = "SELECT e.seq,e.run_id,e.kind,e.created_at,e.exported_at,x.seq FROM telemetry_inbox e,telemetry_exports x WHERE false"

// Readiness checks only this service's schema and permissions without reading user data.
func Readiness(ctx context.Context, db *pgxpool.Pool) error {
	if db == nil {
		return errors.New("database required")
	}
	_, err := db.Exec(ctx, readinessQuery)
	return err
}
