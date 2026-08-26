CREATE TABLE image_editor_documents (
    id BIGSERIAL PRIMARY KEY,
    public_id VARCHAR(64) NOT NULL UNIQUE,
    project_id BIGINT NOT NULL REFERENCES image_canvas_projects(id) ON DELETE CASCADE,
    node_id VARCHAR(128) NOT NULL,
    base_asset_id BIGINT NOT NULL REFERENCES image_assets(id) ON DELETE RESTRICT,
    current_asset_id BIGINT NOT NULL REFERENCES image_assets(id) ON DELETE RESTRICT,
    document JSONB NOT NULL,
    version BIGINT NOT NULL DEFAULT 1 CHECK (version > 0),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    deleted_at TIMESTAMPTZ,
    CONSTRAINT image_editor_documents_node_id_check CHECK (BTRIM(node_id) <> ''),
    CONSTRAINT image_editor_documents_document_check CHECK (jsonb_typeof(document) = 'object')
);

CREATE UNIQUE INDEX idx_image_editor_documents_project_node_active
    ON image_editor_documents(project_id, node_id)
    WHERE deleted_at IS NULL;

CREATE INDEX idx_image_editor_documents_project_updated
    ON image_editor_documents(project_id, updated_at DESC)
    WHERE deleted_at IS NULL;

CREATE TABLE image_editor_asset_references (
    document_id BIGINT NOT NULL REFERENCES image_editor_documents(id) ON DELETE CASCADE,
    asset_id BIGINT NOT NULL REFERENCES image_assets(id) ON DELETE RESTRICT,
    role VARCHAR(16) NOT NULL,
    element_id VARCHAR(128) NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (document_id, asset_id, role, element_id),
    CONSTRAINT image_editor_asset_references_role_check CHECK (role IN ('source', 'layer', 'mask', 'result')),
    CONSTRAINT image_editor_asset_references_element_id_check CHECK (BTRIM(element_id) <> '')
);

CREATE INDEX idx_image_editor_asset_references_asset
    ON image_editor_asset_references(asset_id);

CREATE TABLE image_editor_revisions (
    id BIGSERIAL PRIMARY KEY,
    public_id VARCHAR(64) NOT NULL UNIQUE,
    document_id BIGINT NOT NULL REFERENCES image_editor_documents(id) ON DELETE CASCADE,
    version BIGINT NOT NULL CHECK (version > 0),
    asset_id BIGINT NOT NULL REFERENCES image_assets(id) ON DELETE RESTRICT,
    operation VARCHAR(32) NOT NULL,
    parameters JSONB NOT NULL DEFAULT '{}'::JSONB,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (document_id, version),
    CONSTRAINT image_editor_revisions_operation_check CHECK (BTRIM(operation) <> ''),
    CONSTRAINT image_editor_revisions_parameters_check CHECK (jsonb_typeof(parameters) = 'object')
);

CREATE INDEX idx_image_editor_revisions_document_created
    ON image_editor_revisions(document_id, created_at DESC, id DESC);

ALTER TABLE image_jobs
    ADD COLUMN editor_document_id BIGINT REFERENCES image_editor_documents(id) ON DELETE SET NULL;

CREATE INDEX idx_image_jobs_editor_created
    ON image_jobs(editor_document_id, created_at DESC)
    WHERE editor_document_id IS NOT NULL;
