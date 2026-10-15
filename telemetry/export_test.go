package telemetry

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/euler-gaozl/agent-os-sdk/serviceclient"
	"github.com/euler-gaozl/agent-os-sdk/servicehttp"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Real isolated PostgreSQL and the Owner migration; Registry, Runtime and OTLP
// are explicit HTTP fixtures. SET ROLE is not a separate database login.
func telemetryExportFixture(t *testing.T, collect http.HandlerFunc) (*Store, context.Context, string) {
	t.Helper()
	db, role := fixtureDatabase(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	t.Cleanup(cancel)
	if err := Migrate(ctx, db, role); err != nil {
		t.Fatal(err)
	}
	config := db.Config().Copy()
	config.AfterConnect = func(ctx context.Context, conn *pgx.Conn) error {
		_, err := conn.Exec(ctx, "SET ROLE "+pgx.Identifier{role}.Sanitize())
		return err
	}
	app, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal("configure application fixture pool", err)
	}
	t.Cleanup(app.Close)
	var identity string
	if err = app.QueryRow(ctx, "SELECT current_user").Scan(&identity); err != nil || identity != role {
		t.Fatal("application fixture role mismatch", err)
	}
	if err = Readiness(ctx, app); err != nil {
		t.Fatal("application fixture readiness", err)
	}
	const token = "telemetry-export-protocol-fixture-only"
	registry := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.Path != "/v1/credentials/resolve" || r.Header.Get("Authorization") != "Bearer "+token {
			http.Error(w, "fixture request rejected", http.StatusForbidden)
			return
		}
		var body struct {
			Name string `json:"name"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "fixture input rejected", http.StatusBadRequest)
			return
		}
		value := ""
		switch body.Name {
		case "LANGFUSE_PUBLIC_KEY":
			value = "fixture-public"
		case "LANGFUSE_SECRET_KEY":
			value = "fixture-secret"
		default:
			http.Error(w, "fixture name rejected", http.StatusNotFound)
			return
		}
		servicehttp.JSONResponse(w, http.StatusOK, map[string]string{"value": value})
	}))
	t.Cleanup(registry.Close)
	collector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		public, secret, ok := r.BasicAuth()
		if r.Method != "POST" || r.URL.Path != "/api/public/otel/v1/traces" || !ok || public != "fixture-public" || secret != "fixture-secret" {
			t.Error("export did not use the expected OTLP fixture protocol")
			http.Error(w, "fixture request rejected", http.StatusForbidden)
			return
		}
		collect(w, r)
	}))
	t.Cleanup(collector.Close)
	store := &Store{DB: app, Remote: &serviceclient.Client{RegistryURL: registry.URL, Token: token}}
	event := telemetryDelivery{Sequence: 1, RunID: ID(), Kind: "owner_export_fixture", CreatedAt: time.Now().UTC().Truncate(time.Microsecond), Fence: 1}
	if err = store.saveTelemetryInbox(ctx, []telemetryDelivery{event}); err != nil {
		t.Fatal("persist fixture inbox", err)
	}
	return store, ctx, collector.URL
}

func assertTelemetryExportState(t *testing.T, ctx context.Context, store *Store, pending, exported, receipts int) {
	t.Helper()
	var gotPending, gotExported, gotReceipts int
	err := store.DB.QueryRow(ctx, `SELECT
 (SELECT count(*) FROM telemetry_inbox WHERE exported_at IS NULL),
 (SELECT count(*) FROM telemetry_inbox WHERE exported_at IS NOT NULL),
 (SELECT count(*) FROM telemetry_exports)`).Scan(&gotPending, &gotExported, &gotReceipts)
	if err != nil || gotPending != pending || gotExported != exported || gotReceipts != receipts {
		t.Fatalf("unexpected durable export state pending=%d exported=%d receipts=%d: %v", gotPending, gotExported, gotReceipts, err)
	}
}

func TestConcurrentExportersDoNotRepeatCommittedSpan(t *testing.T) {
	var requests atomic.Int32
	entered, release := make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	defer unblock()
	store, ctx, endpoint := telemetryExportFixture(t, func(w http.ResponseWriter, r *http.Request) {
		if requests.Add(1) == 1 {
			close(entered)
		}
		select {
		case <-release:
			_, _ = w.Write([]byte(`{}`))
		case <-r.Context().Done():
		}
	})
	first := make(chan error, 1)
	go func() { first <- store.exportOne(ctx, endpoint) }()
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal("first exporter did not reach collector")
	}
	// The first transaction holds its row lock while its HTTP reply is pending.
	// A second exporter must skip the locked row instead of sending it again.
	if err := store.exportOne(ctx, endpoint); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatal("concurrent exporter did not skip claimed inbox row", err)
	}
	assertTelemetryExportState(t, ctx, store, 1, 0, 0)
	unblock()
	select {
	case err := <-first:
		if err != nil {
			t.Fatal("first exporter did not commit acknowledgement", err)
		}
	case <-ctx.Done():
		t.Fatal("first exporter did not finish")
	}
	assertTelemetryExportState(t, ctx, store, 0, 1, 1)
	if err := store.exportOne(ctx, endpoint); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatal("committed inbox row was exported again", err)
	}
	if requests.Load() != 1 {
		t.Fatal("collector received duplicate requests", requests.Load())
	}
}

func TestRuntimeReceiveFailureDoesNotBlockDurableInboxExport(t *testing.T) {
	var exports, receives atomic.Int32
	store, ctx, endpoint := telemetryExportFixture(t, func(w http.ResponseWriter, r *http.Request) {
		exports.Add(1)
		_, _ = w.Write([]byte(`{}`))
	})
	runtime := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.Path != "/v1/telemetry/claim" || r.Header.Get("Authorization") != "Bearer "+store.Remote.Token {
			t.Error("unexpected Runtime fixture request")
		}
		receives.Add(1)
		http.Error(w, "controlled Runtime receive failure", http.StatusServiceUnavailable)
	}))
	t.Cleanup(runtime.Close)
	store.Remote.RuntimeURL = runtime.URL
	if err := store.receiveTelemetry(ctx); err == nil {
		t.Fatal("Runtime receive failure reported success")
	}
	assertTelemetryExportState(t, ctx, store, 1, 0, 0)
	// Registry is a separate available fixture; no Runtime call is needed here.
	if err := store.exportOne(ctx, endpoint); err != nil {
		t.Fatal("Runtime receive failure blocked durable inbox export", err)
	}
	assertTelemetryExportState(t, ctx, store, 0, 1, 1)
	if receives.Load() != 1 || exports.Load() != 1 {
		t.Fatal("unexpected protocol call counts", receives.Load(), exports.Load())
	}
}

func TestPartialTelemetryAcknowledgementRemainsPending(t *testing.T) {
	var reject atomic.Bool
	reject.Store(true)
	var requests atomic.Int32
	store, ctx, endpoint := telemetryExportFixture(t, func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if reject.Load() {
			_, _ = w.Write([]byte(`{"partialSuccess":{"rejectedSpans":"1"}}`))
		} else {
			_, _ = w.Write([]byte(`{"partialSuccess":{"rejectedSpans":"0"}}`))
		}
	})
	if err := store.exportOne(ctx, endpoint); err == nil {
		t.Fatal("partially rejected span was acknowledged")
	}
	assertTelemetryExportState(t, ctx, store, 1, 0, 0)
	reject.Store(false)
	if err := store.exportOne(ctx, endpoint); err != nil {
		t.Fatal("subsequent successful acknowledgement failed", err)
	}
	assertTelemetryExportState(t, ctx, store, 0, 1, 1)
	if err := store.exportOne(ctx, endpoint); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatal("acknowledged event was exported again", err)
	}
	if requests.Load() != 2 {
		t.Fatal("unexpected OTLP attempts", requests.Load())
	}
}

func TestTelemetryRejectsOversizedAcknowledgement(t *testing.T) {
	var oversized atomic.Bool
	oversized.Store(true)
	store, ctx, endpoint := telemetryExportFixture(t, func(w http.ResponseWriter, r *http.Request) {
		if oversized.Load() {
			// Exactly 4096 bytes form valid JSON plus whitespace; the extra byte
			// makes the complete HTTP 200 response invalid. Truncation must not
			// turn an unverified acknowledgement into a durable success marker.
			_, _ = w.Write([]byte(`{}` + strings.Repeat(" ", 4094) + "x"))
		} else {
			_, _ = w.Write([]byte(`{}`))
		}
	})
	if err := store.exportOne(ctx, endpoint); err == nil {
		t.Fatal("oversized acknowledgement accepted after truncating its body")
	}
	assertTelemetryExportState(t, ctx, store, 1, 0, 0)
	oversized.Store(false)
	if err := store.exportOne(ctx, endpoint); err != nil {
		t.Fatal("valid acknowledgement after rejected oversized response", err)
	}
	assertTelemetryExportState(t, ctx, store, 0, 1, 1)
}

func TestTelemetryAcceptsExactAcknowledgementLimit(t *testing.T) {
	var requests atomic.Int32
	store, ctx, endpoint := telemetryExportFixture(t, func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		_, _ = w.Write([]byte(`{}` + strings.Repeat(" ", 4094)))
	})
	if err := store.exportOne(ctx, endpoint); err != nil {
		t.Fatal("valid acknowledgement at the exact body limit was rejected", err)
	}
	assertTelemetryExportState(t, ctx, store, 0, 1, 1)
	if requests.Load() != 1 {
		t.Fatal("unexpected export requests", requests.Load())
	}
}
