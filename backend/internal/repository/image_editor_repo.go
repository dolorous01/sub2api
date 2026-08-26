package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

type imageEditorRepository struct {
	db *sql.DB
}

var _ service.ImageEditorRepository = (*imageEditorRepository)(nil)

func NewImageEditorRepository(db *sql.DB) service.ImageEditorRepository {
	return &imageEditorRepository{db: db}
}

func (r *imageEditorRepository) CreateOrGet(
	ctx context.Context,
	input service.ImageEditorDocumentCreate,
) (*service.ImageEditorDocument, bool, error) {
	if r == nil || r.db == nil {
		return nil, false, fmt.Errorf("image editor database is required")
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, false, fmt.Errorf("begin image editor document create: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var projectID int64
	err = tx.QueryRowContext(ctx, `
		SELECT id
		FROM image_canvas_projects
		WHERE public_id = $1 AND user_id = $2 AND deleted_at IS NULL
		FOR SHARE`, strings.TrimSpace(input.ProjectPublicID), input.UserID).Scan(&projectID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, service.ErrImageCanvasProjectNotFound
	}
	if err != nil {
		return nil, false, fmt.Errorf("resolve image editor project: %w", err)
	}

	baseAssetID, err := findOwnedEditorImageAssetID(ctx, tx, input.UserID, input.BaseAssetPublicID)
	if err != nil {
		return nil, false, err
	}

	document, err := scanImageEditorDocument(tx.QueryRowContext(ctx, `
		INSERT INTO image_editor_documents (
			public_id, project_id, node_id, base_asset_id, current_asset_id, document
		) VALUES ($1, $2, $3, $4, $4, $5::jsonb)
		ON CONFLICT (project_id, node_id) WHERE deleted_at IS NULL DO NOTHING
		RETURNING id, public_id, project_id, node_id, base_asset_id, current_asset_id,
			document, version, created_at, updated_at`,
		newImageCanvasPublicID("ied"), projectID, strings.TrimSpace(input.NodeID),
		baseAssetID, string(input.Document),
	))
	created := err == nil
	if errors.Is(err, sql.ErrNoRows) {
		document, err = scanImageEditorDocument(tx.QueryRowContext(ctx, `
			SELECT id, public_id, project_id, node_id, base_asset_id, current_asset_id,
				document, version, created_at, updated_at
			FROM image_editor_documents
			WHERE project_id = $1 AND node_id = $2 AND deleted_at IS NULL
			FOR UPDATE`, projectID, strings.TrimSpace(input.NodeID)))
	}
	if err != nil {
		return nil, false, fmt.Errorf("create or resolve image editor document: %w", err)
	}
	document.ProjectPublicID = strings.TrimSpace(input.ProjectPublicID)
	document.UserID = input.UserID
	if document.BaseAssetID != baseAssetID {
		return nil, false, service.ErrImageEditorDocumentConflict
	}

	if created {
		if err := replaceImageEditorAssetReferences(
			ctx, tx, document.ID, input.UserID, document.BaseAssetID,
			document.CurrentAssetID, input.AssetReferences, true,
		); err != nil {
			return nil, false, err
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO image_editor_revisions (
				public_id, document_id, version, asset_id, operation, parameters
			) VALUES ($1, $2, $3, $4, 'source', '{}'::jsonb)`,
			newImageCanvasPublicID("ier"), document.ID, document.Version, document.BaseAssetID,
		); err != nil {
			return nil, false, fmt.Errorf("create initial image editor revision: %w", err)
		}
	}
	if err := loadImageEditorDocumentRelations(ctx, tx, document); err != nil {
		return nil, false, err
	}
	if err := tx.Commit(); err != nil {
		return nil, false, fmt.Errorf("commit image editor document create: %w", err)
	}
	return document, created, nil
}

func (r *imageEditorRepository) GetOwned(
	ctx context.Context,
	userID int64,
	publicID string,
) (*service.ImageEditorDocument, error) {
	if r == nil || r.db == nil {
		return nil, fmt.Errorf("image editor database is required")
	}
	document, err := scanOwnedImageEditorDocument(r.db.QueryRowContext(ctx, `
		SELECT d.id, d.public_id, d.project_id, d.node_id, d.base_asset_id,
			d.current_asset_id, d.document, d.version, d.created_at, d.updated_at,
			p.public_id, p.user_id
		FROM image_editor_documents d
		JOIN image_canvas_projects p ON p.id = d.project_id
		WHERE d.public_id = $1 AND d.deleted_at IS NULL
			AND p.user_id = $2 AND p.deleted_at IS NULL`,
		strings.TrimSpace(publicID), userID,
	))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, service.ErrImageEditorDocumentNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get image editor document: %w", err)
	}
	if err := loadImageEditorDocumentRelations(ctx, r.db, document); err != nil {
		return nil, err
	}
	return document, nil
}

func (r *imageEditorRepository) Update(
	ctx context.Context,
	input service.ImageEditorDocumentUpdate,
) (*service.ImageEditorDocument, error) {
	if r == nil || r.db == nil {
		return nil, fmt.Errorf("image editor database is required")
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin image editor document update: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	document, err := scanOwnedImageEditorDocument(tx.QueryRowContext(ctx, `
		SELECT d.id, d.public_id, d.project_id, d.node_id, d.base_asset_id,
			d.current_asset_id, d.document, d.version, d.created_at, d.updated_at,
			p.public_id, p.user_id
		FROM image_editor_documents d
		JOIN image_canvas_projects p ON p.id = d.project_id
		WHERE d.public_id = $1 AND d.deleted_at IS NULL
			AND p.user_id = $2 AND p.deleted_at IS NULL
		FOR UPDATE OF d`, strings.TrimSpace(input.PublicID), input.UserID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, service.ErrImageEditorDocumentNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("lock image editor document: %w", err)
	}
	if document.Version != input.Version {
		return nil, service.ErrImageEditorDocumentVersionConflict
	}

	currentAssetID := document.CurrentAssetID
	if strings.TrimSpace(input.CurrentAssetPublicID) != "" {
		currentAssetID, err = findOwnedEditorImageAssetID(
			ctx, tx, input.UserID, input.CurrentAssetPublicID,
		)
		if err != nil {
			return nil, err
		}
	}

	updated, err := scanImageEditorDocument(tx.QueryRowContext(ctx, `
		UPDATE image_editor_documents
		SET document = $3::jsonb, current_asset_id = $4,
			version = version + 1, updated_at = NOW()
		WHERE id = $1 AND version = $2 AND deleted_at IS NULL
		RETURNING id, public_id, project_id, node_id, base_asset_id, current_asset_id,
			document, version, created_at, updated_at`,
		document.ID, input.Version, string(input.Document), currentAssetID,
	))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, service.ErrImageEditorDocumentVersionConflict
	}
	if err != nil {
		return nil, fmt.Errorf("update image editor document: %w", err)
	}
	updated.ProjectPublicID = document.ProjectPublicID
	updated.UserID = document.UserID

	if err := replaceImageEditorAssetReferences(
		ctx, tx, updated.ID, input.UserID, updated.BaseAssetID, updated.CurrentAssetID,
		input.AssetReferences, input.AssetReferences != nil,
	); err != nil {
		return nil, err
	}
	if strings.TrimSpace(input.Operation) != "" {
		parameters := input.Parameters
		if len(parameters) == 0 {
			parameters = json.RawMessage("{}")
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO image_editor_revisions (
				public_id, document_id, version, asset_id, operation, parameters
			) VALUES ($1, $2, $3, $4, $5, $6::jsonb)`,
			newImageCanvasPublicID("ier"), updated.ID, updated.Version,
			updated.CurrentAssetID, strings.TrimSpace(input.Operation), string(parameters),
		); err != nil {
			return nil, fmt.Errorf("append image editor revision: %w", err)
		}
	}
	if err := loadImageEditorDocumentRelations(ctx, tx, updated); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit image editor document update: %w", err)
	}
	return updated, nil
}

type imageEditorQueryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func scanImageEditorDocument(scanner imageCanvasScanner) (*service.ImageEditorDocument, error) {
	document := &service.ImageEditorDocument{}
	var raw []byte
	if err := scanner.Scan(
		&document.ID, &document.PublicID, &document.ProjectID, &document.NodeID,
		&document.BaseAssetID, &document.CurrentAssetID, &raw, &document.Version,
		&document.CreatedAt, &document.UpdatedAt,
	); err != nil {
		return nil, err
	}
	document.Document = append(json.RawMessage(nil), raw...)
	return document, nil
}

func scanOwnedImageEditorDocument(scanner imageCanvasScanner) (*service.ImageEditorDocument, error) {
	document := &service.ImageEditorDocument{}
	var raw []byte
	if err := scanner.Scan(
		&document.ID, &document.PublicID, &document.ProjectID, &document.NodeID,
		&document.BaseAssetID, &document.CurrentAssetID, &raw, &document.Version,
		&document.CreatedAt, &document.UpdatedAt, &document.ProjectPublicID, &document.UserID,
	); err != nil {
		return nil, err
	}
	document.Document = append(json.RawMessage(nil), raw...)
	return document, nil
}

func loadImageEditorDocumentRelations(
	ctx context.Context,
	queryer imageEditorQueryer,
	document *service.ImageEditorDocument,
) error {
	base, err := getOwnedEditorImageAssetByID(ctx, queryer, document.UserID, document.BaseAssetID)
	if err != nil {
		return fmt.Errorf("load image editor base asset: %w", err)
	}
	current, err := getOwnedEditorImageAssetByID(ctx, queryer, document.UserID, document.CurrentAssetID)
	if err != nil {
		return fmt.Errorf("load image editor current asset: %w", err)
	}
	document.BaseAsset = base
	document.CurrentAsset = current

	rows, err := queryer.QueryContext(ctx, `
		SELECT r.asset_id, a.public_id, r.role, r.element_id
		FROM image_editor_asset_references r
		JOIN image_assets a ON a.id = r.asset_id
		WHERE r.document_id = $1 AND a.owner_user_id = $2
		ORDER BY r.role, r.element_id, r.asset_id`, document.ID, document.UserID)
	if err != nil {
		return fmt.Errorf("list image editor asset references: %w", err)
	}
	document.AssetReferences = make([]service.ImageEditorAssetReference, 0)
	for rows.Next() {
		var reference service.ImageEditorAssetReference
		if err := rows.Scan(
			&reference.AssetID, &reference.AssetPublicID, &reference.Role, &reference.ElementID,
		); err != nil {
			_ = rows.Close()
			return fmt.Errorf("scan image editor asset reference: %w", err)
		}
		document.AssetReferences = append(document.AssetReferences, reference)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("close image editor asset references: %w", err)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate image editor asset references: %w", err)
	}

	revisionRows, err := queryer.QueryContext(ctx, `
		WITH latest AS (
			SELECT id, public_id, document_id, version, asset_id, operation, parameters, created_at
			FROM image_editor_revisions
			WHERE document_id = $1
			ORDER BY version DESC, id DESC
			LIMIT 100
		)
		SELECT latest.id, latest.public_id, latest.document_id, latest.version,
			latest.asset_id, latest.operation, latest.parameters, latest.created_at,
			a.id, a.public_id, a.owner_user_id, a.project_id, a.source_type,
			a.media_kind, a.file_name, a.object_key, a.thumbnail_object_key,
			a.mime_type, a.width, a.height, a.byte_size, a.sha256, a.duration_ms,
			a.origin_job_id, a.parent_asset_ids, a.created_at
		FROM latest
		JOIN image_assets a ON a.id = latest.asset_id
		WHERE a.owner_user_id = $2
		ORDER BY latest.version, latest.id`, document.ID, document.UserID)
	if err != nil {
		return fmt.Errorf("list image editor revisions: %w", err)
	}
	defer func() { _ = revisionRows.Close() }()
	document.Revisions = make([]service.ImageEditorRevision, 0)
	for revisionRows.Next() {
		revision, err := scanImageEditorRevision(revisionRows)
		if err != nil {
			return fmt.Errorf("scan image editor revision: %w", err)
		}
		document.Revisions = append(document.Revisions, *revision)
	}
	if err := revisionRows.Err(); err != nil {
		return fmt.Errorf("iterate image editor revisions: %w", err)
	}
	return nil
}

func scanImageEditorRevision(scanner imageCanvasScanner) (*service.ImageEditorRevision, error) {
	revision := &service.ImageEditorRevision{}
	asset := &service.ImageAsset{}
	var parameters, parentIDs []byte
	var projectID, originJobID sql.NullInt64
	var thumbnailObjectKey sql.NullString
	if err := scanner.Scan(
		&revision.ID, &revision.PublicID, &revision.DocumentID, &revision.Version,
		&revision.AssetID, &revision.Operation, &parameters, &revision.CreatedAt,
		&asset.ID, &asset.PublicID, &asset.OwnerUserID, &projectID, &asset.SourceType,
		&asset.MediaKind, &asset.FileName, &asset.ObjectKey, &thumbnailObjectKey,
		&asset.MIMEType, &asset.Width, &asset.Height, &asset.ByteSize, &asset.SHA256,
		&asset.DurationMS, &originJobID, &parentIDs, &asset.CreatedAt,
	); err != nil {
		return nil, err
	}
	revision.Parameters = append(json.RawMessage(nil), parameters...)
	asset.ProjectID = nullInt64Pointer(projectID)
	asset.OriginJobID = nullInt64Pointer(originJobID)
	asset.ThumbnailObjectKey = thumbnailObjectKey.String
	asset.ParentAssetIDs = append(json.RawMessage(nil), parentIDs...)
	revision.Asset = asset
	return revision, nil
}

func getOwnedEditorImageAssetByID(
	ctx context.Context,
	queryer imageEditorQueryer,
	userID, assetID int64,
) (*service.ImageAsset, error) {
	asset, err := scanImageAsset(queryer.QueryRowContext(ctx, `
		SELECT id, public_id, owner_user_id, project_id, source_type, media_kind,
			file_name, object_key, thumbnail_object_key, mime_type, width, height,
			byte_size, sha256, duration_ms, origin_job_id, parent_asset_ids, created_at
		FROM image_assets
		WHERE id = $1 AND owner_user_id = $2`, assetID, userID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, service.ErrImageAssetNotFound
	}
	if err != nil {
		return nil, err
	}
	return asset, nil
}

func findOwnedEditorImageAssetID(
	ctx context.Context,
	queryer imageEditorQueryer,
	userID int64,
	publicID string,
) (int64, error) {
	var assetID int64
	err := queryer.QueryRowContext(ctx, `
		SELECT id
		FROM image_assets
		WHERE public_id = $1 AND owner_user_id = $2
			AND media_kind = 'image' AND deleted_at IS NULL`,
		strings.TrimSpace(publicID), userID,
	).Scan(&assetID)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, service.ErrImageAssetNotFound
	}
	if err != nil {
		return 0, fmt.Errorf("validate image editor asset: %w", err)
	}
	return assetID, nil
}

func replaceImageEditorAssetReferences(
	ctx context.Context,
	tx *sql.Tx,
	documentID, userID, baseAssetID, currentAssetID int64,
	references []service.ImageEditorAssetReference,
	replaceAll bool,
) error {
	if replaceAll {
		if _, err := tx.ExecContext(ctx, `
			DELETE FROM image_editor_asset_references WHERE document_id = $1`, documentID); err != nil {
			return fmt.Errorf("delete image editor asset references: %w", err)
		}
	} else {
		if _, err := tx.ExecContext(ctx, `
			DELETE FROM image_editor_asset_references
			WHERE document_id = $1 AND role IN ('source', 'result')`, documentID); err != nil {
			return fmt.Errorf("refresh image editor system references: %w", err)
		}
	}

	entries := make([]service.ImageEditorAssetReference, 0, len(references)+2)
	if replaceAll {
		entries = append(entries, references...)
	}
	entries = append(entries,
		service.ImageEditorAssetReference{AssetID: baseAssetID, Role: "source", ElementID: "base"},
		service.ImageEditorAssetReference{AssetID: currentAssetID, Role: "result", ElementID: "current"},
	)
	seen := make(map[string]struct{}, len(entries))
	for _, reference := range entries {
		role := strings.ToLower(strings.TrimSpace(reference.Role))
		elementID := strings.TrimSpace(reference.ElementID)
		if !validImageEditorReferenceRole(role) || elementID == "" || len(elementID) > 128 {
			return service.ErrImageEditorDocumentInvalid
		}
		assetID := reference.AssetID
		if assetID <= 0 {
			var err error
			assetID, err = findOwnedEditorImageAssetID(
				ctx, tx, userID, reference.AssetPublicID,
			)
			if err != nil {
				return err
			}
		}
		key := fmt.Sprintf("%d\x00%s\x00%s", assetID, role, elementID)
		if _, duplicate := seen[key]; duplicate {
			continue
		}
		seen[key] = struct{}{}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO image_editor_asset_references (
				document_id, asset_id, role, element_id
			) VALUES ($1, $2, $3, $4)`,
			documentID, assetID, role, elementID,
		); err != nil {
			return fmt.Errorf("insert image editor asset reference: %w", err)
		}
	}
	return nil
}

func validImageEditorReferenceRole(role string) bool {
	switch role {
	case "source", "layer", "mask", "result":
		return true
	default:
		return false
	}
}
