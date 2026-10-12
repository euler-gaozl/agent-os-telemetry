package telemetry

import (
	"context"
	"github.com/euler-gaozl/agent-os-sdk/serviceclient"
	"github.com/euler-gaozl/agent-os-sdk/servicehttp"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestInboxPersistsBeforeAckAndRejectsIdentityChange(t *testing.T) {
	db, role := fixtureDatabase(t)
	ctx := context.Background()
	if err := Migrate(ctx, db, role); err != nil {
		t.Fatal(err)
	}
	event := telemetryDelivery{Sequence: 1, RunID: strings.Repeat("a", 32), Kind: "fixture", CreatedAt: time.Now().UTC().Truncate(time.Microsecond), Fence: 1}
	acks := 0
	runtime := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/telemetry/claim":
			servicehttp.JSONResponse(w, 200, []telemetryDelivery{event})
		case "/v1/telemetry/ack":
			var count int
			if err := db.QueryRow(ctx, "SELECT count(*) FROM telemetry_inbox WHERE seq=1").Scan(&count); err != nil || count != 1 {
				t.Error("ack before durable inbox", err)
			}
			acks++
			if acks == 1 {
				http.Error(w, "fixture lost acknowledgement", 503)
				return
			}
			servicehttp.JSONResponse(w, 200, map[string]bool{"ok": true})
		default:
			http.NotFound(w, r)
		}
	}))
	defer runtime.Close()
	store := &Store{DB: db, Remote: &serviceclient.Client{RuntimeURL: runtime.URL, Token: strings.Repeat("fixture-", 5)}}
	if err := store.receiveTelemetry(ctx); err == nil {
		t.Fatal("failed ack not reported")
	}
	event.Fence = 2
	if err := store.receiveTelemetry(ctx); err != nil {
		t.Fatal("redelivery failed", err)
	}
	event.Kind = "changed"
	if err := store.receiveTelemetry(ctx); err == nil {
		t.Fatal("different identity accepted")
	}
	if acks != 2 {
		t.Fatal("invalid delivery acknowledged", acks)
	}
	var count int
	if err := db.QueryRow(ctx, "SELECT count(*) FROM telemetry_inbox").Scan(&count); err != nil || count != 1 {
		t.Fatal("redelivery duplicated or lost data", err)
	}
}
