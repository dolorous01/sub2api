package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

type imageModelPolicyRepository struct {
	db *sql.DB
}

var _ service.ImageModelPolicyRepository = (*imageModelPolicyRepository)(nil)

func NewImageModelPolicyRepository(db *sql.DB) service.ImageModelPolicyRepository {
	return &imageModelPolicyRepository{db: db}
}

func (r *imageModelPolicyRepository) Get(ctx context.Context) (*service.ImageModelPolicy, error) {
	if r == nil || r.db == nil {
		return nil, fmt.Errorf("image model policy database is required")
	}
	return loadImageModelPolicy(ctx, r.db)
}

func (r *imageModelPolicyRepository) Replace(
	ctx context.Context,
	expectedVersion, operatorUserID int64,
	enabled bool,
	items []service.ImageModelPolicyItem,
) (*service.ImageModelPolicy, error) {
	if r == nil || r.db == nil {
		return nil, fmt.Errorf("image model policy database is required")
	}
	if err := service.ValidateImageModelPolicyItems(items); err != nil {
		return nil, err
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin image model policy replace: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var current service.ImageModelPolicy
	if err := tx.QueryRowContext(ctx, `
		SELECT version, enabled
		FROM image_model_policies
		WHERE id = 1
		FOR UPDATE`).Scan(&current.Version, &current.Enabled); err != nil {
		return nil, fmt.Errorf("lock image model policy: %w", err)
	}
	if current.Version != expectedVersion {
		return nil, service.ErrImageModelPolicyVersionConflict
	}
	current.Items, err = loadImageModelPolicyItems(ctx, tx)
	if err != nil {
		return nil, err
	}
	beforeJSON, err := json.Marshal(current)
	if err != nil {
		return nil, fmt.Errorf("marshal image model policy before value: %w", err)
	}

	if _, err := tx.ExecContext(ctx, `DELETE FROM image_model_policy_items WHERE policy_id = 1`); err != nil {
		return nil, fmt.Errorf("delete image model policy items: %w", err)
	}
	for _, item := range items {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO image_model_policy_items (policy_id, model, enabled, position)
			VALUES (1, $1, $2, $3)`, item.Model, item.Enabled, item.Position); err != nil {
			return nil, fmt.Errorf("insert image model policy item %q: %w", item.Model, err)
		}
	}

	next := &service.ImageModelPolicy{
		Version: current.Version + 1,
		Enabled: enabled,
		Items:   append([]service.ImageModelPolicyItem(nil), items...),
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE image_model_policies
		SET version = $1, enabled = $2, updated_at = NOW()
		WHERE id = 1`, next.Version, next.Enabled); err != nil {
		return nil, fmt.Errorf("update image model policy: %w", err)
	}
	afterJSON, err := json.Marshal(next)
	if err != nil {
		return nil, fmt.Errorf("marshal image model policy after value: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO image_model_policy_audits
			(operator_user_id, old_version, new_version, before_value, after_value)
		VALUES ($1, $2, $3, $4::jsonb, $5::jsonb)`,
		operatorUserID, current.Version, next.Version, string(beforeJSON), string(afterJSON)); err != nil {
		return nil, fmt.Errorf("insert image model policy audit: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit image model policy replace: %w", err)
	}
	return next, nil
}

func (r *imageModelPolicyRepository) ListAudit(ctx context.Context, limit int) ([]service.ImageModelPolicyAudit, error) {
	if r == nil || r.db == nil {
		return nil, fmt.Errorf("image model policy database is required")
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, operator_user_id, old_version, new_version, before_value, after_value, created_at
		FROM image_model_policy_audits
		ORDER BY id DESC
		LIMIT $1`, limit)
	if err != nil {
		return nil, fmt.Errorf("list image model policy audits: %w", err)
	}
	defer func() { _ = rows.Close() }()

	audits := make([]service.ImageModelPolicyAudit, 0)
	for rows.Next() {
		var audit service.ImageModelPolicyAudit
		var beforeValue, afterValue []byte
		if err := rows.Scan(
			&audit.ID,
			&audit.OperatorUserID,
			&audit.OldVersion,
			&audit.NewVersion,
			&beforeValue,
			&afterValue,
			&audit.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan image model policy audit: %w", err)
		}
		audit.BeforeValue = append(json.RawMessage(nil), beforeValue...)
		audit.AfterValue = append(json.RawMessage(nil), afterValue...)
		audits = append(audits, audit)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate image model policy audits: %w", err)
	}
	return audits, nil
}

type imageModelPolicyQueryer interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

func loadImageModelPolicy(ctx context.Context, queryer imageModelPolicyQueryer) (*service.ImageModelPolicy, error) {
	policy := &service.ImageModelPolicy{}
	if err := queryer.QueryRowContext(ctx, `
		SELECT version, enabled
		FROM image_model_policies
		WHERE id = 1`).Scan(&policy.Version, &policy.Enabled); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("image model policy singleton is missing")
		}
		return nil, fmt.Errorf("load image model policy: %w", err)
	}
	items, err := loadImageModelPolicyItems(ctx, queryer)
	if err != nil {
		return nil, err
	}
	policy.Items = items
	return policy, nil
}

func loadImageModelPolicyItems(ctx context.Context, queryer imageModelPolicyQueryer) ([]service.ImageModelPolicyItem, error) {
	rows, err := queryer.QueryContext(ctx, `
		SELECT model, enabled, position
		FROM image_model_policy_items
		WHERE policy_id = 1
		ORDER BY position ASC`)
	if err != nil {
		return nil, fmt.Errorf("load image model policy items: %w", err)
	}
	defer func() { _ = rows.Close() }()

	items := make([]service.ImageModelPolicyItem, 0)
	for rows.Next() {
		var item service.ImageModelPolicyItem
		if err := rows.Scan(&item.Model, &item.Enabled, &item.Position); err != nil {
			return nil, fmt.Errorf("scan image model policy item: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate image model policy items: %w", err)
	}
	return items, nil
}
