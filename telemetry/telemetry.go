package telemetry

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
)

// An optional independent exporter consumes the durable event log. It never gates run progress.
func (s *Store) ExportTelemetry(ctx context.Context, baseURL string) error {
	if e := validateEndpoint(baseURL); e != nil {
		return e
	}
	var receive sync.WaitGroup
	if s.Remote != nil {
		receive.Add(1)
		go func() { defer receive.Done(); telemetryLoop(ctx, "receive", s.receiveTelemetry) }()
	}
	telemetryLoop(ctx, "export", func(ctx context.Context) error { return s.exportOne(ctx, baseURL) })
	receive.Wait()
	return ctx.Err()
}

func telemetryLoop(ctx context.Context, operation string, work func(context.Context) error) {
	delay, backoff := time.Duration(0), time.Second
	for ctx.Err() == nil {
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
		bounded, cancel := context.WithTimeout(ctx, 8*time.Second)
		e := work(bounded)
		cancel()
		if errors.Is(e, pgx.ErrNoRows) {
			delay, backoff = time.Second, time.Second
		} else if e != nil {
			delay = backoff
			backoff = min(32*time.Second, backoff*2)
			if ctx.Err() == nil {
				slog.Warn("telemetry work deferred", "operation", operation)
			}
		} else {
			delay, backoff = 0, time.Second
		}
	}
}

func (s *Store) exportOne(ctx context.Context, baseURL string) error {
	tx, e := s.DB.Begin(ctx)
	if e != nil {
		return e
	}
	defer tx.Rollback(ctx)
	table, pending := "telemetry_inbox", "e.exported_at IS NULL"
	rows, e := tx.Query(ctx, "SELECT e.seq,e.run_id,e.kind,e.created_at FROM "+table+" e WHERE "+pending+" ORDER BY e.seq FOR UPDATE OF e SKIP LOCKED LIMIT 32")
	if e != nil {
		return e
	}
	attribute := func(k, v string) map[string]any {
		return map[string]any{"key": k, "value": map[string]string{"stringValue": v}}
	}
	spans := []any{}
	sequences := []int64{}
	for rows.Next() {
		var seq int64
		var id, kind string
		var at time.Time
		if e = rows.Scan(&seq, &id, &kind, &at); e != nil {
			rows.Close()
			return e
		}
		sequences = append(sequences, seq)
		spans = append(spans, map[string]any{"traceId": id, "spanId": Hash([]byte(fmt.Sprint(id, seq)))[:16], "name": kind, "kind": 1, "startTimeUnixNano": strconv.FormatInt(at.UnixNano(), 10), "endTimeUnixNano": strconv.FormatInt(at.Add(time.Nanosecond).UnixNano(), 10), "attributes": []any{attribute("agentos.run_id", id), attribute("agentos.event_sequence", strconv.FormatInt(seq, 10)), attribute("langfuse.trace.name", "Agent OS run"), attribute("langfuse.observation.type", "event")}})
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return e
	}
	if len(spans) == 0 {
		return pgx.ErrNoRows
	}
	pub, e := s.Secret(ctx, "LANGFUSE_PUBLIC_KEY")
	if e != nil {
		return e
	}
	secret, e := s.Secret(ctx, "LANGFUSE_SECRET_KEY")
	if e != nil {
		return e
	}
	payload := map[string]any{"resourceSpans": []any{map[string]any{"resource": map[string]any{"attributes": []any{attribute("service.name", "agent-os")}}, "scopeSpans": []any{map[string]any{"scope": map[string]string{"name": "agent-os", "version": "0.6.0"}, "spans": spans}}}}}
	requestCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	req, e := http.NewRequestWithContext(requestCtx, "POST", strings.TrimRight(baseURL, "/")+"/api/public/otel/v1/traces", bytes.NewReader(JSON(payload)))
	if e != nil {
		return e
	}
	req.SetBasicAuth(pub, secret)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-langfuse-ingestion-version", "4")
	resp, e := externalHTTP.Do(req)
	if e != nil {
		return e
	}
	defer resp.Body.Close()
	const acknowledgementLimit = 4096
	body, e := io.ReadAll(io.LimitReader(resp.Body, acknowledgementLimit+1))
	if e != nil {
		return e
	}
	if resp.StatusCode != 200 {
		return fmt.Errorf("telemetry HTTP %d", resp.StatusCode)
	}
	if len(body) > acknowledgementLimit {
		return errors.New("telemetry acknowledgement exceeds size limit; event remains queued")
	}
	var ack struct {
		Partial struct {
			Rejected json.RawMessage `json:"rejectedSpans"`
		} `json:"partialSuccess"`
	}
	if len(body) > 0 {
		if e = json.Unmarshal(body, &ack); e != nil {
			return errors.New("invalid telemetry acknowledgement")
		}
	}
	if rejected := strings.Trim(string(ack.Partial.Rejected), "\""); rejected != "" && rejected != "0" {
		return errors.New("telemetry reported partial rejection; event remains queued")
	}
	if _, e = tx.Exec(ctx, "INSERT INTO telemetry_exports(seq) SELECT unnest($1::bigint[]) ON CONFLICT DO NOTHING", sequences); e != nil {
		return e
	}
	if s.Remote != nil {
		if _, e = tx.Exec(ctx, "UPDATE telemetry_inbox SET exported_at=now() WHERE seq=ANY($1::bigint[])", sequences); e != nil {
			return e
		}
	}
	return tx.Commit(ctx)
}
