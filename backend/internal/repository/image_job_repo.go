package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

const imageJobColumns = `
	id, public_id, user_id, api_key_id, group_id, endpoint, operation, mode,
	requested_model, mapped_model, status, requested_count, completed_count,
	request, request_digest, idempotency_key_hash, reserved_usd,
	reservation_billing_type, reservation_subscription_id, reservation_status,
	usage, settlement_status, attempt_id, worker_id, execution_phase,
	heartbeat_at, cancel_requested_at, canceled_at,
	error_type, error_code, error_message, error_retryable,
	started_at, finished_at, expires_at, created_at, updated_at,
	project_id, client_node_id, selected_model, policy_version,
	attempt_plan, successful_model, attempt_log`

type imageJobRepository struct {
	client *dbent.Client
	db     *sql.DB
}

var _ service.ImageJobRepository = (*imageJobRepository)(nil)

func NewImageJobRepository(client *dbent.Client, db *sql.DB) service.ImageJobRepository {
	return &imageJobRepository{client: client, db: db}
}

func (r *imageJobRepository) CreateReserved(ctx context.Context, create *service.ImageJobCreate) (bool, *service.ImageJob, error) {
	if r == nil || r.db == nil {
		return false, nil, fmt.Errorf("image job repository database is required")
	}
	if create == nil {
		return false, nil, fmt.Errorf("image job create is required")
	}
	requestJSON, err := json.Marshal(create.Request)
	if err != nil {
		return false, nil, fmt.Errorf("marshal image job request: %w", err)
	}
	canvasProjectID, clientNodeID, selectedModel, policyVersion := any(nil), any(nil), any(nil), any(nil)
	attemptPlan := []string{}
	if create.Canvas != nil {
		canvasProjectID = nullableInt64Pointer(create.Canvas.ProjectID)
		clientNodeID = nullableString(create.Canvas.ClientNodeID)
		selectedModel = nullableString(create.Canvas.SelectedModel)
		policyVersion = create.Canvas.PolicyVersion
		attemptPlan = append(attemptPlan, create.Canvas.AttemptPlan...)
	}
	attemptPlanJSON, err := json.Marshal(attemptPlan)
	if err != nil {
		return false, nil, fmt.Errorf("marshal image job attempt plan: %w", err)
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return false, nil, fmt.Errorf("begin image job create: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if create.IdempotencyKeyHash != nil {
		job, err := scanImageJob(tx.QueryRowContext(ctx, `
			SELECT `+imageJobColumns+`
			FROM image_jobs
			WHERE api_key_id = $1 AND idempotency_key_hash = $2
			FOR SHARE`, create.APIKeyID, *create.IdempotencyKeyHash))
		if err == nil {
			if job.RequestDigest != create.RequestDigest {
				return false, nil, service.ErrImageJobIdempotencyConflict
			}
			if err := loadImageJobRelations(ctx, tx, job); err != nil {
				return false, nil, err
			}
			if err := tx.Commit(); err != nil {
				return false, nil, fmt.Errorf("commit image job replay: %w", err)
			}
			return false, job, nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return false, nil, fmt.Errorf("load idempotent image job before reservation: %w", err)
		}
	}
	if create.MaxActiveJobsPerUser > 0 {
		// Serialize admission for one user so concurrent submissions cannot race
		// past the configured active-job limit.
		if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock($1)`, create.UserID); err != nil {
			return false, nil, fmt.Errorf("lock image job user admission: %w", err)
		}
		var active int
		if err := tx.QueryRowContext(ctx, `
			SELECT COUNT(*)
			FROM image_jobs
			WHERE user_id = $1 AND status IN ('queued', 'running')`, create.UserID).Scan(&active); err != nil {
			return false, nil, fmt.Errorf("count active image jobs: %w", err)
		}
		if active >= create.MaxActiveJobsPerUser {
			return false, nil, service.ErrImageJobQueueFull
		}
	}
	if err := validateImageJobReservation(ctx, tx, create); err != nil {
		return false, nil, err
	}

	row := tx.QueryRowContext(ctx, `
		INSERT INTO image_jobs (
			public_id, user_id, api_key_id, group_id, endpoint, operation, mode,
			requested_model, mapped_model, status, requested_count, completed_count,
			request, request_digest, idempotency_key_hash, reserved_usd,
			reservation_billing_type, reservation_subscription_id,
			reservation_status, settlement_status, execution_phase, expires_at,
			project_id, client_node_id, selected_model, policy_version, attempt_plan
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7,
			$8, $9, 'queued', $10, 0,
			$11::jsonb, $12, $13, $14,
			$15, $16, 'held', 'pending', 'preflight', $17,
			$18, $19, $20, $21, $22::jsonb
		)
		ON CONFLICT (api_key_id, idempotency_key_hash)
			WHERE idempotency_key_hash IS NOT NULL
			DO NOTHING
		RETURNING `+imageJobColumns,
		create.PublicID,
		create.UserID,
		create.APIKeyID,
		create.GroupID,
		create.Endpoint,
		create.Operation,
		create.Mode,
		create.RequestedModel,
		create.MappedModel,
		create.RequestedCount,
		string(requestJSON),
		create.RequestDigest,
		nullableStringPointer(create.IdempotencyKeyHash),
		create.ReservedUSD,
		int(create.ReservationBillingType),
		nullableInt64Pointer(create.ReservationSubscriptionID),
		create.ExpiresAt,
		canvasProjectID,
		clientNodeID,
		selectedModel,
		policyVersion,
		string(attemptPlanJSON),
	)
	job, err := scanImageJob(row)
	if errors.Is(err, sql.ErrNoRows) {
		if create.IdempotencyKeyHash == nil {
			return false, nil, fmt.Errorf("create image job returned no row")
		}
		job, err = scanImageJob(tx.QueryRowContext(ctx, `
			SELECT `+imageJobColumns+`
			FROM image_jobs
			WHERE api_key_id = $1 AND idempotency_key_hash = $2
			FOR SHARE`, create.APIKeyID, *create.IdempotencyKeyHash))
		if err != nil {
			return false, nil, fmt.Errorf("load idempotent image job: %w", err)
		}
		if job.RequestDigest != create.RequestDigest {
			return false, nil, service.ErrImageJobIdempotencyConflict
		}
		if err := loadImageJobRelations(ctx, tx, job); err != nil {
			return false, nil, err
		}
		if err := tx.Commit(); err != nil {
			return false, nil, fmt.Errorf("commit image job replay: %w", err)
		}
		return false, job, nil
	}
	if err != nil {
		return false, nil, fmt.Errorf("insert image job: %w", err)
	}

	for _, input := range create.Inputs {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO image_job_inputs
				(job_id, index, kind, object_key, mime_type, byte_size, sha256)
			VALUES ($1, $2, $3, $4, $5, $6, $7)`,
			job.ID, input.Index, input.Kind, input.ObjectKey, input.MIMEType, input.ByteSize, input.SHA256,
		); err != nil {
			return false, nil, fmt.Errorf("insert image job input: %w", err)
		}
	}
	if err := loadImageJobRelations(ctx, tx, job); err != nil {
		return false, nil, err
	}
	if err := tx.Commit(); err != nil {
		return false, nil, fmt.Errorf("commit image job create: %w", err)
	}
	return true, job, nil
}

func validateImageJobReservation(ctx context.Context, tx *sql.Tx, create *service.ImageJobCreate) error {
	if create.ReservedUSD <= 0 {
		return fmt.Errorf("%w: amount must be greater than zero", service.ErrImageJobReservationUnavailable)
	}
	switch create.ReservationBillingType {
	case service.BillingTypeBalance:
		var balance float64
		err := tx.QueryRowContext(ctx, `
			SELECT balance
			FROM users
			WHERE id = $1 AND deleted_at IS NULL
			FOR UPDATE`, create.UserID).Scan(&balance)
		if errors.Is(err, sql.ErrNoRows) {
			return service.ErrImageJobReservationInsufficient
		}
		if err != nil {
			return fmt.Errorf("lock image job reservation balance: %w", err)
		}
		var held float64
		if err := tx.QueryRowContext(ctx, `
			SELECT COALESCE(SUM(reserved_usd), 0)
			FROM image_jobs
			WHERE user_id = $1 AND reservation_billing_type = $2
				AND reservation_status = 'held'
				AND ($4::varchar IS NULL OR api_key_id <> $3
					OR idempotency_key_hash IS DISTINCT FROM $4)`,
			create.UserID, service.BillingTypeBalance, create.APIKeyID,
			nullableStringPointer(create.IdempotencyKeyHash),
		).Scan(&held); err != nil {
			return fmt.Errorf("sum held image job balance reservations: %w", err)
		}
		if balance+1e-9 < held+create.ReservedUSD {
			return service.ErrImageJobReservationInsufficient
		}
	case service.BillingTypeSubscription:
		if create.ReservationSubscriptionID == nil {
			return fmt.Errorf("%w: subscription ID is required", service.ErrImageJobReservationUnavailable)
		}
		var dailyUsage, weeklyUsage, monthlyUsage float64
		var dailyLimit, weeklyLimit, monthlyLimit sql.NullFloat64
		err := tx.QueryRowContext(ctx, `
			SELECT us.daily_usage_usd, us.weekly_usage_usd, us.monthly_usage_usd,
				g.daily_limit_usd, g.weekly_limit_usd, g.monthly_limit_usd
			FROM user_subscriptions us
			JOIN groups g ON g.id = us.group_id
			WHERE us.id = $1 AND us.user_id = $2 AND us.group_id = $3
				AND us.status = $4 AND us.deleted_at IS NULL
				AND us.expires_at > NOW() AND g.deleted_at IS NULL
			FOR UPDATE OF us, g`,
			*create.ReservationSubscriptionID, create.UserID, create.GroupID, service.SubscriptionStatusActive,
		).Scan(&dailyUsage, &weeklyUsage, &monthlyUsage, &dailyLimit, &weeklyLimit, &monthlyLimit)
		if errors.Is(err, sql.ErrNoRows) {
			return service.ErrImageJobReservationInsufficient
		}
		if err != nil {
			return fmt.Errorf("lock image job subscription reservation: %w", err)
		}
		var held float64
		if err := tx.QueryRowContext(ctx, `
			SELECT COALESCE(SUM(reserved_usd), 0)
			FROM image_jobs
			WHERE reservation_subscription_id = $1
				AND reservation_billing_type = $2
				AND reservation_status = 'held'
				AND ($4::varchar IS NULL OR api_key_id <> $3
					OR idempotency_key_hash IS DISTINCT FROM $4)`,
			*create.ReservationSubscriptionID, service.BillingTypeSubscription,
			create.APIKeyID, nullableStringPointer(create.IdempotencyKeyHash),
		).Scan(&held); err != nil {
			return fmt.Errorf("sum held image job subscription reservations: %w", err)
		}
		requested := held + create.ReservedUSD
		if exceedsImageJobLimit(dailyUsage, requested, dailyLimit) ||
			exceedsImageJobLimit(weeklyUsage, requested, weeklyLimit) ||
			exceedsImageJobLimit(monthlyUsage, requested, monthlyLimit) {
			return service.ErrImageJobReservationInsufficient
		}
	default:
		return fmt.Errorf("%w: unsupported billing type %d", service.ErrImageJobReservationUnavailable, create.ReservationBillingType)
	}

	var quota, quotaUsed float64
	err := tx.QueryRowContext(ctx, `
		SELECT quota, quota_used
		FROM api_keys
		WHERE id = $1 AND user_id = $2 AND deleted_at IS NULL
		FOR UPDATE`, create.APIKeyID, create.UserID).Scan(&quota, &quotaUsed)
	if errors.Is(err, sql.ErrNoRows) {
		return service.ErrImageJobReservationInsufficient
	}
	if err != nil {
		return fmt.Errorf("lock image job API key reservation: %w", err)
	}
	if quota > 0 {
		var held float64
		if err := tx.QueryRowContext(ctx, `
			SELECT COALESCE(SUM(reserved_usd), 0)
			FROM image_jobs
			WHERE api_key_id = $1 AND reservation_status = 'held'
				AND ($2::varchar IS NULL OR idempotency_key_hash IS DISTINCT FROM $2)`,
			create.APIKeyID, nullableStringPointer(create.IdempotencyKeyHash),
		).Scan(&held); err != nil {
			return fmt.Errorf("sum held image job API key reservations: %w", err)
		}
		if quota+1e-9 < quotaUsed+held+create.ReservedUSD {
			return service.ErrImageJobReservationInsufficient
		}
	}
	return nil
}

func exceedsImageJobLimit(usage, requested float64, limit sql.NullFloat64) bool {
	return limit.Valid && limit.Float64 > 0 && usage+requested > limit.Float64+1e-9
}

func (r *imageJobRepository) GetOwned(ctx context.Context, publicID string, apiKeyID int64) (*service.ImageJob, error) {
	return r.get(ctx, publicID, &apiKeyID)
}

func (r *imageJobRepository) GetAdmin(ctx context.Context, publicID string) (*service.ImageJob, error) {
	return r.get(ctx, publicID, nil)
}

// ListRecentAdmin is intentionally bounded and ordered by creation time. The
// attempt plan/log are persisted on image_jobs, so the admin console can
// explain fallback decisions without replaying or probing an upstream
// provider. Inputs and results are intentionally not hydrated here because
// this endpoint does not expose them and doing so would add two queries per
// row.
func (r *imageJobRepository) ListRecentAdmin(ctx context.Context, limit int) ([]*service.ImageJob, error) {
	if r == nil || r.db == nil {
		return nil, fmt.Errorf("image job repository database is required")
	}
	if limit < 1 {
		limit = 1
	}
	if limit > 100 {
		limit = 100
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT `+imageJobColumns+`
		FROM image_jobs
		ORDER BY created_at DESC, id DESC
		LIMIT $1`, limit)
	if err != nil {
		return nil, fmt.Errorf("list recent admin image jobs: %w", err)
	}
	defer func() { _ = rows.Close() }()
	jobs := make([]*service.ImageJob, 0, limit)
	for rows.Next() {
		job, scanErr := scanImageJob(rows)
		if scanErr != nil {
			return nil, fmt.Errorf("scan recent admin image job: %w", scanErr)
		}
		jobs = append(jobs, job)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate recent admin image jobs: %w", err)
	}
	return jobs, nil
}

func (r *imageJobRepository) get(ctx context.Context, publicID string, apiKeyID *int64) (*service.ImageJob, error) {
	query := `SELECT ` + imageJobColumns + ` FROM image_jobs WHERE public_id = $1`
	args := []any{publicID}
	if apiKeyID != nil {
		query += ` AND api_key_id = $2`
		args = append(args, *apiKeyID)
	}
	job, err := scanImageJob(r.db.QueryRowContext(ctx, query, args...))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, service.ErrImageJobNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get image job: %w", err)
	}
	if err := loadImageJobRelations(ctx, r.db, job); err != nil {
		return nil, err
	}
	return job, nil
}

func (r *imageJobRepository) ClaimNext(ctx context.Context, workerID, attemptID string) (*service.ImageJobClaim, error) {
	if workerID == "" || attemptID == "" {
		return nil, fmt.Errorf("image job worker and attempt IDs are required")
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin image job claim: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	job, err := scanImageJob(tx.QueryRowContext(ctx, `
		WITH next AS (
			SELECT id FROM image_jobs
			WHERE status = 'queued'
			ORDER BY created_at, id
			LIMIT 1
			FOR UPDATE SKIP LOCKED
		)
		UPDATE image_jobs j
		SET status = 'running', worker_id = $1, attempt_id = $2,
			execution_phase = 'preflight', heartbeat_at = NOW(),
			started_at = COALESCE(started_at, NOW()), updated_at = NOW()
		FROM next WHERE j.id = next.id
		RETURNING `+prefixedImageJobColumns("j"), workerID, attemptID))
	if errors.Is(err, sql.ErrNoRows) {
		if err := tx.Commit(); err != nil {
			return nil, fmt.Errorf("commit empty image job claim: %w", err)
		}
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("claim image job: %w", err)
	}
	if err := loadImageJobRelations(ctx, tx, job); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit image job claim: %w", err)
	}
	return &service.ImageJobClaim{Job: job, AttemptID: attemptID, WorkerID: workerID}, nil
}

func (r *imageJobRepository) MarkUpstreamStarted(ctx context.Context, jobID int64, attemptID, mappedModel string) error {
	result, err := r.db.ExecContext(ctx, `
		UPDATE image_jobs
		SET execution_phase = 'upstream', mapped_model = $3,
			heartbeat_at = NOW(), updated_at = NOW()
		WHERE id = $1 AND attempt_id = $2 AND status = 'running'`, jobID, attemptID, mappedModel)
	return imageJobCASResult(result, err, "mark image job upstream started")
}

func (r *imageJobRepository) SetExecutionPhase(ctx context.Context, jobID int64, attemptID, phase string) error {
	result, err := r.db.ExecContext(ctx, `
		UPDATE image_jobs
		SET execution_phase = $3, heartbeat_at = NOW(), updated_at = NOW()
		WHERE id = $1 AND attempt_id = $2 AND status = 'running'`, jobID, attemptID, strings.TrimSpace(phase))
	return imageJobCASResult(result, err, "set image job execution phase")
}

func (r *imageJobRepository) RecordImageModelAttempt(
	ctx context.Context,
	jobID int64,
	attemptID string,
	attempt service.ImageJobAttempt,
	successfulModel string,
) error {
	encoded, err := json.Marshal([]service.ImageJobAttempt{attempt})
	if err != nil {
		return fmt.Errorf("marshal image model attempt: %w", err)
	}
	result, err := r.db.ExecContext(ctx, `
		UPDATE image_jobs
		SET attempt_log = attempt_log || $3::jsonb,
			successful_model = COALESCE(NULLIF($4, ''), successful_model),
			heartbeat_at = NOW(), updated_at = NOW()
		WHERE id = $1 AND attempt_id = $2 AND status = 'running'`,
		jobID, attemptID, string(encoded), strings.TrimSpace(successfulModel))
	return imageJobCASResult(result, err, "record image model attempt")
}

func (r *imageJobRepository) Heartbeat(ctx context.Context, jobID int64, attemptID string, at time.Time) error {
	result, err := r.db.ExecContext(ctx, `
		UPDATE image_jobs
		SET heartbeat_at = $3, updated_at = $3
		WHERE id = $1 AND attempt_id = $2 AND status = 'running'`, jobID, attemptID, at)
	return imageJobCASResult(result, err, "heartbeat image job")
}

func (r *imageJobRepository) UpsertResult(ctx context.Context, jobID int64, attemptID string, result *service.ImageJobResult) (bool, error) {
	if result == nil {
		return false, fmt.Errorf("image job result is required")
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return false, fmt.Errorf("begin image job result upsert: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var currentStatus string
	var projectID sql.NullInt64
	var ownerUserID int64
	if err := tx.QueryRowContext(ctx, `
		SELECT status, project_id, user_id FROM image_jobs
		WHERE id = $1 AND attempt_id = $2 AND status = 'running'
		FOR UPDATE`, jobID, attemptID).Scan(&currentStatus, &projectID, &ownerUserID); errors.Is(err, sql.ErrNoRows) {
		return false, service.ErrImageJobAttemptMismatch
	} else if err != nil {
		return false, fmt.Errorf("lock image job for result: %w", err)
	}

	var previousStatus string
	err = tx.QueryRowContext(ctx, `
		SELECT status FROM image_job_results
		WHERE job_id = $1 AND index = $2
		FOR UPDATE`, jobID, result.Index).Scan(&previousStatus)
	inserted := errors.Is(err, sql.ErrNoRows)
	if err != nil && !inserted {
		return false, fmt.Errorf("load existing image job result: %w", err)
	}
	if inserted {
		_, err = tx.ExecContext(ctx, `
			INSERT INTO image_job_results (
				job_id, index, status, object_key, mime_type, byte_size,
				width, height, size_tier, revised_prompt, upstream_output_id
			) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)`,
			jobID, result.Index, result.Status, nullableString(result.ObjectKey),
			nullableString(result.MIMEType), nullablePositiveInt64(result.ByteSize),
			nullablePositiveInt(result.Width), nullablePositiveInt(result.Height),
			nullableString(result.SizeTier), nullableString(result.RevisedPrompt),
			nullableString(result.UpstreamOutputID),
		)
	} else {
		_, err = tx.ExecContext(ctx, `
			UPDATE image_job_results
			SET status = $3, object_key = $4, mime_type = $5, byte_size = $6,
				width = $7, height = $8, size_tier = $9, revised_prompt = $10,
				upstream_output_id = $11, updated_at = NOW()
			WHERE job_id = $1 AND index = $2`,
			jobID, result.Index, result.Status, nullableString(result.ObjectKey),
			nullableString(result.MIMEType), nullablePositiveInt64(result.ByteSize),
			nullablePositiveInt(result.Width), nullablePositiveInt(result.Height),
			nullableString(result.SizeTier), nullableString(result.RevisedPrompt),
			nullableString(result.UpstreamOutputID),
		)
	}
	if err != nil {
		return false, fmt.Errorf("upsert image job result: %w", err)
	}
	if result.Status == "completed" && projectID.Valid && result.Width > 0 && result.Height > 0 && result.ByteSize > 0 && strings.TrimSpace(result.ObjectKey) != "" && strings.TrimSpace(result.SHA256) != "" {
		var assetID int64
		err := tx.QueryRowContext(ctx, `
			INSERT INTO image_assets (
				public_id, owner_user_id, project_id, source_type, object_key, mime_type,
				width, height, byte_size, sha256, origin_job_id, parent_asset_ids
			) VALUES ($1, $2, $3, 'generated', $4, $5, $6, $7, $8, $9, $10, '[]'::jsonb)
			ON CONFLICT (object_key) DO UPDATE SET object_key = EXCLUDED.object_key
			RETURNING id`,
			newImageCanvasPublicID("asset"), ownerUserID, projectID.Int64, result.ObjectKey,
			result.MIMEType, result.Width, result.Height, result.ByteSize, result.SHA256, jobID,
		).Scan(&assetID)
		if err != nil {
			return false, fmt.Errorf("upsert generated image asset: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE image_job_results SET asset_id = $3 WHERE job_id = $1 AND index = $2`,
			jobID, result.Index, assetID); err != nil {
			return false, fmt.Errorf("link image job result asset: %w", err)
		}
		result.AssetID = &assetID
	}
	delta := 0
	if previousStatus != "completed" && result.Status == "completed" {
		delta = 1
	} else if previousStatus == "completed" && result.Status != "completed" {
		delta = -1
	}
	if delta != 0 {
		cas, err := tx.ExecContext(ctx, `
			UPDATE image_jobs
			SET completed_count = GREATEST(0, completed_count + $3), updated_at = NOW()
			WHERE id = $1 AND attempt_id = $2 AND status = 'running'`, jobID, attemptID, delta)
		if err := imageJobCASResult(cas, err, "update image job completed count"); err != nil {
			return false, err
		}
	}
	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("commit image job result: %w", err)
	}
	return inserted, nil
}

func (r *imageJobRepository) SettleReservation(ctx context.Context, jobID int64) error {
	return r.transitionImageJobReservation(ctx, jobID, "settled")
}

func (r *imageJobRepository) ReleaseReservation(ctx context.Context, jobID int64) error {
	return r.transitionImageJobReservation(ctx, jobID, "released")
}

func (r *imageJobRepository) transitionImageJobReservation(ctx context.Context, jobID int64, target string) error {
	if jobID <= 0 {
		return fmt.Errorf("image job ID is required for reservation transition")
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin image job reservation transition: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	var reservationStatus, settlementStatus string
	if err := tx.QueryRowContext(ctx, `
		SELECT reservation_status, settlement_status
		FROM image_jobs WHERE id = $1 FOR UPDATE`, jobID).Scan(&reservationStatus, &settlementStatus); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return service.ErrImageJobNotFound
		}
		return fmt.Errorf("lock image job reservation: %w", err)
	}
	if reservationStatus == target && settlementStatus == target {
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit image job reservation replay: %w", err)
		}
		return nil
	}
	if reservationStatus != "held" || (settlementStatus != "pending" && settlementStatus != "settling") {
		return service.ErrImageJobConflict
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE image_jobs
		SET reservation_status = $2, settlement_status = $2, updated_at = NOW()
		WHERE id = $1`, jobID, target); err != nil {
		return fmt.Errorf("update image job reservation: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit image job reservation transition: %w", err)
	}
	return nil
}

func (r *imageJobRepository) MarkTerminal(ctx context.Context, jobID int64, attemptID string, update service.ImageJobTerminalUpdate) error {
	if !update.Status.Terminal() || update.Status == service.ImageJobStatusExpired {
		return fmt.Errorf("%w: running -> %s", service.ErrImageJobInvalidTransition, update.Status)
	}
	if update.FinishedAt.IsZero() {
		update.FinishedAt = time.Now().UTC()
	}
	if strings.TrimSpace(update.ReservationStatus) == "" || strings.TrimSpace(update.SettlementStatus) == "" {
		update.ReservationStatus, update.SettlementStatus = terminalImageJobReservationState(update.Status, update.CompletedCount)
	}
	var usage any
	if len(update.Usage) > 0 {
		if !json.Valid(update.Usage) {
			return fmt.Errorf("invalid image job usage JSON")
		}
		usage = string(update.Usage)
	}
	var errorType, errorCode, errorMessage any
	errorRetryable := false
	if update.Error != nil {
		errorType = nullableString(update.Error.Type)
		errorCode = nullableString(update.Error.Code)
		errorMessage = nullableString(update.Error.Message)
		errorRetryable = update.Error.Retryable
	}
	result, err := r.db.ExecContext(ctx, `
		UPDATE image_jobs
		SET status = $1, completed_count = $2, usage = $3::jsonb,
			finished_at = $4, canceled_at = $5,
			error_type = $6, error_code = $7, error_message = $8,
			error_retryable = $9,
			reservation_status = CASE
				WHEN reservation_status = 'held' THEN COALESCE(NULLIF($10, ''), reservation_status)
				ELSE reservation_status
			END,
			settlement_status = CASE
				WHEN reservation_status = 'held' AND settlement_status IN ('pending', 'settling')
					THEN COALESCE(NULLIF($11, ''), settlement_status)
				ELSE settlement_status
			END,
			updated_at = NOW()
		WHERE id = $12 AND attempt_id = $13 AND status IN ('queued', 'running')`,
		string(update.Status), update.CompletedCount, usage,
		update.FinishedAt, nullableTimePointer(update.CanceledAt),
		errorType, errorCode, errorMessage, errorRetryable,
		update.ReservationStatus, update.SettlementStatus,
		jobID, attemptID,
	)
	return imageJobCASResult(result, err, "mark image job terminal")
}

func terminalImageJobReservationState(status service.ImageJobStatus, completedCount int) (string, string) {
	if (status == service.ImageJobStatusCompleted || status == service.ImageJobStatusPartial) && completedCount > 0 {
		return "settled", "settled"
	}
	return "released", "released"
}

func (r *imageJobRepository) CancelOwned(ctx context.Context, publicID string, apiKeyID int64, at time.Time) (*service.ImageJob, error) {
	return r.cancel(ctx, publicID, &apiKeyID, at)
}

func (r *imageJobRepository) CancelAdmin(ctx context.Context, publicID string, at time.Time) (*service.ImageJob, error) {
	return r.cancel(ctx, publicID, nil, at)
}

func (r *imageJobRepository) cancel(ctx context.Context, publicID string, apiKeyID *int64, at time.Time) (*service.ImageJob, error) {
	if at.IsZero() {
		at = time.Now().UTC()
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin image job cancel: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	query := `SELECT ` + imageJobColumns + ` FROM image_jobs WHERE public_id = $1`
	args := []any{publicID}
	if apiKeyID != nil {
		query += ` AND api_key_id = $2`
		args = append(args, *apiKeyID)
	}
	query += ` FOR UPDATE`
	job, err := scanImageJob(tx.QueryRowContext(ctx, query, args...))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, service.ErrImageJobNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("lock image job for cancel: %w", err)
	}
	if job.Status == service.ImageJobStatusCanceled {
		if err := loadImageJobRelations(ctx, tx, job); err != nil {
			return nil, err
		}
		if err := tx.Commit(); err != nil {
			return nil, fmt.Errorf("commit image job cancel replay: %w", err)
		}
		return job, nil
	}
	if job.Status.Terminal() {
		return nil, service.ErrImageJobCancelConflict
	}
	if job.Status == service.ImageJobStatusQueued {
		job, err = scanImageJob(tx.QueryRowContext(ctx, `
			UPDATE image_jobs
			SET status = 'canceled', cancel_requested_at = $2, canceled_at = $2,
				finished_at = $2, reservation_status = 'released',
				settlement_status = 'released', updated_at = $2
			WHERE id = $1 AND status = 'queued'
			RETURNING `+imageJobColumns, job.ID, at))
	} else {
		job, err = scanImageJob(tx.QueryRowContext(ctx, `
			UPDATE image_jobs
			SET cancel_requested_at = COALESCE(cancel_requested_at, $2), updated_at = $2
			WHERE id = $1 AND status = 'running'
			RETURNING `+imageJobColumns, job.ID, at))
	}
	if errors.Is(err, sql.ErrNoRows) {
		return nil, service.ErrImageJobConflict
	}
	if err != nil {
		return nil, fmt.Errorf("cancel image job: %w", err)
	}
	if err := loadImageJobRelations(ctx, tx, job); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit image job cancel: %w", err)
	}
	return job, nil
}

func (r *imageJobRepository) IsCancelRequested(ctx context.Context, jobID int64, attemptID string) (bool, error) {
	var requested bool
	err := r.db.QueryRowContext(ctx, `
		SELECT cancel_requested_at IS NOT NULL
		FROM image_jobs
		WHERE id = $1 AND attempt_id = $2 AND status = 'running'`, jobID, attemptID).Scan(&requested)
	if errors.Is(err, sql.ErrNoRows) {
		return false, service.ErrImageJobAttemptMismatch
	}
	if err != nil {
		return false, fmt.Errorf("check image job cancellation: %w", err)
	}
	return requested, nil
}

func (r *imageJobRepository) RecoverStale(ctx context.Context, cutoff time.Time) (int64, int64, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, 0, fmt.Errorf("begin stale image job recovery: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	requeueResult, err := tx.ExecContext(ctx, `
		UPDATE image_jobs
		SET status = 'queued', worker_id = NULL, attempt_id = NULL,
			heartbeat_at = NULL, execution_phase = 'preflight', updated_at = NOW()
		WHERE status = 'running' AND execution_phase = 'preflight'
			AND COALESCE(heartbeat_at, started_at, updated_at) < $1`, cutoff)
	if err != nil {
		return 0, 0, fmt.Errorf("requeue stale preflight image jobs: %w", err)
	}
	indeterminateResult, err := tx.ExecContext(ctx, `
		UPDATE image_jobs
		SET status = 'indeterminate', finished_at = NOW(), updated_at = NOW(),
			reservation_status = CASE
				WHEN reservation_status = 'held' THEN 'released'
				ELSE reservation_status
			END,
			settlement_status = CASE
				WHEN reservation_status = 'held' AND settlement_status IN ('pending', 'settling') THEN 'released'
				ELSE settlement_status
			END,
			error_type = 'upstream_error', error_code = 'execution_indeterminate',
			error_message = 'Image generation may have completed upstream; the job was not retried automatically',
			error_retryable = false
		WHERE status = 'running' AND execution_phase = 'upstream'
			AND COALESCE(heartbeat_at, started_at, updated_at) < $1`, cutoff)
	if err != nil {
		return 0, 0, fmt.Errorf("mark stale upstream image jobs indeterminate: %w", err)
	}
	requeued, err := requeueResult.RowsAffected()
	if err != nil {
		return 0, 0, fmt.Errorf("count requeued image jobs: %w", err)
	}
	indeterminate, err := indeterminateResult.RowsAffected()
	if err != nil {
		return 0, 0, fmt.Errorf("count indeterminate image jobs: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return 0, 0, fmt.Errorf("commit stale image job recovery: %w", err)
	}
	return requeued, indeterminate, nil
}

func (r *imageJobRepository) ListExpired(ctx context.Context, now time.Time, limit int) ([]*service.ImageJob, error) {
	if limit <= 0 {
		return nil, fmt.Errorf("image job expiration limit must be greater than 0")
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT `+imageJobColumns+`
		FROM image_jobs
		WHERE status IN ('completed', 'partial', 'failed', 'indeterminate', 'canceled')
			AND expires_at <= $1
		ORDER BY expires_at, id
		LIMIT $2`, now, limit)
	if err != nil {
		return nil, fmt.Errorf("list expired image jobs: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var jobs []*service.ImageJob
	for rows.Next() {
		job, err := scanImageJob(rows)
		if err != nil {
			return nil, fmt.Errorf("scan expired image job: %w", err)
		}
		jobs = append(jobs, job)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate expired image jobs: %w", err)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("close expired image jobs: %w", err)
	}
	for _, job := range jobs {
		if err := loadImageJobRelations(ctx, r.db, job); err != nil {
			return nil, err
		}
	}
	return jobs, nil
}

func (r *imageJobRepository) MarkExpired(ctx context.Context, jobID int64, fromStatus service.ImageJobStatus, expiredAt time.Time) error {
	if !fromStatus.Terminal() || fromStatus == service.ImageJobStatusExpired {
		return fmt.Errorf("%w: %s -> expired", service.ErrImageJobInvalidTransition, fromStatus)
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin image job expiration: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	result, err := tx.ExecContext(ctx, `
		UPDATE image_jobs
		SET status = 'expired',
			reservation_status = CASE WHEN reservation_status = 'settled' THEN 'settled' ELSE 'released' END,
			settlement_status = CASE WHEN settlement_status = 'settled' THEN 'settled' ELSE 'released' END,
			updated_at = $3
		WHERE id = $1 AND status = $2`, jobID, string(fromStatus), expiredAt)
	if err != nil {
		return fmt.Errorf("mark image job expired: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("count expired image job update: %w", err)
	}
	if count != 1 {
		return service.ErrImageJobConflict
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM image_job_results WHERE job_id = $1`, jobID); err != nil {
		return fmt.Errorf("delete expired image job results: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM image_job_inputs WHERE job_id = $1`, jobID); err != nil {
		return fmt.Errorf("delete expired image job inputs: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit image job expiration: %w", err)
	}
	return nil
}

type imageJobScanner interface {
	Scan(dest ...any) error
}

func scanImageJob(scanner imageJobScanner) (*service.ImageJob, error) {
	job := &service.ImageJob{}
	var status string
	var requestJSON []byte
	var idempotencyKey sql.NullString
	var reservationBillingType int
	var reservationSubscriptionID sql.NullInt64
	var usageJSON []byte
	var attemptID, workerID sql.NullString
	var heartbeatAt, cancelRequestedAt, canceledAt sql.NullTime
	var errorType, errorCode, errorMessage sql.NullString
	var jobErrorRetryable bool
	var startedAt, finishedAt sql.NullTime
	var projectID, policyVersion sql.NullInt64
	var clientNodeID, selectedModel, successfulModel sql.NullString
	var attemptPlanJSON, attemptLogJSON []byte
	err := scanner.Scan(
		&job.ID, &job.PublicID, &job.UserID, &job.APIKeyID, &job.GroupID,
		&job.Endpoint, &job.Operation, &job.Mode,
		&job.RequestedModel, &job.MappedModel, &status,
		&job.RequestedCount, &job.CompletedCount,
		&requestJSON, &job.RequestDigest, &idempotencyKey, &job.ReservedUSD,
		&reservationBillingType, &reservationSubscriptionID,
		&job.ReservationStatus, &usageJSON, &job.SettlementStatus,
		&attemptID, &workerID, &job.ExecutionPhase,
		&heartbeatAt, &cancelRequestedAt, &canceledAt,
		&errorType, &errorCode, &errorMessage, &jobErrorRetryable,
		&startedAt, &finishedAt, &job.ExpiresAt, &job.CreatedAt, &job.UpdatedAt,
		&projectID, &clientNodeID, &selectedModel, &policyVersion,
		&attemptPlanJSON, &successfulModel, &attemptLogJSON,
	)
	if err != nil {
		return nil, err
	}
	job.Status = service.ImageJobStatus(status)
	if err := json.Unmarshal(requestJSON, &job.Request); err != nil {
		return nil, fmt.Errorf("unmarshal image job request: %w", err)
	}
	job.IdempotencyKeyHash = nullStringPointer(idempotencyKey)
	job.ReservationBillingType = int8(reservationBillingType)
	job.ReservationSubscriptionID = nullInt64Pointer(reservationSubscriptionID)
	job.AttemptID = nullStringPointer(attemptID)
	job.WorkerID = nullStringPointer(workerID)
	job.HeartbeatAt = nullTimePointer(heartbeatAt)
	job.CancelRequestedAt = nullTimePointer(cancelRequestedAt)
	job.CanceledAt = nullTimePointer(canceledAt)
	job.StartedAt = nullTimePointer(startedAt)
	job.FinishedAt = nullTimePointer(finishedAt)
	job.ProjectID = nullInt64Pointer(projectID)
	job.ClientNodeID = clientNodeID.String
	job.SelectedModel = selectedModel.String
	if policyVersion.Valid {
		job.PolicyVersion = policyVersion.Int64
	}
	job.SuccessfulModel = successfulModel.String
	if len(attemptPlanJSON) > 0 {
		if err := json.Unmarshal(attemptPlanJSON, &job.AttemptPlan); err != nil {
			return nil, fmt.Errorf("unmarshal image job attempt plan: %w", err)
		}
	}
	if len(attemptLogJSON) > 0 {
		if err := json.Unmarshal(attemptLogJSON, &job.AttemptLog); err != nil {
			return nil, fmt.Errorf("unmarshal image job attempt log: %w", err)
		}
	}
	job.Usage = append(json.RawMessage(nil), usageJSON...)
	if errorType.Valid || errorCode.Valid || errorMessage.Valid {
		job.Error = &service.ImageJobError{
			Type: errorType.String, Code: errorCode.String, Message: errorMessage.String,
			Retryable: jobErrorRetryable,
		}
	}
	return job, nil
}

func loadImageJobRelations(ctx context.Context, query imageJobQueryer, job *service.ImageJob) error {
	inputRows, err := query.QueryContext(ctx, `
		SELECT id, job_id, index, kind, object_key, mime_type, byte_size, sha256, created_at
		FROM image_job_inputs WHERE job_id = $1 ORDER BY kind, index`, job.ID)
	if err != nil {
		return fmt.Errorf("load image job inputs: %w", err)
	}
	for inputRows.Next() {
		var input service.ImageJobInput
		if err := inputRows.Scan(&input.ID, &input.JobID, &input.Index, &input.Kind, &input.ObjectKey, &input.MIMEType, &input.ByteSize, &input.SHA256, &input.CreatedAt); err != nil {
			_ = inputRows.Close()
			return fmt.Errorf("scan image job input: %w", err)
		}
		job.Inputs = append(job.Inputs, input)
	}
	if err := inputRows.Err(); err != nil {
		_ = inputRows.Close()
		return fmt.Errorf("iterate image job inputs: %w", err)
	}
	if err := inputRows.Close(); err != nil {
		return fmt.Errorf("close image job inputs: %w", err)
	}

	resultRows, err := query.QueryContext(ctx, `
		SELECT r.id, r.job_id, r.index, r.status, r.object_key, r.mime_type, r.byte_size,
			r.width, r.height, r.size_tier, r.revised_prompt, r.upstream_output_id,
			r.asset_id, a.public_id, r.created_at, r.updated_at
		FROM image_job_results r
		LEFT JOIN image_assets a ON a.id = r.asset_id AND a.deleted_at IS NULL
		WHERE r.job_id = $1 ORDER BY r.index`, job.ID)
	if err != nil {
		return fmt.Errorf("load image job results: %w", err)
	}
	defer func() { _ = resultRows.Close() }()
	for resultRows.Next() {
		var result service.ImageJobResult
		var objectKey, mimeType, sizeTier, revisedPrompt, upstreamOutputID sql.NullString
		var byteSize sql.NullInt64
		var width, height sql.NullInt64
		var assetID sql.NullInt64
		var assetPublicID sql.NullString
		if err := resultRows.Scan(
			&result.ID, &result.JobID, &result.Index, &result.Status,
			&objectKey, &mimeType, &byteSize, &width, &height, &sizeTier,
			&revisedPrompt, &upstreamOutputID, &assetID, &assetPublicID,
			&result.CreatedAt, &result.UpdatedAt,
		); err != nil {
			return fmt.Errorf("scan image job result: %w", err)
		}
		result.ObjectKey = objectKey.String
		result.MIMEType = mimeType.String
		result.ByteSize = byteSize.Int64
		result.Width = int(width.Int64)
		result.Height = int(height.Int64)
		result.SizeTier = sizeTier.String
		result.RevisedPrompt = revisedPrompt.String
		result.UpstreamOutputID = upstreamOutputID.String
		result.AssetID = nullInt64Pointer(assetID)
		result.AssetPublicID = assetPublicID.String
		job.Results = append(job.Results, result)
	}
	if err := resultRows.Err(); err != nil {
		return fmt.Errorf("iterate image job results: %w", err)
	}
	return nil
}

type imageJobQueryer interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

func imageJobCASResult(result sql.Result, err error, operation string) error {
	if err != nil {
		return fmt.Errorf("%s: %w", operation, err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("%s rows affected: %w", operation, err)
	}
	if count != 1 {
		return service.ErrImageJobAttemptMismatch
	}
	return nil
}

func prefixedImageJobColumns(prefix string) string {
	columns := []string{
		"id", "public_id", "user_id", "api_key_id", "group_id", "endpoint", "operation", "mode",
		"requested_model", "mapped_model", "status", "requested_count", "completed_count",
		"request", "request_digest", "idempotency_key_hash", "reserved_usd",
		"reservation_billing_type", "reservation_subscription_id", "reservation_status",
		"usage", "settlement_status", "attempt_id", "worker_id", "execution_phase",
		"heartbeat_at", "cancel_requested_at", "canceled_at", "error_type", "error_code",
		"error_message", "error_retryable", "started_at", "finished_at", "expires_at", "created_at", "updated_at",
		"project_id", "client_node_id", "selected_model", "policy_version", "attempt_plan", "successful_model", "attempt_log",
	}
	for i := range columns {
		columns[i] = prefix + "." + columns[i]
	}
	return joinSQLColumns(columns)
}

func joinSQLColumns(columns []string) string {
	if len(columns) == 0 {
		return ""
	}
	joined := columns[0]
	for _, column := range columns[1:] {
		joined += ", " + column
	}
	return joined
}

func nullableStringPointer(value *string) any {
	if value == nil {
		return nil
	}
	return *value
}

func nullableInt64Pointer(value *int64) any {
	if value == nil {
		return nil
	}
	return *value
}

func nullableTimePointer(value *time.Time) any {
	if value == nil {
		return nil
	}
	return *value
}

func nullableString(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func nullablePositiveInt(value int) any {
	if value <= 0 {
		return nil
	}
	return value
}

func nullablePositiveInt64(value int64) any {
	if value <= 0 {
		return nil
	}
	return value
}

func nullStringPointer(value sql.NullString) *string {
	if !value.Valid {
		return nil
	}
	cloned := value.String
	return &cloned
}

func nullInt64Pointer(value sql.NullInt64) *int64 {
	if !value.Valid {
		return nil
	}
	cloned := value.Int64
	return &cloned
}

func nullTimePointer(value sql.NullTime) *time.Time {
	if !value.Valid {
		return nil
	}
	cloned := value.Time
	return &cloned
}
