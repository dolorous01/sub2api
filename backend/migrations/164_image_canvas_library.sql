CREATE TABLE image_canvas_library_items (
    id BIGSERIAL PRIMARY KEY,
    public_id VARCHAR(64) NOT NULL UNIQUE,
    client_id VARCHAR(128) NOT NULL,
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    kind VARCHAR(16) NOT NULL CHECK (kind IN ('text', 'image', 'video', 'audio')),
    asset_id BIGINT REFERENCES image_assets(id) ON DELETE RESTRICT,
    title VARCHAR(240) NOT NULL,
    content TEXT NOT NULL DEFAULT '',
    tags JSONB NOT NULL DEFAULT '[]'::JSONB,
    source VARCHAR(240) NOT NULL DEFAULT '',
    note TEXT NOT NULL DEFAULT '',
    metadata JSONB NOT NULL DEFAULT '{}'::JSONB,
    version BIGINT NOT NULL DEFAULT 1 CHECK (version > 0),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    deleted_at TIMESTAMPTZ,
    CONSTRAINT image_canvas_library_items_user_client_unique UNIQUE (user_id, client_id),
    CONSTRAINT image_canvas_library_items_client_id_check CHECK (BTRIM(client_id) <> ''),
    CONSTRAINT image_canvas_library_items_title_check CHECK (BTRIM(title) <> ''),
    CONSTRAINT image_canvas_library_items_tags_check CHECK (jsonb_typeof(tags) = 'array'),
    CONSTRAINT image_canvas_library_items_metadata_check CHECK (jsonb_typeof(metadata) = 'object'),
    CONSTRAINT image_canvas_library_items_asset_check CHECK (
        (kind = 'text' AND asset_id IS NULL) OR
        (kind IN ('image', 'video', 'audio') AND asset_id IS NOT NULL)
    )
);

CREATE INDEX idx_image_canvas_library_items_user_updated
    ON image_canvas_library_items(user_id, updated_at DESC, id DESC)
    WHERE deleted_at IS NULL;

CREATE INDEX idx_image_canvas_library_items_asset
    ON image_canvas_library_items(asset_id)
    WHERE deleted_at IS NULL AND asset_id IS NOT NULL;
