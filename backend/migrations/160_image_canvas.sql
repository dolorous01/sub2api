CREATE TABLE image_model_policies (
    id SMALLINT PRIMARY KEY CHECK (id = 1),
    version BIGINT NOT NULL DEFAULT 0,
    enabled BOOLEAN NOT NULL DEFAULT FALSE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

INSERT INTO image_model_policies (id) VALUES (1)
    ON CONFLICT (id) DO NOTHING;

CREATE TABLE image_model_policy_items (
    id BIGSERIAL PRIMARY KEY,
    policy_id SMALLINT NOT NULL REFERENCES image_model_policies(id) ON DELETE CASCADE,
    model VARCHAR(128) NOT NULL,
    enabled BOOLEAN NOT NULL DEFAULT TRUE,
    position INTEGER NOT NULL CHECK (position >= 0),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (policy_id, model),
    UNIQUE (policy_id, position)
);

CREATE TABLE image_model_policy_audits (
    id BIGSERIAL PRIMARY KEY,
    operator_user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    old_version BIGINT NOT NULL,
    new_version BIGINT NOT NULL,
    before_value JSONB NOT NULL,
    after_value JSONB NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE image_canvas_projects (
    id BIGSERIAL PRIMARY KEY,
    public_id VARCHAR(64) NOT NULL UNIQUE,
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    name VARCHAR(160) NOT NULL,
    document JSONB NOT NULL,
    version BIGINT NOT NULL DEFAULT 1,
    thumbnail_asset_id BIGINT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    deleted_at TIMESTAMPTZ
);

CREATE INDEX idx_image_canvas_projects_user_updated
    ON image_canvas_projects(user_id, updated_at DESC)
    WHERE deleted_at IS NULL;

CREATE TABLE image_assets (
    id BIGSERIAL PRIMARY KEY,
    public_id VARCHAR(64) NOT NULL UNIQUE,
    owner_user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    project_id BIGINT REFERENCES image_canvas_projects(id) ON DELETE SET NULL,
    source_type VARCHAR(20) NOT NULL CHECK (source_type IN ('upload', 'generated', 'derived')),
    object_key TEXT NOT NULL UNIQUE,
    thumbnail_object_key TEXT,
    mime_type VARCHAR(100) NOT NULL,
    width INTEGER NOT NULL CHECK (width > 0),
    height INTEGER NOT NULL CHECK (height > 0),
    byte_size BIGINT NOT NULL CHECK (byte_size > 0),
    sha256 CHAR(64) NOT NULL,
    origin_job_id BIGINT REFERENCES image_jobs(id) ON DELETE SET NULL,
    parent_asset_ids JSONB NOT NULL DEFAULT '[]'::JSONB,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    deleted_at TIMESTAMPTZ
);

ALTER TABLE image_canvas_projects
    ADD CONSTRAINT fk_image_canvas_thumbnail
    FOREIGN KEY (thumbnail_asset_id) REFERENCES image_assets(id) ON DELETE SET NULL;

CREATE INDEX idx_image_assets_owner_project
    ON image_assets(owner_user_id, project_id)
    WHERE deleted_at IS NULL;

CREATE TABLE image_canvas_asset_references (
    project_id BIGINT NOT NULL REFERENCES image_canvas_projects(id) ON DELETE CASCADE,
    asset_id BIGINT NOT NULL REFERENCES image_assets(id) ON DELETE CASCADE,
    node_id VARCHAR(128) NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (project_id, asset_id, node_id)
);

CREATE INDEX idx_image_canvas_asset_references_asset
    ON image_canvas_asset_references(asset_id);

ALTER TABLE image_jobs ADD COLUMN project_id BIGINT REFERENCES image_canvas_projects(id) ON DELETE SET NULL;
ALTER TABLE image_jobs ADD COLUMN client_node_id VARCHAR(128);
ALTER TABLE image_jobs ADD COLUMN selected_model VARCHAR(128);
ALTER TABLE image_jobs ADD COLUMN policy_version BIGINT;
ALTER TABLE image_jobs ADD COLUMN attempt_plan JSONB NOT NULL DEFAULT '[]'::JSONB;
ALTER TABLE image_jobs ADD COLUMN successful_model VARCHAR(128);
ALTER TABLE image_jobs ADD COLUMN attempt_log JSONB NOT NULL DEFAULT '[]'::JSONB;
ALTER TABLE image_job_results ADD COLUMN asset_id BIGINT REFERENCES image_assets(id) ON DELETE SET NULL;

-- Canvas workers expose their durable phase while trying fallback models and
-- while linking generated assets. The base image-job migration predates
-- those phases and only permits preflight/upstream.
ALTER TABLE image_jobs DROP CONSTRAINT IF EXISTS image_jobs_execution_phase_check;
ALTER TABLE image_jobs
    ADD CONSTRAINT image_jobs_execution_phase_check
    CHECK (execution_phase IN ('preflight', 'upstream', 'falling_back', 'saving'));

CREATE INDEX idx_image_jobs_project_created
    ON image_jobs(project_id, created_at DESC)
    WHERE project_id IS NOT NULL;
