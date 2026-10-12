package telemetry

import (
	"context"
	"errors"

	"time"

	"github.com/jackc/pgx/v5"
)

// Only event identity/type/time cross the boundary. Prompts, model messages,
// tool arguments, results and credentials are never exported here.
type telemetryDelivery struct {
	Sequence  int64     `json:"sequence"`
	RunID     string    `json:"run_id"`
	Kind      string    `json:"kind"`
	CreatedAt time.Time `json:"created_at"`
	Fence     int64     `json:"fence"`
}

type telemetryReceipt struct {
	Sequence int64 `json:"sequence"`
	Fence    int64 `json:"fence"`
}

func (s *Store) saveTelemetryInbox(ctx context.Context, items []telemetryDelivery) error {
	if len(items) > 32 {
		return errors.New("telemetry batch exceeds limit")
	}
	tx, e := s.DB.Begin(ctx)
	if e != nil {
		return e
	}
	defer tx.Rollback(ctx)
	for _, d := range items {
		if d.Sequence < 1 || d.Fence < 1 || len(d.RunID) != 32 || d.Kind == "" || len(d.Kind) > 200 || d.CreatedAt.IsZero() {
			return errors.New("invalid telemetry delivery")
		}
		tag, e := tx.Exec(ctx, `INSERT INTO telemetry_inbox(seq,run_id,kind,created_at,exported_at) VALUES($1,$2,$3,$4,(SELECT exported_at FROM telemetry_exports WHERE seq=$1))
 ON CONFLICT(seq) DO UPDATE SET seq=excluded.seq WHERE telemetry_inbox.run_id=excluded.run_id
 AND telemetry_inbox.kind=excluded.kind AND telemetry_inbox.created_at=excluded.created_at`, d.Sequence, d.RunID, d.Kind, d.CreatedAt)
		if e != nil {
			return e
		}
		if tag.RowsAffected() != 1 {
			return errors.New("telemetry delivery identity changed")
		}
	}
	return tx.Commit(ctx)
}

func (s *Store) receiveTelemetry(ctx context.Context) error {
	if s.Remote == nil || s.Remote.RuntimeURL == "" {
		return errors.New("runtime event delivery URL required")
	}
	owner := ID()
	var items []telemetryDelivery
	if e := s.Remote.Request(ctx, "POST", s.Remote.RuntimeURL+"/v1/telemetry/claim", map[string]string{"owner": owner}, &items); e != nil {
		return e
	}
	if len(items) == 0 {
		return pgx.ErrNoRows
	}

	if e := s.saveTelemetryInbox(ctx, items); e != nil {
		return e
	}
	receipts := make([]telemetryReceipt, 0, len(items))
	for _, d := range items {
		receipts = append(receipts, telemetryReceipt{Sequence: d.Sequence, Fence: d.Fence})
	}
	return s.Remote.Request(ctx, "POST", s.Remote.RuntimeURL+"/v1/telemetry/ack", map[string]any{"owner": owner, "receipts": receipts}, nil)
}
