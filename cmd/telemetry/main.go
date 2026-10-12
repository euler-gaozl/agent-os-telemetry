package main

import (
	"context"
	"github.com/euler-gaozl/agent-os-sdk/servicehost"
	"github.com/euler-gaozl/agent-os-sdk/servicehttp"
	"github.com/euler-gaozl/agent-os-telemetry/telemetry"
	"github.com/jackc/pgx/v5/pgxpool"
	"net/http"
)

func main() {
	servicehost.Main(servicehost.Options{Role: "telemetry", Port: "0", Database: true, Concurrency: 16, Authorize: telemetry.Authorize, Readiness: telemetry.Readiness}, func(ctx context.Context, p *pgxpool.Pool, c servicehttp.Config) (http.Handler, func(context.Context) error, error) {
		client := servicehost.Client()
		store := &telemetry.Store{DB: p, Remote: &client}
		return servicehttp.New(c, p.Ping, func(*http.ServeMux) {}), func(ctx context.Context) error {
			return store.ExportTelemetry(ctx, servicehost.Env("LANGFUSE_URL", ""))
		}, nil
	})
}
