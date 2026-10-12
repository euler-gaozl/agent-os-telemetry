CREATE TABLE IF NOT EXISTS telemetry_exports(seq bigint PRIMARY KEY, exported_at timestamptz NOT NULL DEFAULT now());
ALTER TABLE telemetry_exports DROP CONSTRAINT IF EXISTS telemetry_exports_seq_fkey;
CREATE TABLE IF NOT EXISTS telemetry_inbox(
 seq bigint PRIMARY KEY, run_id text NOT NULL, kind text NOT NULL, created_at timestamptz NOT NULL,
 received_at timestamptz NOT NULL DEFAULT now(),exported_at timestamptz);
CREATE INDEX IF NOT EXISTS telemetry_inbox_pending ON telemetry_inbox(seq) WHERE exported_at IS NULL;
