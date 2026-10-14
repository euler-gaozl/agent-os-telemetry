# 历史实现与验收记录

以下为 2026-10-14 整理中文首页前的原始英文记录，保留具体参数、证据编号及历史状态。相对路径以仓库根目录为基准；当前状态以根目录 README 和集成计划为准。

---

# Agent OS Telemetry

Independent optional event exporter. Owns only telemetry_inbox and
telemetry_exports; consumes Runtime's claim/ack HTTP contract. Inbox commits
before acknowledgement; failed acknowledgement can be redelivered. Event
identity changes are rejected. It never reads Runtime's tables and never exports
prompts, tool arguments, results or credentials.

## Build and run

`telemetry --execution-access-profile` reports the compiled `telemetry-http-v1`
profile and exits before reading deployment configuration or connecting to any
service. The release contract carries the same claim. The Windows coordinator
verifies the actual binary and bound database identity, including inbox
SELECT/INSERT/UPDATE and exports SELECT/INSERT, before start/update/rollback and
pending recovery. The existing readiness SELECT probe alone does not establish
those write permissions. A missing capability is conservatively treated as
requiring legacy execution events privileges; malformed metadata never becomes
a legacy fallback. This does not enable Langfuse or apply any database migration.

Two entrypoint tests, vet, native build and the actual report passed in local
evidence `execution-access-build-1e387f7fc44c40e094e2ce0491f0ada7`;
44 restricted-login/release checks are recorded in
`execution-access-release-fdce72a63e154c74a34c70f8caf37a2a`.
Formal installation remains separate. The formal Windows account currently has
no `telemetry_inbox`; its Owner migration must run before activating this service.
No paid provider or GitHub Actions ran.

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
PostgreSQL and race checks; Windows CI verifies unit/build portability. Standard
verification uses no real Langfuse or model key; the separate live workflow is
described below. P2's live independent release acceptance is recorded separately.

The new `langfuse.yml` workflow carries the former integration repository's live
Langfuse 4.56.0 ingestion/readback/retransmission scenario into this Owner. It
uses this repository's exporter, a real isolated PostgreSQL inbox and real
self-hosted Langfuse; Runtime claim/ack and Registry credential HTTP endpoints
are explicit protocol fixtures. The independent-image integration separately
tests actual Runtime-to-Telemetry delivery. The live test verifies durable inbox
before acknowledgement, original event identities at Langfuse's read API,
exclusion of a private payload marker, and two observations per identity after
forced retransmission. This is at-least-once delivery, not upstream exactly-once.
The first Owner live workflow result is pending; the old integration coverage
has not been deleted or counted as a new Owner pass.

Legacy local configurations may omit the Runtime address: this service preserves
the previous loopback default (127.0.0.1:8094) for its bounded heartbeat/client.
Where used, Registry (8096) and ToolHub (8091) defaults are also owned by the
service entrypoint. Explicit environment/config endpoints always take precedence.
Configure these addresses explicitly for containers or multiple hosts. No config
file or shared SDK role policy is rewritten by this compatibility behavior.
