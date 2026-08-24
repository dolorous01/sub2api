ALTER TABLE image_assets
    ADD COLUMN media_kind VARCHAR(16) NOT NULL DEFAULT 'image',
    ADD COLUMN duration_ms BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN file_name VARCHAR(255) NOT NULL DEFAULT '';

ALTER TABLE image_assets DROP CONSTRAINT IF EXISTS image_assets_width_check;
ALTER TABLE image_assets DROP CONSTRAINT IF EXISTS image_assets_height_check;

ALTER TABLE image_assets
    ADD CONSTRAINT image_assets_media_kind_check
        CHECK (media_kind IN ('image', 'video', 'audio')),
    ADD CONSTRAINT image_assets_dimensions_check
        CHECK (
            (media_kind IN ('image', 'video') AND width > 0 AND height > 0)
            OR (media_kind = 'audio' AND width = 0 AND height = 0)
        ),
    ADD CONSTRAINT image_assets_duration_check CHECK (duration_ms >= 0);

CREATE INDEX idx_image_assets_owner_kind_created
    ON image_assets(owner_user_id, media_kind, created_at DESC)
    WHERE deleted_at IS NULL;
