CREATE TABLE IF NOT EXISTS image_jobs (
    id BIGSERIAL PRIMARY KEY,
    public_id VARCHAR(64) NOT NULL UNIQUE,
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    api_key_id BIGINT NOT NULL REFERENCES api_keys(id) ON DELETE RESTRICT,
    group_id BIGINT NOT NULL REFERENCES groups(id) ON DELETE RESTRICT,
    endpoint VARCHAR(64) NOT NULL,
    operation VARCHAR(32) NOT NULL,
    mode VARCHAR(32) NOT NULL,
    requested_model VARCHAR(128) NOT NULL,
    mapped_model VARCHAR(128) NOT NULL DEFAULT '',
    status VARCHAR(32) NOT NULL,
    requested_count INTEGER NOT NULL,
    completed_count INTEGER NOT NULL DEFAULT 0,
    request JSONB NOT NULL,
    request_digest VARCHAR(64) NOT NULL,
    idempotency_key_hash VARCHAR(64),
    reserved_usd DECIMAL(20,8) NOT NULL DEFAULT 0,
    reservation_billing_type INTEGER NOT NULL DEFAULT 1,
    reservation_subscription_id BIGINT,
    reservation_status VARCHAR(20) NOT NULL DEFAULT 'held',
    usage JSONB,
    settlement_status VARCHAR(20) NOT NULL DEFAULT 'pending',
    attempt_id VARCHAR(64),
    worker_id VARCHAR(128),
    execution_phase VARCHAR(20) NOT NULL DEFAULT 'preflight',
    heartbeat_at TIMESTAMPTZ,
    cancel_requested_at TIMESTAMPTZ,
    canceled_at TIMESTAMPTZ,
    error_type VARCHAR(64),
    error_code VARCHAR(64),
    error_message TEXT,
    error_retryable BOOLEAN NOT NULL DEFAULT FALSE,
    started_at TIMESTAMPTZ,
    finished_at TIMESTAMPTZ,
    expires_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT image_jobs_status_check
        CHECK (status IN ('queued','running','completed','partial','failed','indeterminate','canceled','expired')),
    CONSTRAINT image_jobs_execution_phase_check
        CHECK (execution_phase IN ('preflight','upstream')),
    CONSTRAINT image_jobs_reservation_status_check
        CHECK (reservation_status IN ('held','released','settled')),
    CONSTRAINT image_jobs_settlement_status_check
        CHECK (settlement_status IN ('pending','settling','settled','released'))
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_image_jobs_api_key_idempotency
    ON image_jobs(api_key_id, idempotency_key_hash)
    WHERE idempotency_key_hash IS NOT NULL;

CREATE INDEX IF NOT EXISTS idx_image_jobs_claim
    ON image_jobs(status, created_at, id)
    WHERE status IN ('queued','running');

CREATE TABLE IF NOT EXISTS image_job_inputs (
    id BIGSERIAL PRIMARY KEY,
    job_id BIGINT NOT NULL REFERENCES image_jobs(id) ON DELETE CASCADE,
    index INTEGER NOT NULL,
    kind VARCHAR(32) NOT NULL,
    object_key TEXT NOT NULL,
    mime_type VARCHAR(128) NOT NULL,
    byte_size BIGINT NOT NULL,
    sha256 VARCHAR(64) NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_image_job_inputs_job_kind_index
    ON image_job_inputs(job_id, kind, index);

CREATE TABLE IF NOT EXISTS image_job_results (
    id BIGSERIAL PRIMARY KEY,
    job_id BIGINT NOT NULL REFERENCES image_jobs(id) ON DELETE CASCADE,
    index INTEGER NOT NULL,
    status VARCHAR(32) NOT NULL,
    object_key TEXT,
    mime_type VARCHAR(128),
    byte_size BIGINT,
    width INTEGER,
    height INTEGER,
    size_tier VARCHAR(16),
    revised_prompt TEXT,
    upstream_output_id VARCHAR(128),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_image_job_results_job_index
    ON image_job_results(job_id, index);
