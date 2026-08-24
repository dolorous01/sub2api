CREATE TABLE canvas_media_tasks (
    id BIGSERIAL PRIMARY KEY,
    public_id VARCHAR(64) NOT NULL UNIQUE,
    kind VARCHAR(16) NOT NULL CHECK (kind IN ('video', 'audio')),
    status VARCHAR(20) NOT NULL CHECK (
        status IN ('queued', 'running', 'partial', 'completed', 'failed', 'indeterminate', 'canceled', 'expired')
    ),
    phase VARCHAR(32) NOT NULL DEFAULT 'preflight',
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    api_key_id BIGINT NOT NULL REFERENCES api_keys(id) ON DELETE RESTRICT,
    project_id BIGINT NOT NULL REFERENCES image_canvas_projects(id) ON DELETE CASCADE,
    client_node_id VARCHAR(128) NOT NULL,
    selected_model VARCHAR(128) NOT NULL,
    successful_model VARCHAR(128),
    prompt TEXT NOT NULL,
    request JSONB NOT NULL,
    request_hash CHAR(64) NOT NULL,
    idempotency_key VARCHAR(255) NOT NULL,
    upstream_request_id VARCHAR(255),
    upstream_account_id BIGINT REFERENCES accounts(id) ON DELETE SET NULL,
    result_asset_id BIGINT REFERENCES image_assets(id) ON DELETE SET NULL,
    error JSONB,
    cancel_requested BOOLEAN NOT NULL DEFAULT FALSE,
    attempt_count INTEGER NOT NULL DEFAULT 0 CHECK (attempt_count >= 0),
    next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    lease_owner VARCHAR(64),
    lease_expires_at TIMESTAMPTZ,
    billing_type SMALLINT NOT NULL,
    billing_subscription_id BIGINT REFERENCES user_subscriptions(id) ON DELETE SET NULL,
    billing_recorded_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    completed_at TIMESTAMPTZ,
    UNIQUE (user_id, idempotency_key)
);

CREATE INDEX idx_canvas_media_tasks_claim
    ON canvas_media_tasks(kind, next_attempt_at, created_at)
    WHERE status IN ('queued', 'running') AND cancel_requested = FALSE;

CREATE INDEX idx_canvas_media_tasks_user_project
    ON canvas_media_tasks(user_id, project_id, created_at DESC);

CREATE INDEX idx_canvas_media_tasks_lease
    ON canvas_media_tasks(lease_expires_at)
    WHERE lease_expires_at IS NOT NULL AND status IN ('queued', 'running');
