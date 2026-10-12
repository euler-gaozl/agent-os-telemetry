# Agent OS Telemetry

Independent optional event exporter. Owns only telemetry_inbox and
telemetry_exports; consumes Runtime's claim/ack HTTP contract. Inbox commits
before acknowledgement; failed acknowledgement can be redelivered. Event
identity changes are rejected. It never reads Runtime's tables and never exports
prompts, tool arguments, results or credentials.

## Build and run

Go 1.27.2 / PostgreSQL 18. Run `scripts/Build.ps1` (PowerShell 7) or
`GOWORK=off go build -mod=vendor ./cmd/telemetry`. The committed vendor tree pins
the SDK and permits one-checkout builds with no private dependency token.
`source-provenance.json` records its origin/version. Start with
`bin/telemetry --config <service.json>`.

`deploy/service.example.json` is a placeholder contract. Supply a scoped DB URL,
unique service token and peer hashes (including self), RUNTIME_URL, REGISTRY_URL,
and LANGFUSE_URL. Registry supplies only the named Langfuse provider credentials.
The executable defaults to a dynamic loopback port to avoid collisions/multiple
instance conflicts; LISTEN can explicitly bind a fixed address. Docker uses 8099
inside its private network. `/readyz` validates this service's schema, not the
remote exporter. Unconfigured Langfuse is not an available export capability.

Export retries have bounded batches/timeouts/backoff. A lost successful remote
reply may duplicate an observation: delivery is at least once, not exactly once.
Telemetry outage does not block the Agent execution path.

## Migration and verification

Provision the non-owner application role, schema USAGE and no CREATE. Provide
the DDL connection only as `MIGRATION_DATABASE_URL`; run
`bin/migrate --role <existing-role>`. The transaction changes only the two owned
tables and grants their DML rights. Startup checks schema, never applies DDL.
Retain inbox/export markers through service updates and binary rollbacks under
the compatible HTTP/data identifiers in `deploy/release-contract.json`.

Run `go vet -mod=vendor ./...` and `go test -mod=vendor -count=1 ./...`.
With `TEST_DATABASE_URL`, tests create isolated fixture databases/NOLOGIN roles
and verify transactions, least privilege, inbox-before-ack and redelivery after
a lost acknowledgement. Without it DB tests explicitly skip. Linux CI runs
PostgreSQL and race checks; Windows CI verifies unit/build portability. No real
Langfuse or model key is used here. Full Langfuse integration remains a separately
recorded integration test; P2's live independent release acceptance is pending.

Legacy local configurations may omit the Runtime address: this service preserves
the previous loopback default (127.0.0.1:8094) for its bounded heartbeat/client.
Where used, Registry (8096) and ToolHub (8091) defaults are also owned by the
service entrypoint. Explicit environment/config endpoints always take precedence.
Configure these addresses explicitly for containers or multiple hosts. No config
file or shared SDK role policy is rewritten by this compatibility behavior.
