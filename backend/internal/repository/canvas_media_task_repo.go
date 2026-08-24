package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

type canvasMediaTaskRepository struct {
	db *sql.DB
}

var _ service.CanvasMediaTaskRepository = (*canvasMediaTaskRepository)(nil)

func NewCanvasMediaTaskRepository(db *sql.DB) service.CanvasMediaTaskRepository {
	return &canvasMediaTaskRepository{db: db}
}

const canvasMediaTaskSelect = `
	SELECT t.id, t.public_id, t.kind, t.status, t.phase, t.user_id, t.api_key_id,
		t.project_id, p.public_id, t.client_node_id, t.selected_model,
		COALESCE(t.successful_model, ''), t.prompt, t.request, t.request_hash,
		t.idempotency_key, COALESCE(t.upstream_request_id, ''), t.upstream_account_id,
		t.result_asset_id, COALESCE(a.public_id, ''), COALESCE(a.mime_type, ''),
		t.error, t.cancel_requested, t.attempt_count, t.next_attempt_at,
		COALESCE(t.lease_owner, ''), t.lease_expires_at, t.billing_type,
		t.billing_subscription_id, t.billing_recorded_at, t.created_at, t.updated_at,
		t.completed_at
	FROM canvas_media_tasks t
	JOIN image_canvas_projects p ON p.id = t.project_id
	LEFT JOIN image_assets a ON a.id = t.result_asset_id`

func (r *canvasMediaTaskRepository) Create(ctx context.Context, input service.CanvasMediaTaskInsert) (*service.CanvasMediaTask, bool, error) {
	if r == nil || r.db == nil {
		return nil, false, fmt.Errorf("canvas media task database is required")
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, false, fmt.Errorf("begin canvas media task create: %w", err)
	}
	defer tx.Rollback()
	var id int64
	err = tx.QueryRowContext(ctx, `
		INSERT INTO canvas_media_tasks (
			public_id, kind, status, phase, user_id, api_key_id, project_id,
			client_node_id, selected_model, prompt, request, request_hash,
			idempotency_key, billing_type, billing_subscription_id
		) VALUES ($1, $2, 'queued', 'preflight', $3, $4, $5, $6, $7, $8, $9::jsonb, $10, $11, $12, $13)
		ON CONFLICT (user_id, idempotency_key) DO NOTHING
		RETURNING id`,
		input.PublicID, input.Kind, input.UserID, input.APIKeyID, input.ProjectID,
		input.ClientNodeID, input.SelectedModel, input.Prompt, string(input.Request),
		input.RequestHash, input.IdempotencyKey, input.BillingType, nullableInt64Pointer(input.BillingSubscriptionID),
	).Scan(&id)
	created := true
	if errors.Is(err, sql.ErrNoRows) {
		created = false
		err = tx.QueryRowContext(ctx, `
			SELECT id FROM canvas_media_tasks WHERE user_id = $1 AND idempotency_key = $2`,
			input.UserID, input.IdempotencyKey).Scan(&id)
	}
	if err != nil {
		return nil, false, fmt.Errorf("create canvas media task: %w", err)
	}
	task, err := getCanvasMediaTaskByID(ctx, tx, id)
	if err != nil {
		return nil, false, err
	}
	if !created && task.RequestHash != input.RequestHash {
		return nil, false, service.ErrCanvasMediaTaskIdempotencyConflict
	}
	if err := tx.Commit(); err != nil {
		return nil, false, fmt.Errorf("commit canvas media task create: %w", err)
	}
	return task, created, nil
}

func (r *canvasMediaTaskRepository) GetOwned(ctx context.Context, userID int64, publicID string) (*service.CanvasMediaTask, error) {
	task, err := scanCanvasMediaTask(r.db.QueryRowContext(ctx,
		canvasMediaTaskSelect+` WHERE t.user_id = $1 AND t.public_id = $2`,
		userID, strings.TrimSpace(publicID)))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, service.ErrCanvasMediaTaskNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get canvas media task: %w", err)
	}
	return task, nil
}

func (r *canvasMediaTaskRepository) ListRecoverable(ctx context.Context, userID, projectID int64) ([]service.CanvasMediaTask, error) {
	if r == nil || r.db == nil {
		return nil, fmt.Errorf("canvas media task database is required")
	}
	rows, err := r.db.QueryContext(ctx, canvasMediaTaskSelect+`
		WHERE t.user_id = $1 AND t.project_id = $2
			AND (t.status IN ('queued', 'running') OR t.updated_at > p.updated_at)
		ORDER BY t.created_at, t.id
		LIMIT 100`, userID, projectID)
	if err != nil {
		return nil, fmt.Errorf("list recoverable canvas media tasks: %w", err)
	}
	defer rows.Close()
	tasks := make([]service.CanvasMediaTask, 0)
	for rows.Next() {
		task, err := scanCanvasMediaTask(rows)
		if err != nil {
			return nil, fmt.Errorf("scan recoverable canvas media task: %w", err)
		}
		tasks = append(tasks, *task)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate recoverable canvas media tasks: %w", err)
	}
	return tasks, nil
}

func (r *canvasMediaTaskRepository) ClaimNextVideo(ctx context.Context, owner string, leaseUntil time.Time) (*service.CanvasMediaTask, error) {
	return r.claimNext(ctx, service.CanvasMediaKindVideo, owner, leaseUntil)
}

func (r *canvasMediaTaskRepository) ClaimNextAudio(ctx context.Context, owner string, leaseUntil time.Time) (*service.CanvasMediaTask, error) {
	return r.claimNext(ctx, service.CanvasMediaKindAudio, owner, leaseUntil)
}

func (r *canvasMediaTaskRepository) claimNext(
	ctx context.Context,
	kind service.CanvasMediaKind,
	owner string,
	leaseUntil time.Time,
) (*service.CanvasMediaTask, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var id int64
	err = tx.QueryRowContext(ctx, `
		SELECT id FROM canvas_media_tasks
		WHERE kind = '`+string(kind)+`' AND status IN ('queued', 'running')
			AND cancel_requested = FALSE AND next_attempt_at <= NOW()
			AND (lease_expires_at IS NULL OR lease_expires_at < NOW())
		ORDER BY next_attempt_at, created_at, id
		FOR UPDATE SKIP LOCKED LIMIT 1`).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("claim canvas media task: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE canvas_media_tasks
		SET status = 'running',
			phase = CASE WHEN upstream_request_id IS NULL OR upstream_request_id = '' THEN 'submitting' ELSE 'polling' END,
			attempt_count = attempt_count + 1, lease_owner = $2, lease_expires_at = $3, updated_at = NOW()
		WHERE id = $1`, id, owner, leaseUntil); err != nil {
		return nil, fmt.Errorf("lease canvas media task: %w", err)
	}
	task, err := getCanvasMediaTaskByID(ctx, tx, id)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return task, nil
}

func (r *canvasMediaTaskRepository) StartOwned(ctx context.Context, id, userID int64, owner string, leaseUntil time.Time) (*service.CanvasMediaTask, error) {
	result, err := r.db.ExecContext(ctx, `
		UPDATE canvas_media_tasks SET status = 'running', phase = 'submitting',
			attempt_count = attempt_count + 1, lease_owner = $3, lease_expires_at = $4, updated_at = NOW()
		WHERE id = $1 AND user_id = $2 AND status = 'queued' AND cancel_requested = FALSE`,
		id, userID, owner, leaseUntil)
	if err != nil {
		return nil, fmt.Errorf("start canvas media task: %w", err)
	}
	affected, _ := result.RowsAffected()
	if affected != 1 {
		return nil, service.ErrCanvasMediaTaskConflict
	}
	return getCanvasMediaTaskByID(ctx, r.db, id)
}

func (r *canvasMediaTaskRepository) RenewLease(ctx context.Context, id int64, owner string, leaseUntil time.Time) error {
	result, err := r.db.ExecContext(ctx, `
		UPDATE canvas_media_tasks SET lease_expires_at = $3, updated_at = NOW()
		WHERE id = $1 AND lease_owner = $2 AND status = 'running' AND cancel_requested = FALSE`,
		id, owner, leaseUntil)
	if err != nil {
		return fmt.Errorf("renew canvas media task lease: %w", err)
	}
	affected, _ := result.RowsAffected()
	if affected != 1 {
		return service.ErrCanvasMediaTaskConflict
	}
	return nil
}

func (r *canvasMediaTaskRepository) MarkSubmitted(ctx context.Context, id int64, owner, requestID string, accountID int64, next time.Time) error {
	return r.updateLeased(ctx, id, owner, `
		phase = 'polling', upstream_request_id = $3, upstream_account_id = $4,
		next_attempt_at = $5, lease_owner = NULL, lease_expires_at = NULL`,
		strings.TrimSpace(requestID), accountID, next)
}

func (r *canvasMediaTaskRepository) ReschedulePoll(ctx context.Context, id int64, owner string, next time.Time) error {
	return r.updateLeased(ctx, id, owner, `
		phase = 'polling', next_attempt_at = $3, lease_owner = NULL, lease_expires_at = NULL`, next)
}

func (r *canvasMediaTaskRepository) RetryQueued(ctx context.Context, id int64, owner string, next time.Time, taskError *service.CanvasMediaTaskError) error {
	raw, _ := json.Marshal(taskError)
	return r.updateLeased(ctx, id, owner, `
		status = 'queued', phase = 'preflight', attempt_count = 0, next_attempt_at = $3, error = $4::jsonb,
		lease_owner = NULL, lease_expires_at = NULL`, next, nullableJSON(raw))
}

func (r *canvasMediaTaskRepository) MarkBillingRecorded(ctx context.Context, id int64) error {
	_, err := r.db.ExecContext(ctx, `
		UPDATE canvas_media_tasks SET billing_recorded_at = COALESCE(billing_recorded_at, NOW()), updated_at = NOW()
		WHERE id = $1`, id)
	return err
}

func (r *canvasMediaTaskRepository) Complete(ctx context.Context, id int64, owner string, assetID int64, successfulModel string) error {
	return r.updateLeased(ctx, id, owner, `
		status = 'completed', phase = 'completed', successful_model = $3,
		result_asset_id = $4, error = NULL, completed_at = NOW(),
		lease_owner = NULL, lease_expires_at = NULL`, strings.TrimSpace(successfulModel), assetID)
}

func (r *canvasMediaTaskRepository) Fail(ctx context.Context, id int64, owner string, status service.ImageJobStatus, taskError service.CanvasMediaTaskError) error {
	if status != service.ImageJobStatusFailed && status != service.ImageJobStatusIndeterminate {
		status = service.ImageJobStatusFailed
	}
	raw, _ := json.Marshal(taskError)
	return r.updateLeased(ctx, id, owner, `
		status = $3, phase = 'failed', error = $4::jsonb, completed_at = NOW(),
		lease_owner = NULL, lease_expires_at = NULL`, status, string(raw))
}

func (r *canvasMediaTaskRepository) CancelOwned(ctx context.Context, userID int64, publicID string) (*service.CanvasMediaTask, error) {
	result, err := r.db.ExecContext(ctx, `
		UPDATE canvas_media_tasks SET status = 'canceled', phase = 'canceled',
			cancel_requested = TRUE, completed_at = NOW(), updated_at = NOW(),
			lease_owner = NULL, lease_expires_at = NULL
		WHERE user_id = $1 AND public_id = $2 AND status IN ('queued', 'running')`,
		userID, strings.TrimSpace(publicID))
	if err != nil {
		return nil, err
	}
	affected, _ := result.RowsAffected()
	task, getErr := r.GetOwned(ctx, userID, publicID)
	if getErr != nil {
		return nil, getErr
	}
	if affected == 0 && !task.Terminal() {
		return nil, service.ErrCanvasMediaTaskConflict
	}
	return task, nil
}

func (r *canvasMediaTaskRepository) updateLeased(ctx context.Context, id int64, owner, setClause string, args ...any) error {
	query := `UPDATE canvas_media_tasks SET ` + setClause + `, updated_at = NOW()
		WHERE id = $1 AND lease_owner = $2 AND status = 'running'`
	values := []any{id, owner}
	values = append(values, args...)
	result, err := r.db.ExecContext(ctx, query, values...)
	if err != nil {
		return fmt.Errorf("update canvas media task: %w", err)
	}
	affected, _ := result.RowsAffected()
	if affected != 1 {
		return service.ErrCanvasMediaTaskConflict
	}
	return nil
}

type canvasMediaTaskQuerier interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func getCanvasMediaTaskByID(ctx context.Context, query canvasMediaTaskQuerier, id int64) (*service.CanvasMediaTask, error) {
	task, err := scanCanvasMediaTask(query.QueryRowContext(ctx, canvasMediaTaskSelect+` WHERE t.id = $1`, id))
	if err != nil {
		return nil, fmt.Errorf("load canvas media task: %w", err)
	}
	return task, nil
}

func scanCanvasMediaTask(scanner imageCanvasScanner) (*service.CanvasMediaTask, error) {
	task := &service.CanvasMediaTask{}
	var kind, status string
	var request, rawError []byte
	var upstreamAccountID, resultAssetID, billingSubscriptionID sql.NullInt64
	var leaseExpiresAt, billingRecordedAt, completedAt sql.NullTime
	if err := scanner.Scan(
		&task.ID, &task.PublicID, &kind, &status, &task.Phase, &task.UserID, &task.APIKeyID,
		&task.ProjectID, &task.ProjectPublicID, &task.ClientNodeID, &task.SelectedModel,
		&task.SuccessfulModel, &task.Prompt, &request, &task.RequestHash, &task.IdempotencyKey,
		&task.UpstreamRequestID, &upstreamAccountID, &resultAssetID, &task.ResultAssetPublicID,
		&task.ResultMIMEType, &rawError, &task.CancelRequested, &task.AttemptCount,
		&task.NextAttemptAt, &task.LeaseOwner, &leaseExpiresAt, &task.BillingType,
		&billingSubscriptionID, &billingRecordedAt, &task.CreatedAt, &task.UpdatedAt, &completedAt,
	); err != nil {
		return nil, err
	}
	task.Kind = service.CanvasMediaKind(kind)
	task.Status = service.ImageJobStatus(status)
	task.Request = append(json.RawMessage(nil), request...)
	task.UpstreamAccountID = nullInt64Pointer(upstreamAccountID)
	task.ResultAssetID = nullInt64Pointer(resultAssetID)
	task.BillingSubscriptionID = nullInt64Pointer(billingSubscriptionID)
	task.LeaseExpiresAt = nullTimePointer(leaseExpiresAt)
	task.BillingRecordedAt = nullTimePointer(billingRecordedAt)
	task.CompletedAt = nullTimePointer(completedAt)
	if len(rawError) > 0 && string(rawError) != "null" {
		var taskError service.CanvasMediaTaskError
		if json.Unmarshal(rawError, &taskError) == nil {
			task.Error = &taskError
		}
	}
	return task, nil
}

func nullableJSON(raw []byte) any {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	return string(raw)
}

func nullTimePointer(value sql.NullTime) *time.Time {
	if !value.Valid {
		return nil
	}
	result := value.Time
	return &result
}
