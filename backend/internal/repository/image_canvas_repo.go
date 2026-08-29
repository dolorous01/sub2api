package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/google/uuid"
)

type imageCanvasRepository struct {
	db *sql.DB
}

var _ service.ImageCanvasRepository = (*imageCanvasRepository)(nil)

func NewImageCanvasRepository(db *sql.DB) service.ImageCanvasRepository {
	return &imageCanvasRepository{db: db}
}

func (r *imageCanvasRepository) ListProjects(ctx context.Context, userID int64) ([]service.ImageCanvasProject, error) {
	if r == nil || r.db == nil {
		return nil, fmt.Errorf("image canvas database is required")
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, public_id, user_id, name, document, version, thumbnail_asset_id,
			created_at, updated_at
		FROM image_canvas_projects
		WHERE user_id = $1 AND deleted_at IS NULL
		ORDER BY updated_at DESC, id DESC`, userID)
	if err != nil {
		return nil, fmt.Errorf("list image canvas projects: %w", err)
	}
	defer func() { _ = rows.Close() }()

	projects := make([]service.ImageCanvasProject, 0)
	for rows.Next() {
		project, err := scanImageCanvasProject(rows)
		if err != nil {
			return nil, fmt.Errorf("scan image canvas project: %w", err)
		}
		projects = append(projects, *project)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate image canvas projects: %w", err)
	}
	return projects, nil
}

func (r *imageCanvasRepository) CreateProject(
	ctx context.Context,
	userID int64,
	publicID string,
	name string,
	document json.RawMessage,
	assetRefs []service.ImageCanvasAssetReference,
) (*service.ImageCanvasProject, error) {
	if r == nil || r.db == nil {
		return nil, fmt.Errorf("image canvas database is required")
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin image canvas project create: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	publicID = strings.TrimSpace(publicID)
	if publicID == "" {
		publicID = newImageCanvasPublicID("icp")
	}
	project, err := scanImageCanvasProject(tx.QueryRowContext(ctx, `
		INSERT INTO image_canvas_projects (public_id, user_id, name, document)
		VALUES ($1, $2, $3, $4::jsonb)
		RETURNING id, public_id, user_id, name, document, version, thumbnail_asset_id,
				created_at, updated_at`, publicID, userID, strings.TrimSpace(name), string(document)))
	if err != nil {
		return nil, fmt.Errorf("create image canvas project: %w", err)
	}
	if err := replaceImageCanvasAssetReferences(ctx, tx, project.ID, userID, assetRefs); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit image canvas project create: %w", err)
	}
	return project, nil
}

func (r *imageCanvasRepository) GetProject(ctx context.Context, userID int64, publicID string) (*service.ImageCanvasProject, error) {
	if r == nil || r.db == nil {
		return nil, fmt.Errorf("image canvas database is required")
	}
	project, err := scanImageCanvasProject(r.db.QueryRowContext(ctx, `
		SELECT id, public_id, user_id, name, document, version, thumbnail_asset_id,
			created_at, updated_at
		FROM image_canvas_projects
		WHERE public_id = $1 AND user_id = $2 AND deleted_at IS NULL`, strings.TrimSpace(publicID), userID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, service.ErrImageCanvasProjectNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get image canvas project: %w", err)
	}
	return project, nil
}

func (r *imageCanvasRepository) UpdateProject(
	ctx context.Context,
	userID int64,
	publicID string,
	version int64,
	name string,
	document json.RawMessage,
	assetRefs []service.ImageCanvasAssetReference,
) (*service.ImageCanvasProject, error) {
	if r == nil || r.db == nil {
		return nil, fmt.Errorf("image canvas database is required")
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin image canvas project update: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	project, err := scanImageCanvasProject(tx.QueryRowContext(ctx, `
		UPDATE image_canvas_projects
		SET name = $4, document = $5::jsonb, version = version + 1, updated_at = NOW()
		WHERE public_id = $1 AND user_id = $2 AND version = $3 AND deleted_at IS NULL
		RETURNING id, public_id, user_id, name, document, version, thumbnail_asset_id,
			created_at, updated_at`, strings.TrimSpace(publicID), userID, version, strings.TrimSpace(name), string(document)))
	if errors.Is(err, sql.ErrNoRows) {
		var exists bool
		lookupErr := tx.QueryRowContext(ctx, `
			SELECT EXISTS(
				SELECT 1 FROM image_canvas_projects
				WHERE public_id = $1 AND user_id = $2 AND deleted_at IS NULL
			)`, strings.TrimSpace(publicID), userID).Scan(&exists)
		if lookupErr != nil {
			return nil, fmt.Errorf("resolve image canvas update conflict: %w", lookupErr)
		}
		if exists {
			return nil, service.ErrImageCanvasProjectVersionConflict
		}
		return nil, service.ErrImageCanvasProjectNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("update image canvas project: %w", err)
	}
	if err := replaceImageCanvasAssetReferences(ctx, tx, project.ID, userID, assetRefs); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit image canvas project update: %w", err)
	}
	return project, nil
}

func (r *imageCanvasRepository) DeleteProject(ctx context.Context, userID int64, publicID string) error {
	if r == nil || r.db == nil {
		return fmt.Errorf("image canvas database is required")
	}
	result, err := r.db.ExecContext(ctx, `
		UPDATE image_canvas_projects
		SET deleted_at = NOW(), updated_at = NOW()
		WHERE public_id = $1 AND user_id = $2 AND deleted_at IS NULL`, strings.TrimSpace(publicID), userID)
	if err != nil {
		return fmt.Errorf("delete image canvas project: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read image canvas project delete result: %w", err)
	}
	if affected == 0 {
		return service.ErrImageCanvasProjectNotFound
	}
	return nil
}

func (r *imageCanvasRepository) CreateAsset(ctx context.Context, input service.ImageAssetCreate) (*service.ImageAsset, error) {
	if r == nil || r.db == nil {
		return nil, fmt.Errorf("image canvas database is required")
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin image asset create: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if input.ProjectID != nil {
		var exists bool
		if err := tx.QueryRowContext(ctx, `
			SELECT EXISTS(
				SELECT 1 FROM image_canvas_projects
				WHERE id = $1 AND user_id = $2 AND deleted_at IS NULL
			)`, *input.ProjectID, input.OwnerUserID).Scan(&exists); err != nil {
			return nil, fmt.Errorf("validate image asset project: %w", err)
		}
		if !exists {
			return nil, service.ErrImageCanvasProjectNotFound
		}
	}
	parents := input.ParentAssetIDs
	if len(parents) == 0 {
		parents = json.RawMessage(`[]`)
	}
	publicID := strings.TrimSpace(input.PublicID)
	if publicID == "" {
		publicID = newImageCanvasPublicID("asset")
	}
	asset, err := scanImageAsset(tx.QueryRowContext(ctx, `
		INSERT INTO image_assets (
				public_id, owner_user_id, project_id, source_type, media_kind, file_name, object_key,
				thumbnail_object_key, mime_type, width, height, byte_size, sha256,
				duration_ms, origin_job_id, parent_asset_ids
			) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16::jsonb)
			RETURNING id, public_id, owner_user_id, project_id, source_type, media_kind, file_name, object_key,
				thumbnail_object_key, mime_type, width, height, byte_size, sha256,
				duration_ms, origin_job_id, parent_asset_ids, created_at`,
		publicID, input.OwnerUserID, nullableInt64Pointer(input.ProjectID),
		input.SourceType, input.MediaKind, input.FileName, input.ObjectKey, nullableString(input.ThumbnailObjectKey), input.MIMEType,
		input.Width, input.Height, input.ByteSize, input.SHA256, input.DurationMS, nullableInt64Pointer(input.OriginJobID), string(parents)))
	if err != nil {
		return nil, fmt.Errorf("create image asset: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit image asset create: %w", err)
	}
	return asset, nil
}

func (r *imageCanvasRepository) GetAsset(ctx context.Context, userID int64, publicID string) (*service.ImageAsset, error) {
	if r == nil || r.db == nil {
		return nil, fmt.Errorf("image canvas database is required")
	}
	asset, err := scanImageAsset(r.db.QueryRowContext(ctx, `
		SELECT id, public_id, owner_user_id, project_id, source_type, media_kind, file_name, object_key,
			thumbnail_object_key, mime_type, width, height, byte_size, sha256,
			duration_ms, origin_job_id, parent_asset_ids, created_at
		FROM image_assets
		WHERE public_id = $1 AND owner_user_id = $2 AND deleted_at IS NULL`, strings.TrimSpace(publicID), userID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, service.ErrImageAssetNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get image asset: %w", err)
	}
	return asset, nil
}

func (r *imageCanvasRepository) ListLibraryItems(ctx context.Context, userID int64) ([]service.ImageCanvasLibraryItem, error) {
	if r == nil || r.db == nil {
		return nil, fmt.Errorf("image canvas database is required")
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT item.id, item.public_id, item.client_id, item.user_id, item.kind, item.asset_id,
			COALESCE(asset.public_id, ''), item.title, item.content, item.tags, item.source,
			item.note, item.metadata, item.version, item.created_at, item.updated_at
		FROM image_canvas_library_items item
		LEFT JOIN image_assets asset ON asset.id = item.asset_id
		WHERE item.user_id = $1 AND item.deleted_at IS NULL
		ORDER BY item.updated_at DESC, item.id DESC`, userID)
	if err != nil {
		return nil, fmt.Errorf("list image canvas library items: %w", err)
	}
	defer func() { _ = rows.Close() }()

	items := make([]service.ImageCanvasLibraryItem, 0)
	for rows.Next() {
		item, scanErr := scanImageCanvasLibraryItem(rows)
		if scanErr != nil {
			return nil, fmt.Errorf("scan image canvas library item: %w", scanErr)
		}
		items = append(items, *item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate image canvas library items: %w", err)
	}
	return items, nil
}

func (r *imageCanvasRepository) CreateLibraryItem(
	ctx context.Context,
	input service.ImageCanvasLibraryItemWrite,
) (*service.ImageCanvasLibraryItem, bool, error) {
	if r == nil || r.db == nil {
		return nil, false, fmt.Errorf("image canvas database is required")
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, false, fmt.Errorf("begin image canvas library item create: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	assetID, err := resolveImageCanvasLibraryAssetID(ctx, tx, input.UserID, input.Kind, input.AssetPublicID)
	if err != nil {
		return nil, false, err
	}
	tags, err := json.Marshal(input.Tags)
	if err != nil {
		return nil, false, service.ErrImageCanvasLibraryItemInvalid
	}
	publicID := strings.TrimSpace(input.PublicID)
	if publicID == "" {
		publicID = newImageCanvasPublicID("lib")
	}
	item, err := scanImageCanvasLibraryItem(tx.QueryRowContext(ctx, `
		WITH inserted AS (
			INSERT INTO image_canvas_library_items (
				public_id, client_id, user_id, kind, asset_id, title, content, tags, source, note, metadata
			) VALUES ($1, $2, $3, $4, $5, $6, $7, $8::jsonb, $9, $10, $11::jsonb)
			ON CONFLICT (user_id, client_id) DO NOTHING
			RETURNING *
		)
		SELECT item.id, item.public_id, item.client_id, item.user_id, item.kind, item.asset_id,
			COALESCE(asset.public_id, ''), item.title, item.content, item.tags, item.source,
			item.note, item.metadata, item.version, item.created_at, item.updated_at
		FROM inserted item
		LEFT JOIN image_assets asset ON asset.id = item.asset_id`,
		publicID, input.ClientID, input.UserID, input.Kind, nullableInt64Pointer(assetID), input.Title,
		input.Content, string(tags), input.Source, input.Note, string(input.Metadata)))
	created := true
	if errors.Is(err, sql.ErrNoRows) {
		created = false
		item, err = scanImageCanvasLibraryItem(tx.QueryRowContext(ctx, `
			SELECT item.id, item.public_id, item.client_id, item.user_id, item.kind, item.asset_id,
				COALESCE(asset.public_id, ''), item.title, item.content, item.tags, item.source,
				item.note, item.metadata, item.version, item.created_at, item.updated_at
			FROM image_canvas_library_items item
			LEFT JOIN image_assets asset ON asset.id = item.asset_id
			WHERE item.user_id = $1 AND item.client_id = $2 AND item.deleted_at IS NULL`, input.UserID, input.ClientID))
		if errors.Is(err, sql.ErrNoRows) {
			return nil, false, service.ErrImageCanvasLibraryClientConflict
		}
	}
	if err != nil {
		return nil, false, fmt.Errorf("create image canvas library item: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, false, fmt.Errorf("commit image canvas library item create: %w", err)
	}
	return item, created, nil
}

func (r *imageCanvasRepository) UpdateLibraryItem(
	ctx context.Context,
	input service.ImageCanvasLibraryItemWrite,
) (*service.ImageCanvasLibraryItem, error) {
	if r == nil || r.db == nil {
		return nil, fmt.Errorf("image canvas database is required")
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin image canvas library item update: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	assetID, err := resolveImageCanvasLibraryAssetID(ctx, tx, input.UserID, input.Kind, input.AssetPublicID)
	if err != nil {
		return nil, err
	}
	tags, err := json.Marshal(input.Tags)
	if err != nil {
		return nil, service.ErrImageCanvasLibraryItemInvalid
	}
	item, err := scanImageCanvasLibraryItem(tx.QueryRowContext(ctx, `
		WITH updated AS (
			UPDATE image_canvas_library_items
			SET kind = $4, asset_id = $5, title = $6, content = $7, tags = $8::jsonb,
				source = $9, note = $10, metadata = $11::jsonb,
				version = version + 1, updated_at = NOW()
			WHERE public_id = $1 AND user_id = $2 AND version = $3 AND deleted_at IS NULL
			RETURNING *
		)
		SELECT updated_item.id, updated_item.public_id, updated_item.client_id, updated_item.user_id, updated_item.kind, updated_item.asset_id,
			COALESCE(asset.public_id, ''), updated_item.title, updated_item.content, updated_item.tags, updated_item.source,
			updated_item.note, updated_item.metadata, updated_item.version, updated_item.created_at, updated_item.updated_at
		FROM updated updated_item
		LEFT JOIN image_assets asset ON asset.id = updated_item.asset_id`,
		input.PublicID, input.UserID, input.Version, input.Kind, nullableInt64Pointer(assetID), input.Title,
		input.Content, string(tags), input.Source, input.Note, string(input.Metadata)))
	if errors.Is(err, sql.ErrNoRows) {
		var exists bool
		lookupErr := tx.QueryRowContext(ctx, `
			SELECT EXISTS(
				SELECT 1 FROM image_canvas_library_items
				WHERE public_id = $1 AND user_id = $2 AND deleted_at IS NULL
			)`, input.PublicID, input.UserID).Scan(&exists)
		if lookupErr != nil {
			return nil, fmt.Errorf("resolve image canvas library update conflict: %w", lookupErr)
		}
		if exists {
			return nil, service.ErrImageCanvasLibraryVersionConflict
		}
		return nil, service.ErrImageCanvasLibraryItemNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("update image canvas library item: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit image canvas library item update: %w", err)
	}
	return item, nil
}

func (r *imageCanvasRepository) DeleteLibraryItem(ctx context.Context, userID int64, publicID string) error {
	if r == nil || r.db == nil {
		return fmt.Errorf("image canvas database is required")
	}
	result, err := r.db.ExecContext(ctx, `
		UPDATE image_canvas_library_items
		SET deleted_at = NOW(), updated_at = NOW()
		WHERE public_id = $1 AND user_id = $2 AND deleted_at IS NULL`, strings.TrimSpace(publicID), userID)
	if err != nil {
		return fmt.Errorf("delete image canvas library item: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read image canvas library item delete result: %w", err)
	}
	if affected == 0 {
		return service.ErrImageCanvasLibraryItemNotFound
	}
	return nil
}

func (r *imageCanvasRepository) ListOpenJobs(ctx context.Context, userID int64, projectID int64) ([]service.ImageJob, error) {
	if r == nil || r.db == nil {
		return nil, fmt.Errorf("image canvas database is required")
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT `+imageJobColumns+`
		FROM image_jobs
		WHERE user_id = $1 AND project_id = $2
			AND (
				status IN ('queued', 'running')
				OR updated_at > COALESCE(
					(SELECT updated_at FROM image_canvas_projects WHERE id = $2),
					updated_at
				)
			)
		ORDER BY created_at, id
		LIMIT 100`, userID, projectID)
	if err != nil {
		return nil, fmt.Errorf("list open image canvas jobs: %w", err)
	}
	defer func() { _ = rows.Close() }()
	jobs := make([]service.ImageJob, 0)
	for rows.Next() {
		job, err := scanImageJob(rows)
		if err != nil {
			return nil, fmt.Errorf("scan open image canvas job: %w", err)
		}
		if err := loadImageJobRelations(ctx, r.db, job); err != nil {
			return nil, err
		}
		jobs = append(jobs, *job)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate open image canvas jobs: %w", err)
	}
	return jobs, nil
}

type imageCanvasScanner interface {
	Scan(dest ...any) error
}

func scanImageCanvasProject(scanner imageCanvasScanner) (*service.ImageCanvasProject, error) {
	project := &service.ImageCanvasProject{}
	var document []byte
	var thumbnailID sql.NullInt64
	if err := scanner.Scan(
		&project.ID, &project.PublicID, &project.UserID, &project.Name, &document,
		&project.Version, &thumbnailID, &project.CreatedAt, &project.UpdatedAt,
	); err != nil {
		return nil, err
	}
	project.Document = append(json.RawMessage(nil), document...)
	project.ThumbnailAssetID = nullInt64Pointer(thumbnailID)
	return project, nil
}

func scanImageAsset(scanner imageCanvasScanner) (*service.ImageAsset, error) {
	asset := &service.ImageAsset{}
	var projectID, originJobID sql.NullInt64
	var thumbnailObjectKey sql.NullString
	var parentIDs []byte
	if err := scanner.Scan(
		&asset.ID, &asset.PublicID, &asset.OwnerUserID, &projectID, &asset.SourceType, &asset.MediaKind, &asset.FileName,
		&asset.ObjectKey, &thumbnailObjectKey, &asset.MIMEType, &asset.Width, &asset.Height,
		&asset.ByteSize, &asset.SHA256, &asset.DurationMS, &originJobID, &parentIDs, &asset.CreatedAt,
	); err != nil {
		return nil, err
	}
	asset.ProjectID = nullInt64Pointer(projectID)
	asset.OriginJobID = nullInt64Pointer(originJobID)
	asset.ThumbnailObjectKey = thumbnailObjectKey.String
	asset.ParentAssetIDs = append(json.RawMessage(nil), parentIDs...)
	return asset, nil
}

func scanImageCanvasLibraryItem(scanner imageCanvasScanner) (*service.ImageCanvasLibraryItem, error) {
	item := &service.ImageCanvasLibraryItem{}
	var assetID sql.NullInt64
	var tags, metadata []byte
	if err := scanner.Scan(
		&item.ID, &item.PublicID, &item.ClientID, &item.UserID, &item.Kind, &assetID,
		&item.AssetPublicID, &item.Title, &item.Content, &tags, &item.Source,
		&item.Note, &metadata, &item.Version, &item.CreatedAt, &item.UpdatedAt,
	); err != nil {
		return nil, err
	}
	item.AssetID = nullInt64Pointer(assetID)
	item.Tags = make([]string, 0)
	if len(tags) > 0 {
		if err := json.Unmarshal(tags, &item.Tags); err != nil {
			return nil, err
		}
	}
	item.Metadata = append(json.RawMessage(nil), metadata...)
	return item, nil
}

func resolveImageCanvasLibraryAssetID(
	ctx context.Context,
	tx *sql.Tx,
	userID int64,
	kind, publicID string,
) (*int64, error) {
	if kind == "text" {
		return nil, nil
	}
	var id int64
	var mediaKind string
	err := tx.QueryRowContext(ctx, `
		SELECT id, media_kind
		FROM image_assets
		WHERE public_id = $1 AND owner_user_id = $2 AND deleted_at IS NULL`, strings.TrimSpace(publicID), userID).Scan(&id, &mediaKind)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, service.ErrImageAssetNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("resolve image canvas library asset: %w", err)
	}
	if mediaKind != kind {
		return nil, service.ErrImageCanvasLibraryItemInvalid
	}
	return &id, nil
}

func replaceImageCanvasAssetReferences(
	ctx context.Context,
	tx *sql.Tx,
	projectID, userID int64,
	refs []service.ImageCanvasAssetReference,
) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM image_canvas_asset_references WHERE project_id = $1`, projectID); err != nil {
		return fmt.Errorf("delete image canvas asset references: %w", err)
	}
	seen := make(map[string]struct{}, len(refs))
	for _, ref := range refs {
		assetPublicID := strings.TrimSpace(ref.AssetPublicID)
		nodeID := strings.TrimSpace(ref.NodeID)
		if assetPublicID == "" || nodeID == "" || len(nodeID) > 128 {
			return service.ErrImageCanvasDocumentInvalid
		}
		key := assetPublicID + "\x00" + nodeID
		if _, duplicate := seen[key]; duplicate {
			continue
		}
		seen[key] = struct{}{}
		var assetID int64
		err := tx.QueryRowContext(ctx, `
			SELECT id FROM image_assets
			WHERE public_id = $1 AND owner_user_id = $2 AND deleted_at IS NULL`, assetPublicID, userID).Scan(&assetID)
		if errors.Is(err, sql.ErrNoRows) {
			return service.ErrImageAssetNotFound
		}
		if err != nil {
			return fmt.Errorf("validate image canvas asset reference: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO image_canvas_asset_references (project_id, asset_id, node_id)
			VALUES ($1, $2, $3)`, projectID, assetID, nodeID); err != nil {
			return fmt.Errorf("insert image canvas asset reference: %w", err)
		}
	}
	return nil
}

func newImageCanvasPublicID(prefix string) string {
	return prefix + "_" + strings.ReplaceAll(uuid.NewString(), "-", "")
}
