# Async Images and Codex Plugin Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 为 Sub2API 增加可恢复的异步图片 Job、单次上游 `n=4` 批量生成、连续场景编排，以及可安装的 Codex `sub2api-image` Plugin。

**Architecture:** 保留现有同步 `/v1/images/generations` 和 `/v1/images/edits`，请求携带 `Prefer: respond-async` 时把规范化请求和输入对象写入 PostgreSQL/S3 或 R2，再由独立 worker 调用与同步路径相同的图片执行器。批量模式始终保留原始 `n` 并只调用一次上游；连续模式串行执行首帧和后续 edits，每一帧立即落对象存储并更新 Job。

**Tech Stack:** Go 1.x、Gin、Ent、PostgreSQL `FOR UPDATE SKIP LOCKED`、AWS SDK v2（S3/R2）、Python 3.9+ 标准库、Codex Plugin/Skill、Go `testing`/`testify`/`sqlmock`、Python `unittest`。

---

## 实施前约束

- 当前工作树已有 11 个与“分组 fallback”相关的未提交改动。实施必须先使用 `superpowers:using-git-worktrees` 从 `aacb627` 创建干净的独立 worktree，所有下方提交命令都只在该 worktree 执行；不得在 `/home/ubuntu/sub2api` 原工作树运行 `git add`、生成代码或合并。
- `backend/internal/service/openai_account_scheduler.go`、`backend/internal/service/openai_gateway_service.go`、`backend/cmd/server/wire_gen.go` 与原工作树改动重叠。功能分支完成后先让用户为原 fallback 工作建立可恢复检查点，再合并并逐块解决冲突；在此之前只交付功能分支，不对脏工作树执行 cherry-pick/merge。
- 创建 worktree 前把原工作树的 `git status --short` 和 `git diff --stat` 保存到会话记录；每个任务只需确认独立 worktree 干净或只含当前任务修改。
- `batch` 的验收不变量是 `one job -> one Execute call -> one upstream request with n=4`。任何重试只能重发完整 `n=4`，不得拆成四个 `n=1`。
- Job 数据库中不保存 Base64、multipart 原文或明文 API Key；图片字节只进入对象存储。
- 所有任务都按红灯、最小实现、绿灯、提交的顺序执行。生成代码后只提交本任务涉及的生成文件。

## 文件职责映射

### Backend 新文件

- `backend/internal/config/image_jobs.go`：异步图片配置默认值和独立校验。
- `backend/ent/schema/image_job.go`：Job 主表 Ent schema。
- `backend/ent/schema/image_job_input.go`：输入图和 mask 元数据 Ent schema。
- `backend/ent/schema/image_job_result.go`：最终图片元数据 Ent schema。
- `backend/migrations/159_image_jobs.sql`：三张表、状态约束、幂等条件唯一索引和 worker 索引。
- `backend/internal/service/image_job.go`：领域类型、状态、错误与 repository/store 接口。
- `backend/internal/service/image_job_request.go`：可序列化请求、摘要、输入对象键和请求体重建。
- `backend/internal/service/image_job_service.go`：创建、所有权查询、取消和 API DTO。
- `backend/internal/service/image_job_worker.go`：worker 生命周期、认领、heartbeat、恢复和结算。
- `backend/internal/service/image_job_sequence.go`：continuity 串行编排。
- `backend/internal/service/image_job_billing.go`：费用上限估算、预留和幂等结算。
- `backend/internal/service/image_job_cleanup.go`：TTL 扫描、对象删除和 expired 状态转换。
- `backend/internal/service/image_job_metrics.go`：轻量原子指标快照。
- `backend/internal/service/openai_images_executor.go`：与 HTTP 无关的账号选择、故障转移和执行器。
- `backend/internal/service/openai_images_sink.go`：同步 HTTP sink 与异步对象存储 sink 的公共协议。
- `backend/internal/repository/image_job_repo.go`：PostgreSQL Job repository 和原子状态转换。
- `backend/internal/repository/image_job_store.go`：S3/R2/local 对象存储工厂。
- `backend/internal/repository/image_job_store_s3.go`：S3/R2 实现。
- `backend/internal/repository/image_job_store_local.go`：开发环境本地目录实现。
- `backend/internal/handler/openai_image_jobs.go`：202 创建、sequence、查询、下载、取消 handler。
- `backend/internal/handler/admin/image_job_handler.go`：管理员查询、下载和取消任意 Job。

### Backend 修改文件

- `backend/internal/config/config.go`：把 `ImageJobsConfig` 挂到 `GatewayConfig`，注册默认值并调用校验。
- `backend/internal/service/openai_images.go`：规范化解析、严格能力分类和 sink 执行入口。
- `backend/internal/service/openai_images_responses.go`：返回结构化图片结果；明确转发桥接支持字段。
- `backend/internal/service/openai_account_scheduler.go`：严格执行 native/bridge 图片能力，不再静默降级。
- `backend/internal/service/account.go`：账号图片能力矩阵。
- `backend/internal/handler/openai_images.go`：在现有同步链前识别 `Prefer: respond-async`。
- `backend/internal/handler/openai_gateway_handler.go`：注入 `ImageJobService`。
- `backend/internal/server/routes/gateway.go`：注册 sequence、status、result 和 cancel 路由。
- `backend/internal/server/routes/admin.go`：注册受 AdminAuth 保护的 Job 运维路由。
- `backend/internal/repository/wire.go`、`backend/internal/service/wire.go`、`backend/cmd/server/wire.go`、`backend/cmd/server/wire_gen.go`：依赖注入和 worker 启停。
- `deploy/config.example.yaml`、`deploy/.env.example`：配置示例。

### Plugin 新文件

- `plugins/sub2api-image/.codex-plugin/plugin.json`：Plugin manifest。
- `plugins/sub2api-image/skills/image/SKILL.md`：自然语言和 `$image` 工作流。
- `plugins/sub2api-image/skills/image/scripts/sub2api_image.py`：标准库 CLI、multipart、轮询和下载。
- `plugins/sub2api-image/skills/image/references/api.md`：API 字段和错误语义。
- `plugins/sub2api-image/README.md`：安装、环境变量和示例。
- `plugins/sub2api-image/tests/test_sub2api_image.py`：模拟服务器端到端测试。

### 文档修改文件

- `docs/openai-images.md`：同步/异步协议、batch 与 continuity 说明（新建）。
- `README.md`：能力入口和 Plugin 链接。

### Task 1: 增加 `gateway.image_jobs` 配置

**Files:**
- Create: `backend/internal/config/image_jobs.go`
- Create: `backend/internal/config/image_jobs_test.go`
- Modify: `backend/internal/config/config.go`
- Modify: `deploy/config.example.yaml`
- Modify: `deploy/.env.example`

- [ ] **Step 1: 写默认值和校验的失败测试**

```go
func TestDefaultImageJobsConfig(t *testing.T) {
	cfg := DefaultImageJobsConfig()
	require.False(t, cfg.Enabled)
	require.Equal(t, 2, cfg.WorkerConcurrency)
	require.Equal(t, 1800, cfg.TaskTimeoutSeconds)
	require.Equal(t, 4, cfg.MaxOutputsPerJob)
	require.Equal(t, 4, cfg.MaxInputImages)
	require.Equal(t, 86400, cfg.ResultTTLSeconds)
	require.Equal(t, "s3", cfg.Storage.Driver)
}

func TestImageJobsConfigValidate(t *testing.T) {
	cfg := DefaultImageJobsConfig()
	cfg.Enabled = true
	cfg.Storage.Bucket = "images"
	cfg.Storage.AccessKeyID = "key"
	cfg.Storage.SecretAccessKey = "secret"
	require.NoError(t, cfg.Validate())
	cfg.MaxInputImages = 17
	require.EqualError(t, cfg.Validate(), "gateway.image_jobs.max_input_images must be between 1 and 16")
}
```

- [ ] **Step 2: 运行测试并确认红灯**

Run: `cd backend && go test ./internal/config -run 'Test(DefaultImageJobsConfig|ImageJobsConfigValidate)' -count=1`

Expected: FAIL，错误包含 `undefined: DefaultImageJobsConfig`。

- [ ] **Step 3: 实现独立配置类型、默认值和校验**

```go
type ImageJobsConfig struct {
	Enabled                  bool                  `mapstructure:"enabled"`
	WorkerConcurrency        int                   `mapstructure:"worker_concurrency"`
	PollIntervalMilliseconds int                   `mapstructure:"poll_interval_milliseconds"`
	HeartbeatIntervalSeconds int                   `mapstructure:"heartbeat_interval_seconds"`
	TaskTimeoutSeconds       int                   `mapstructure:"task_timeout_seconds"`
	MaxOutputsPerJob         int                   `mapstructure:"max_outputs_per_job"`
	MaxInputImages           int                   `mapstructure:"max_input_images"`
	ResultTTLSeconds         int                   `mapstructure:"result_ttl_seconds"`
	MaxReservationUSD        float64               `mapstructure:"max_reservation_usd"`
	Storage                  ImageJobStorageConfig `mapstructure:"storage"`
}

type ImageJobStorageConfig struct {
	Driver          string `mapstructure:"driver"`
	Endpoint        string `mapstructure:"endpoint"`
	Region          string `mapstructure:"region"`
	Bucket          string `mapstructure:"bucket"`
	Prefix          string `mapstructure:"prefix"`
	AccessKeyID     string `mapstructure:"access_key_id"`
	SecretAccessKey string `mapstructure:"secret_access_key"`
	ForcePathStyle  bool   `mapstructure:"force_path_style"`
	LocalDirectory  string `mapstructure:"local_directory"`
}

func DefaultImageJobsConfig() ImageJobsConfig {
	return ImageJobsConfig{
		WorkerConcurrency: 2, PollIntervalMilliseconds: 500,
		HeartbeatIntervalSeconds: 10, TaskTimeoutSeconds: 1800,
		MaxOutputsPerJob: 4, MaxInputImages: 4,
		ResultTTLSeconds: 86400, MaxReservationUSD: 1,
		Storage: ImageJobStorageConfig{Driver: "s3", Region: "auto", Prefix: "image-jobs/"},
	}
}
```

`Validate` 必须检查：并发大于 0、轮询大于 0、heartbeat 小于 task timeout、输出数为 1–4、输入数为 1–16、TTL 大于 0、最大预留大于 0；启用时 `s3` 要求 bucket/access key/secret，`local` 要求绝对目录，其他 driver 返回明确错误。`GatewayConfig` 新增 `ImageJobs ImageJobsConfig`，`setDefaults()` 注册所有键，`Config.Validate()` 调用 `c.Gateway.ImageJobs.Validate()`。

- [ ] **Step 4: 添加 YAML 和环境变量示例**

```yaml
gateway:
  image_jobs:
    enabled: false
    worker_concurrency: 2
    poll_interval_milliseconds: 500
    heartbeat_interval_seconds: 10
    task_timeout_seconds: 1800
    max_outputs_per_job: 4
    max_input_images: 4
    result_ttl_seconds: 86400
    max_reservation_usd: 1.0
    storage:
      driver: s3
      endpoint: ""
      region: auto
      bucket: ""
      prefix: image-jobs/
      access_key_id: ""
      secret_access_key: ""
      force_path_style: false
      local_directory: ""
```

在 `.env.example` 中加入同名大写键，例如 `GATEWAY_IMAGE_JOBS_ENABLED=false`，不提供真实凭据。

- [ ] **Step 5: 运行测试并提交**

Run: `cd backend && go test ./internal/config -run 'ImageJobs' -count=1`

Expected: PASS。

```bash
git add backend/internal/config/image_jobs.go backend/internal/config/image_jobs_test.go backend/internal/config/config.go deploy/config.example.yaml deploy/.env.example
git commit -m "feat: add async image job configuration"
```

### Task 2: 建立图片 Job 数据模型和迁移

**Files:**
- Create: `backend/ent/schema/image_job.go`
- Create: `backend/ent/schema/image_job_input.go`
- Create: `backend/ent/schema/image_job_result.go`
- Create: `backend/migrations/159_image_jobs.sql`
- Modify: `backend/internal/repository/migrations_schema_integration_test.go`
- Modify: generated files under `backend/ent/`

- [ ] **Step 1: 写迁移结构的失败集成测试**

在 `migrations_schema_integration_test.go` 增加一个测试，查询 `information_schema.columns` 并断言三张表存在；再查询 `pg_indexes` 断言 `idx_image_jobs_api_key_idempotency`、`idx_image_jobs_claim` 和 `idx_image_job_results_job_index` 存在。

```go
func TestMigrationsCreateImageJobTables(t *testing.T) {
	db := openMigrationTestDB(t)
	for _, table := range []string{"image_jobs", "image_job_inputs", "image_job_results"} {
		var got string
		require.NoError(t, db.QueryRow(`SELECT table_name FROM information_schema.tables WHERE table_schema='public' AND table_name=$1`, table).Scan(&got))
		require.Equal(t, table, got)
	}
}
```

- [ ] **Step 2: 运行测试并确认红灯**

Run: `cd backend && go test -tags=integration ./internal/repository -run TestMigrationsCreateImageJobTables -count=1`

Expected: FAIL，PostgreSQL 返回 `no rows in result set`。

- [ ] **Step 3: 编写三张 Ent schema**

`ImageJob` 使用 `TimeMixin`，并定义与迁移同名的字段。核心字段必须精确为：

```go
field.String("public_id").MaxLen(64).Unique(),
field.Int64("user_id"),
field.Int64("api_key_id"),
field.Int64("group_id"),
field.String("endpoint").MaxLen(64),
field.String("operation").MaxLen(32),
field.String("mode").MaxLen(32),
field.String("requested_model").MaxLen(128),
field.String("mapped_model").MaxLen(128).Default(""),
field.String("status").MaxLen(32),
field.Int("requested_count"),
field.Int("completed_count").Default(0),
field.JSON("request", json.RawMessage{}),
field.String("request_digest").MaxLen(64),
field.String("idempotency_key_hash").MaxLen(64).Optional().Nillable(),
field.Float("reserved_usd").SchemaType(map[string]string{dialect.Postgres: "decimal(20,8)"}).Default(0),
field.Int("reservation_billing_type").Default(1),
field.Int64("reservation_subscription_id").Optional().Nillable(),
field.String("reservation_status").MaxLen(20).Default("held"),
field.JSON("usage", json.RawMessage{}).Optional(),
field.String("settlement_status").MaxLen(20).Default("pending"),
field.String("attempt_id").MaxLen(64).Optional().Nillable(),
field.String("worker_id").MaxLen(128).Optional().Nillable(),
field.String("execution_phase").MaxLen(20).Default("preflight"),
field.Time("heartbeat_at").Optional().Nillable(),
field.Time("cancel_requested_at").Optional().Nillable(),
field.Time("canceled_at").Optional().Nillable(),
field.String("error_type").MaxLen(64).Optional().Nillable(),
field.String("error_code").MaxLen(64).Optional().Nillable(),
field.String("error_message").SchemaType(map[string]string{dialect.Postgres: "text"}).Optional().Nillable(),
field.Bool("error_retryable").Default(false),
field.Time("started_at").Optional().Nillable(),
field.Time("finished_at").Optional().Nillable(),
field.Time("expires_at"),
```

`ImageJobInput` 保存 `job_id/index/kind/object_key/mime_type/byte_size/sha256/created_at`；`ImageJobResult` 保存 `job_id/index/status/object_key/mime_type/byte_size/width/height/size_tier/revised_prompt/upstream_output_id/created_at/updated_at`。三者通过 edges 建立 Job 一对多关系，但 repository 的 claim 路径使用原生 SQL。

- [ ] **Step 4: 编写幂等 SQL 迁移**

迁移必须使用 `CREATE TABLE IF NOT EXISTS`，外键指向 `users/api_keys/groups/image_jobs`，状态检查包含：

```sql
CHECK (status IN ('queued','running','completed','partial','failed','indeterminate','canceled','expired')),
CHECK (execution_phase IN ('preflight','upstream')),
CHECK (reservation_status IN ('held','released','settled')),
CHECK (settlement_status IN ('pending','settling','settled','released'))
```

索引必须包含：

```sql
CREATE UNIQUE INDEX IF NOT EXISTS idx_image_jobs_api_key_idempotency
  ON image_jobs(api_key_id, idempotency_key_hash)
  WHERE idempotency_key_hash IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_image_jobs_claim
  ON image_jobs(status, created_at, id)
  WHERE status IN ('queued','running');
CREATE UNIQUE INDEX IF NOT EXISTS idx_image_job_inputs_job_kind_index
  ON image_job_inputs(job_id, kind, index);
CREATE UNIQUE INDEX IF NOT EXISTS idx_image_job_results_job_index
  ON image_job_results(job_id, index);
```

- [ ] **Step 5: 生成 Ent 代码并验证 schema**

Run: `cd backend && make generate`

Expected: Ent 生成 `imagejob`、`imagejobinput`、`imagejobresult` 包；Wire 也会重生成。立即运行 `git diff -- backend/cmd/server/wire_gen.go`，确认变化只来自生成器和当前功能，不包含手写凭据或无关构造链。

Run: `cd backend && go test -tags=integration ./internal/repository -run TestMigrationsCreateImageJobTables -count=1`

Expected: PASS。

- [ ] **Step 6: 提交迁移和生成代码**

```bash
git add backend/ent backend/migrations/159_image_jobs.sql backend/internal/repository/migrations_schema_integration_test.go
git commit -m "feat: add persistent image job schema"
```

不要把 `backend/cmd/server/wire_gen.go` 加入本次提交；Task 17 统一提交 Wire 变更。

### Task 3: 定义 Job 领域类型和状态机

**Files:**
- Create: `backend/internal/service/image_job.go`
- Create: `backend/internal/service/image_job_test.go`

- [ ] **Step 1: 写终态和状态转换失败测试**

```go
func TestImageJobStatusTransitions(t *testing.T) {
	require.True(t, ImageJobStatusCompleted.Terminal())
	require.False(t, ImageJobStatusRunning.Terminal())
	require.NoError(t, ValidateImageJobTransition(ImageJobStatusQueued, ImageJobStatusRunning))
	require.NoError(t, ValidateImageJobTransition(ImageJobStatusRunning, ImageJobStatusPartial))
	require.Error(t, ValidateImageJobTransition(ImageJobStatusCompleted, ImageJobStatusRunning))
}
```

- [ ] **Step 2: 运行测试并确认红灯**

Run: `cd backend && go test ./internal/service -run TestImageJobStatusTransitions -count=1`

Expected: FAIL，错误包含 `undefined: ImageJobStatusCompleted`。

- [ ] **Step 3: 实现领域常量、实体和端口**

```go
type ImageJobStatus string

const (
	ImageJobStatusQueued        ImageJobStatus = "queued"
	ImageJobStatusRunning       ImageJobStatus = "running"
	ImageJobStatusCompleted     ImageJobStatus = "completed"
	ImageJobStatusPartial       ImageJobStatus = "partial"
	ImageJobStatusFailed        ImageJobStatus = "failed"
	ImageJobStatusIndeterminate ImageJobStatus = "indeterminate"
	ImageJobStatusCanceled      ImageJobStatus = "canceled"
	ImageJobStatusExpired       ImageJobStatus = "expired"
)

type ImageJobError struct {
	Type string `json:"type"`
	Code string `json:"code"`
	Message string `json:"message"`
	Retryable bool `json:"retryable"`
}

type ImageJob struct {
	ID int64
	PublicID string
	UserID, APIKeyID, GroupID int64
	Endpoint, Operation, Mode, RequestedModel, MappedModel string
	Status ImageJobStatus
	RequestedCount, CompletedCount int
	Request ImageJobRequest
	RequestDigest string
	IdempotencyKeyHash *string
	ReservedUSD float64
	ReservationBillingType int8
	ReservationSubscriptionID *int64
	ReservationStatus, SettlementStatus string
	AttemptID, WorkerID *string
	ExecutionPhase string
	HeartbeatAt, CancelRequestedAt, CanceledAt, StartedAt, FinishedAt *time.Time
	ExpiresAt, CreatedAt, UpdatedAt time.Time
	Error *ImageJobError
	Results []ImageJobResult
}
```

同时定义 `ImageJobInput`、`ImageJobResult`、`ImageJobClaim`、`ImageJobCreate`，以及 repository 接口的完整方法集合：`CreateReserved`、`GetOwned`、`GetAdmin`、`ClaimNext`、`MarkUpstreamStarted`、`Heartbeat`、`UpsertResult`、`MarkTerminal`、`CancelOwned`、`CancelAdmin`、`IsCancelRequested`、`RecoverStale`、`ListExpired`、`MarkExpired`。状态转换表只允许设计文档中的合法边。

- [ ] **Step 4: 运行测试并提交**

Run: `cd backend && go test ./internal/service -run 'ImageJob(Status|Transition)' -count=1`

Expected: PASS。

```bash
git add backend/internal/service/image_job.go backend/internal/service/image_job_test.go
git commit -m "feat: define image job domain model"
```

### Task 4: 实现 S3/R2 和 local 图片对象存储

**Files:**
- Create: `backend/internal/repository/image_job_store.go`
- Create: `backend/internal/repository/image_job_store_s3.go`
- Create: `backend/internal/repository/image_job_store_local.go`
- Create: `backend/internal/repository/image_job_store_test.go`
- Modify: `backend/internal/repository/backup_s3_store.go`

- [ ] **Step 1: 写 local store 的失败测试**

```go
func TestLocalImageJobStoreRoundTripAndTraversal(t *testing.T) {
	root := t.TempDir()
	store, err := NewLocalImageJobObjectStore(root)
	require.NoError(t, err)
	require.NoError(t, store.Put(context.Background(), "image-jobs/key/job/results/0.png", []byte("png"), "image/png"))
	obj, err := store.Get(context.Background(), "image-jobs/key/job/results/0.png")
	require.NoError(t, err)
	require.Equal(t, []byte("png"), obj.Data)
	require.Equal(t, "image/png", obj.ContentType)
	require.Error(t, store.Put(context.Background(), "../escape", []byte("x"), "image/png"))
}
```

- [ ] **Step 2: 运行测试并确认红灯**

Run: `cd backend && go test ./internal/repository -run TestLocalImageJobStoreRoundTripAndTraversal -count=1`

Expected: FAIL，错误包含 `undefined: NewLocalImageJobObjectStore`。

- [ ] **Step 3: 实现公共接口、键校验和 local store**

服务层接口定义在 `image_job.go`：

```go
type ImageJobObject struct { Data []byte; ContentType string; Size int64 }
type ImageJobObjectStore interface {
	Put(ctx context.Context, key string, data []byte, contentType string) error
	Get(ctx context.Context, key string) (*ImageJobObject, error)
	Delete(ctx context.Context, key string) error
	Health(ctx context.Context) error
}
```

repository 的 `validateImageJobObjectKey` 必须拒绝空键、绝对路径、反斜线、`.` 和 `..` 段。local store 用 `os.OpenFile(..., O_CREATE|O_EXCL|O_WRONLY, 0o600)` 写临时文件，再 `os.Rename` 原子替换；内容类型保存在同路径 `.meta.json`，删除时同时删除数据和元数据。

- [ ] **Step 4: 提取共享 S3 client 并实现 S3/R2 store**

从 `backup_s3_store.go` 抽出不带业务语义的 `newS3CompatibleClient`，备份和图片 store 共用 client 构建但使用独立 bucket/prefix。图片实现必须设置 `ContentType`，repository 内定义 `const maxImageJobObjectBytes int64 = 20 << 20` 并在 `Get` 时限制读取大小，`Health` 使用 `HeadBucket`。

```go
func NewImageJobObjectStore(cfg config.ImageJobStorageConfig) (service.ImageJobObjectStore, error) {
	switch strings.ToLower(strings.TrimSpace(cfg.Driver)) {
	case "s3": return newS3ImageJobObjectStore(cfg)
	case "local": return NewLocalImageJobObjectStore(cfg.LocalDirectory)
	default: return nil, fmt.Errorf("unsupported image job storage driver %q", cfg.Driver)
	}
}
```

- [ ] **Step 5: 运行对象存储测试并提交**

Run: `cd backend && go test ./internal/repository -run 'ImageJobStore|S3BackupStore' -count=1`

Expected: PASS；测试不访问真实 S3。

```bash
git add backend/internal/repository/image_job_store.go backend/internal/repository/image_job_store_s3.go backend/internal/repository/image_job_store_local.go backend/internal/repository/image_job_store_test.go backend/internal/repository/backup_s3_store.go backend/internal/service/image_job.go
git commit -m "feat: add image job object storage"
```

### Task 5: 规范化图片请求并把输入移出数据库

**Files:**
- Create: `backend/internal/service/image_job_request.go`
- Create: `backend/internal/service/image_job_request_test.go`
- Modify: `backend/internal/service/openai_images.go`

- [ ] **Step 1: 写 multipart 多图和 mask 规范化失败测试**

```go
func TestNormalizeImageJobRequestStoresUploadsAndDigest(t *testing.T) {
	store := newMemoryImageObjectStore()
	parsed := &OpenAIImagesRequest{
		Endpoint: openAIImagesEditsEndpoint, Model: "gpt-image-2", Prompt: "change sky",
		N: 1, Uploads: []OpenAIImagesUpload{{FieldName: "image[]", FileName: "a.png", ContentType: "image/png", Data: []byte("a")}},
		MaskUpload: &OpenAIImagesUpload{FieldName: "mask", FileName: "mask.png", ContentType: "image/png", Data: []byte("m")},
	}
	req, inputs, digest, err := NormalizeImageJobRequest(context.Background(), store, "42/imgjob_x", parsed, nil, 4)
	require.NoError(t, err)
	require.Len(t, inputs, 2)
	require.Len(t, req.Inputs, 1)
	require.Equal(t, "image-jobs/42/imgjob_x/inputs/0-"+inputs[0].SHA256+".png", inputs[0].ObjectKey)
	require.Len(t, digest, 64)
}
```

- [ ] **Step 2: 运行测试并确认红灯**

Run: `cd backend && go test ./internal/service -run TestNormalizeImageJobRequestStoresUploadsAndDigest -count=1`

Expected: FAIL，错误包含 `undefined: NormalizeImageJobRequest`。

- [ ] **Step 3: 实现稳定 JSON 请求和摘要**

```go
type ImageJobRequest struct {
	Endpoint string `json:"endpoint"`
	Model string `json:"model"`
	Prompt string `json:"prompt"`
	N int `json:"n"`
	Size string `json:"size,omitempty"`
	ResponseFormat string `json:"response_format,omitempty"`
	Quality string `json:"quality,omitempty"`
	Background string `json:"background,omitempty"`
	OutputFormat string `json:"output_format,omitempty"`
	OutputCompression *int `json:"output_compression,omitempty"`
	Moderation string `json:"moderation,omitempty"`
	InputFidelity string `json:"input_fidelity,omitempty"`
	Style string `json:"style,omitempty"`
	PartialImages *int `json:"partial_images,omitempty"`
	InputURLs []string `json:"input_urls,omitempty"`
	MaskURL string `json:"mask_url,omitempty"`
	Inputs []ImageJobInputRef `json:"inputs,omitempty"`
	Mask *ImageJobInputRef `json:"mask,omitempty"`
	Scenes []string `json:"scenes,omitempty"`
}

type ImageJobInputRef struct {
	Kind string `json:"kind"`
	Index int `json:"index"`
	ObjectKey string `json:"object_key"`
	MIMEType string `json:"mime_type"`
	ByteSize int64 `json:"byte_size"`
	SHA256 string `json:"sha256"`
	FieldName string `json:"field_name,omitempty"`
}
```

规范化函数先校验 MIME、非空、单文件上限和 `max_input_images`，再按 SHA-256 写对象存储。摘要使用 `sha256(canonical JSON)`，其中输入对象只包含 `kind/index/sha256/mime_type/byte_size`，不包含 bucket、临时 URL 或随机 public ID，因此同一有效载荷得到相同摘要。

```go
func NormalizeImageJobRequest(ctx context.Context, store ImageJobObjectStore, objectSuffix string, parsed *OpenAIImagesRequest, scenes []string, maxInputs int) (ImageJobRequest, []ImageJobInput, string, error)
```

- [ ] **Step 4: 实现可逆请求体重建**

`BuildOpenAIImagesRequest` 从对象存储加载 inputs/mask，JSON URL 输入保持 URL，上传输入重建 multipart，字段名保留 `image`/`image[]`，并强制 `stream=true` 让 sink 可以逐张接收结果。把现有 parser 拆成 Gin wrapper 和纯函数 `ParseOpenAIImagesRequestBody(endpoint, contentType, body)`；重建后调用纯函数，确保 `RequiredCapability`、`N`、mask 和多图语义一致。

```go
func BuildOpenAIImagesRequest(ctx context.Context, store ImageJobObjectStore, req ImageJobRequest) ([]byte, string, *OpenAIImagesRequest, error)
```

- [ ] **Step 5: 运行 round-trip 测试并提交**

Run: `cd backend && go test ./internal/service -run 'NormalizeImageJobRequest|BuildOpenAIImagesRequest' -count=1`

Expected: PASS；断言重建结果保留两张 `image[]`、一个 mask、`input_fidelity=high` 和原始 `n`。

```bash
git add backend/internal/service/image_job_request.go backend/internal/service/image_job_request_test.go backend/internal/service/openai_images.go
git commit -m "feat: normalize async image requests"
```

### Task 6: 实现 PostgreSQL repository、幂等和原子认领

**Files:**
- Create: `backend/internal/repository/image_job_repo.go`
- Create: `backend/internal/repository/image_job_repo_test.go`
- Create: `backend/internal/repository/image_job_repo_integration_test.go`
- Modify: `backend/internal/repository/wire.go`

- [ ] **Step 1: 写幂等冲突和双 worker 认领的失败测试**

```go
func TestImageJobRepositoryCreateReservedIdempotency(t *testing.T) {
	repo, fixture := newImageJobIntegrationRepo(t)
	created, replay, err := repo.CreateReserved(context.Background(), fixture.Create("hash-a", "idem-a"))
	require.NoError(t, err)
	require.True(t, created)
	created, replay, err = repo.CreateReserved(context.Background(), fixture.Create("hash-a", "idem-a"))
	require.NoError(t, err)
	require.False(t, created)
	require.NotNil(t, replay)
	_, _, err = repo.CreateReserved(context.Background(), fixture.Create("hash-b", "idem-a"))
	require.ErrorIs(t, err, service.ErrImageJobIdempotencyConflict)
}

func TestImageJobRepositoryClaimNextOnlyOnce(t *testing.T) {
	repo, fixture := newImageJobIntegrationRepo(t)
	fixture.InsertQueued(t, repo)
	claims := claimConcurrently(t, repo, 2)
	require.Len(t, claims, 1)
}
```

- [ ] **Step 2: 运行测试并确认红灯**

Run: `cd backend && go test -tags=integration ./internal/repository -run 'TestImageJobRepository(CreateReservedIdempotency|ClaimNextOnlyOnce)' -count=1`

Expected: FAIL，错误包含 `undefined: newImageJobIntegrationRepo` 或 repository 构造器缺失。

- [ ] **Step 3: 实现创建、扫描和幂等冲突**

构造器使用 SQL DB，因为创建预留、条件唯一冲突和 claim 必须在事务中完成：

```go
func NewImageJobRepository(client *dbent.Client, db *sql.DB) service.ImageJobRepository {
	return &imageJobRepository{client: client, db: db}
}
```

`CreateReserved` 在事务中插入 Job 和 inputs。遇到 `(api_key_id,idempotency_key_hash)` 冲突时读取既有记录：摘要相同返回 `(false, existing, nil)`，摘要不同返回 `ErrImageJobIdempotencyConflict`。所有 `SELECT` 使用统一 `scanImageJob`，防止可空字段在不同方法中映射不一致。

- [ ] **Step 4: 实现 claim、heartbeat 和不可逆终态**

claim SQL 必须保留如下结构：

```sql
WITH next AS (
  SELECT id FROM image_jobs
  WHERE status = 'queued'
  ORDER BY created_at, id
  LIMIT 1
  FOR UPDATE SKIP LOCKED
)
UPDATE image_jobs j
SET status='running', worker_id=$1, attempt_id=$2,
    execution_phase='preflight', heartbeat_at=NOW(),
    started_at=COALESCE(started_at, NOW()), updated_at=NOW()
FROM next WHERE j.id=next.id
RETURNING j.*;
```

`Heartbeat`、`MarkUpstreamStarted(attemptID, mappedModel)`、`UpsertResult` 和 `MarkTerminal` 都必须带 `attempt_id` CAS；`MarkTerminal` 的 WHERE 限制当前状态为 queued/running，终态不能被普通 worker 重写。`RecoverStale` 在一个事务中把 stale preflight 任务改回 queued，把 stale upstream 任务改为 indeterminate。

- [ ] **Step 5: 跑 repository 单元和集成测试**

Run: `cd backend && go test ./internal/repository -run ImageJobRepository -count=1`

Run: `cd backend && go test -tags=integration ./internal/repository -run ImageJobRepository -count=1`

Expected: 两条命令均 PASS；并发测试只有一个非空 claim。

- [ ] **Step 6: 提交 repository**

```bash
git add backend/internal/repository/image_job_repo.go backend/internal/repository/image_job_repo_test.go backend/internal/repository/image_job_repo_integration_test.go backend/internal/repository/wire.go backend/internal/service/image_job.go
git commit -m "feat: add image job repository"
```

### Task 7: 增加费用上限预留和幂等结算

**Files:**
- Create: `backend/internal/service/image_job_billing.go`
- Create: `backend/internal/service/image_job_billing_test.go`
- Modify: `backend/internal/repository/image_job_repo.go`
- Modify: `backend/internal/repository/image_job_repo_integration_test.go`
- Modify: `backend/internal/service/openai_gateway_service.go`
- Modify: `backend/internal/service/openai_gateway_record_usage_test.go`

- [ ] **Step 1: 写余额、API key quota 和订阅预留失败测试**

```go
func TestCreateReservedRejectsHeldBalanceOvercommit(t *testing.T) {
	repo, fixture := newImageJobIntegrationRepo(t)
	fixture.SetBalance(t, 1.00)
	requireCreateReserved(t, repo, fixture.CreateWithReserve(0.75))
	_, _, err := repo.CreateReserved(context.Background(), fixture.CreateWithReserve(0.50))
	require.ErrorIs(t, err, service.ErrImageJobReservationInsufficient)
}

func TestImageJobBillingSettlementUsesStableRequestID(t *testing.T) {
	gateway, usageRepo := newImageBillingGateway(t)
	billing := NewImageJobBilling(gateway, 1.0)
	_, err := billing.Settle(context.Background(), settlementFixture("imgjob_x", 0, 1))
	require.NoError(t, err)
	_, err = billing.Settle(context.Background(), settlementFixture("imgjob_x", 0, 1))
	require.NoError(t, err)
	require.Equal(t, 1, usageRepo.ApplyCount("imgjob_x:0"))
}
```

- [ ] **Step 2: 运行测试并确认红灯**

Run: `cd backend && go test ./internal/service -run 'ImageJobBilling' -count=1`

Run: `cd backend && go test -tags=integration ./internal/repository -run 'HeldBalanceOvercommit' -count=1`

Expected: FAIL，分别缺少 `NewImageJobBilling` 和预留检查。

- [ ] **Step 3: 实现费用上限估算**

```go
type ImageJobReservation struct {
	AmountUSD float64
	BillingType int8
	SubscriptionID *int64
}

func (b *ImageJobBilling) Estimate(ctx context.Context, apiKey *APIKey, sub *UserSubscription, req ImageJobRequest) (ImageJobReservation, error)
```

图片按次或按尺寸定价时，用现有 `calculateOpenAIImageCost` 对最大输出数计算精确上限；渠道为 token 计费时使用 `gateway.image_jobs.max_reservation_usd`。sequence 的输出数等于 `len(scenes)`。估算值小于等于 0 或超过配置上限都拒绝创建，错误码为 `image_job_reservation_unavailable`。

- [ ] **Step 4: 在创建事务中实施真实预留**

余额模式先 `SELECT balance FROM users WHERE id=$1 FOR UPDATE`，再减去该用户所有 `reservation_status='held'` 的 Job 金额；API key quota 同理用 `quota-quota_used-held`。订阅模式锁定 `user_subscriptions` 和 `groups`，分别验证日/周/月 `usage + held + requested <= limit`。不直接扣余额，预留以 Job 行的 `reserved_usd` 和 `reservation_status='held'` 表示。

- [ ] **Step 5: 为 RecordUsage 增加返回成本的兼容入口**

保留现有签名并委托新方法，避免修改所有调用者：

```go
func (s *OpenAIGatewayService) RecordUsage(ctx context.Context, in *OpenAIRecordUsageInput) error {
	_, err := s.RecordUsageWithCost(ctx, in)
	return err
}

func (s *OpenAIGatewayService) RecordUsageWithCost(ctx context.Context, in *OpenAIRecordUsageInput) (*CostBreakdown, error)
```

`ImageJobBilling.Settle(ctx, ImageJobSettlementInput)` 接收 Job、frame index、`ImageExecutionResult` 和已经持久化的 result count；返回 `*CostBreakdown`。它使用稳定 request ID：batch 为 `{job_id}:0`，sequence 每帧为 `{job_id}:{index}`。对象已经成功写入后才调用结算；无可下载结果时只释放预留。`usage_billing_dedup` 保证进程在扣费后、更新 Job 前崩溃也不会重复扣款。

- [ ] **Step 6: 运行计费回归并提交**

Run: `cd backend && go test ./internal/service -run 'ImageJobBilling|RecordUsage' -count=1`

Run: `cd backend && go test -tags=integration ./internal/repository -run 'ImageJobRepository.*Reserve' -count=1`

Expected: PASS；重复 settlement 只产生一次扣费，failed/canceled 释放全部 held 金额，partial 只结算已持久化帧。

```bash
git add backend/internal/service/image_job_billing.go backend/internal/service/image_job_billing_test.go backend/internal/repository/image_job_repo.go backend/internal/repository/image_job_repo_integration_test.go backend/internal/service/openai_gateway_service.go backend/internal/service/openai_gateway_record_usage_test.go
git commit -m "feat: reserve and settle async image costs"
```

### Task 8: 实现 Job 创建、所有权查询和取消服务

**Files:**
- Create: `backend/internal/service/image_job_service.go`
- Create: `backend/internal/service/image_job_service_test.go`

- [ ] **Step 1: 写创建重放、归属隔离和取消测试**

```go
func TestImageJobServiceCreateReplayAndOwnership(t *testing.T) {
	svc, deps := newImageJobServiceFixture(t)
	first, replayed, err := svc.Create(context.Background(), deps.CreateInput("same-key"))
	require.NoError(t, err)
	require.False(t, replayed)
	second, replayed, err := svc.Create(context.Background(), deps.CreateInput("same-key"))
	require.NoError(t, err)
	require.True(t, replayed)
	require.Equal(t, first.PublicID, second.PublicID)
	_, err = svc.GetOwned(context.Background(), first.PublicID, first.APIKeyID+1)
	require.ErrorIs(t, err, ErrImageJobNotFound)
}
```

- [ ] **Step 2: 运行测试并确认红灯**

Run: `cd backend && go test ./internal/service -run TestImageJobService -count=1`

Expected: FAIL，错误包含 `undefined: newImageJobServiceFixture`。

- [ ] **Step 3: 实现创建输入、public ID 和清理回滚**

```go
type CreateImageJobInput struct {
	APIKey *APIKey
	Subscription *UserSubscription
	Parsed *OpenAIImagesRequest
	Scenes []string
	Mode string
	IdempotencyKey string
	MappedModel string
	ChannelMapping ChannelMappingResult
}

func NewImageJobPublicID() string {
	return "imgjob_" + strings.ReplaceAll(uuid.NewString(), "-", "")
}
```

创建顺序固定为：验证功能启用和存储健康、验证 group 图片权限、计算内容摘要、估算预留、上传输入、`CreateReserved`。`Idempotency-Key` 去除首尾空白后限制为 1–255 字节，数据库只保存 `sha256(apiKeyID + "\x00" + key)`；没有 header 时该字段为 nil。幂等竞争输掉或数据库写入失败时删除本次 public ID 下已经上传的对象。Job 只保存 API key ID，不保存 key 字符串。

- [ ] **Step 4: 实现查询 DTO 和取消语义**

```go
type ImageJobResponse struct {
	ID string `json:"id"`
	Object string `json:"object"`
	Status ImageJobStatus `json:"status"`
	Operation string `json:"operation"`
	Mode string `json:"mode,omitempty"`
	Model string `json:"model"`
	RequestedCount int `json:"requested_count"`
	CompletedCount int `json:"completed_count"`
	Data []ImageJobResultResponse `json:"data"`
	Error *ImageJobError `json:"error,omitempty"`
	CreatedAt int64 `json:"created_at"`
	StartedAt *int64 `json:"started_at,omitempty"`
	ExpiresAt int64 `json:"expires_at"`
}

type ImageJobResultResponse struct {
	Index int `json:"index"`
	Status string `json:"status"`
	URL string `json:"url"`
	MIMEType string `json:"mime_type"`
	Size string `json:"size,omitempty"`
	RevisedPrompt string `json:"revised_prompt,omitempty"`
}
```

`GetOwned` 和 `CancelOwned` 的 SQL 都同时匹配 `public_id` 与当前 `api_key_id`，不泄露其他 key 的 Job 是否存在。queued 取消立即标为 canceled、写 `canceled_at` 并释放预留；running 只写 `cancel_requested_at`，worker 通过 `IsCancelRequested` 在上游调用前或帧之间检查并停止；终态 DELETE 返回 409，已 canceled 返回成功。另实现只供 AdminAuth handler 调用的 `GetAdmin` 和 `CancelAdmin`，它们不接受客户端提供的角色字段。

- [ ] **Step 5: 运行服务测试并提交**

Run: `cd backend && go test ./internal/service -run TestImageJobService -count=1`

Expected: PASS。

```bash
git add backend/internal/service/image_job_service.go backend/internal/service/image_job_service_test.go backend/internal/service/image_job.go
git commit -m "feat: add image job lifecycle service"
```

### Task 9: 暴露异步创建、sequence、查询、下载和取消 API

**Files:**
- Create: `backend/internal/handler/openai_image_jobs.go`
- Create: `backend/internal/handler/openai_image_jobs_test.go`
- Create: `backend/internal/handler/admin/image_job_handler.go`
- Create: `backend/internal/handler/admin/image_job_handler_test.go`
- Modify: `backend/internal/handler/openai_images.go`
- Modify: `backend/internal/handler/openai_gateway_handler.go`
- Modify: `backend/internal/handler/handler.go`
- Modify: `backend/internal/handler/wire.go`
- Modify: `backend/internal/server/routes/gateway.go`
- Modify: `backend/internal/server/routes/admin.go`

- [ ] **Step 1: 写 `Prefer` 分流和 202 响应失败测试**

```go
func TestOpenAIImagesRespondAsyncReturnsImmediately(t *testing.T) {
	router, jobs := newImageJobHandlerRouter(t)
	req := httptest.NewRequest(http.MethodPost, "/v1/images/generations", strings.NewReader(`{"model":"gpt-image-2","prompt":"city","n":4}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Prefer", "wait=10, respond-async")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	require.Equal(t, http.StatusAccepted, w.Code)
	require.Equal(t, "respond-async", w.Header().Get("Preference-Applied"))
	require.Equal(t, "/v1/images/jobs/imgjob_test", w.Header().Get("Location"))
	require.Equal(t, 1, jobs.CreateCalls())
}
```

- [ ] **Step 2: 运行测试并确认红灯**

Run: `cd backend && go test ./internal/handler -run 'TestOpenAIImagesRespondAsync' -count=1`

Expected: FAIL，现有 handler 尝试同步选账号或返回非 202。

- [ ] **Step 3: 实现标准端点的异步分支**

新增 `prefersRespondAsync`，按逗号拆 token、忽略大小写和周围空格。现有 `Images` 在完成 body 读取、parser、分组权限、内容审核、channel mapping 和 billing eligibility 后，在获取长任务并发槽和账号选择之前调用 Job service；`CreateImageJobInput` 同时携带 mapped model 和 channel usage fields。成功响应：

```go
c.Header("Preference-Applied", "respond-async")
c.Header("Location", "/v1/images/jobs/"+job.PublicID)
c.JSON(http.StatusAccepted, job.ToResponse())
```

未携带该 preference 时继续原同步/SSE 路径，现有测试响应字节保持不变。

- [ ] **Step 4: 实现 sequence 请求解析和路由**

`POST /v1/images/sequences` 固定异步。JSON 和 multipart 都接受 `model/prompt/scenes/size/quality/output_format/input_fidelity`，multipart 重复 `image` 或 `image[]`。scenes 必须为 2–4 个非空字符串；有参考图时验证 `len(inputs)+1 <= max_input_images`，保证后续还能加入上一帧。

- [ ] **Step 5: 实现查询、下载和取消 handler**

注册：

```go
gateway.POST("/images/sequences", h.OpenAIGateway.ImageSequence)
gateway.GET("/images/jobs/:job_id", h.OpenAIGateway.GetImageJob)
gateway.GET("/images/jobs/:job_id/results/:index", h.OpenAIGateway.GetImageJobResult)
gateway.DELETE("/images/jobs/:job_id", h.OpenAIGateway.CancelImageJob)
```

下载先按 API key ownership 查询 result 元数据，再从 store 取对象，设置 `Content-Type`、`Content-Length`、`Cache-Control: private, max-age=300` 和 `Content-Disposition: inline`。数据库无记录、对象缺失或 Job expired 分别返回稳定 404/410 错误，不返回 bucket key。

`AdminHandlers` 增加 `ImageJob *admin.ImageJobHandler`，`ProvideAdminHandlers` 接收并保存 `admin.NewImageJobHandler(imageJobService)`。AdminAuth 路由另外注册 `GET /api/v1/admin/image-jobs/:job_id`、`GET /api/v1/admin/image-jobs/:job_id/results/:index` 和 `DELETE /api/v1/admin/image-jobs/:job_id`。管理员 handler 从 `GetAuthSubjectFromContext` 和 `GetUserRoleFromContext` 获取服务端认证结果，仅 `RoleAdmin` 可调用 `GetAdmin/CancelAdmin`；下载仍由 Sub2API 返回图片字节，不暴露对象存储凭据。

- [ ] **Step 6: 运行 handler 和路由测试并提交**

Run: `cd backend && go test ./internal/handler ./internal/server/routes -run 'Image(Job|Sequence|RespondAsync)' -count=1`

Expected: PASS；额外断言未带 Prefer 的请求仍进入同步 stub。

```bash
git add backend/internal/handler/openai_image_jobs.go backend/internal/handler/openai_image_jobs_test.go backend/internal/handler/admin/image_job_handler.go backend/internal/handler/admin/image_job_handler_test.go backend/internal/handler/openai_images.go backend/internal/handler/openai_gateway_handler.go backend/internal/handler/handler.go backend/internal/handler/wire.go backend/internal/server/routes/gateway.go backend/internal/server/routes/admin.go
git commit -m "feat: expose async image job APIs"
```

### Task 10: 严格执行图片高级参数能力矩阵

**Files:**
- Modify: `backend/internal/service/openai_images.go`
- Modify: `backend/internal/service/openai_images_responses.go`
- Modify: `backend/internal/service/openai_images_test.go`
- Modify: `backend/internal/service/account.go`
- Modify: `backend/internal/service/openai_account_scheduler.go`
- Modify: `backend/internal/service/openai_account_scheduler_test.go`

- [ ] **Step 1: 把现有静默忽略测试改成明确拒绝测试**

```go
func TestBuildOpenAIImagesResponsesRequestRejectsUnsupportedInputFidelity(t *testing.T) {
	_, err := buildOpenAIImagesResponsesRequest(&OpenAIImagesRequest{
		Endpoint: openAIImagesEditsEndpoint, Model: "gpt-image-2", Prompt: "edit",
		InputImageURLs: []string{"https://example.test/a.png"}, InputFidelity: "high",
	}, "gpt-image-2")
	require.EqualError(t, err, "OAuth image bridge does not support input_fidelity")
}

func TestNativeImageRequestDoesNotFallBackToBridgeAccount(t *testing.T) {
	// group 只有 OAuth 账号，显式 input_fidelity=high 必须 no compatible account。
}
```

- [ ] **Step 2: 运行测试并确认红灯**

Run: `cd backend && go test ./internal/service -run 'UnsupportedInputFidelity|NativeImageRequestDoesNotFallBack' -count=1`

Expected: FAIL；当前 builder 丢弃字段，scheduler 会从 native 降级到 basic。

- [ ] **Step 3: 修正账号能力和 scheduler**

保留 `OpenAIImagesCapabilityBasic` 表示 Responses bridge，`OpenAIImagesCapabilityNative` 表示原生 Images API。能力矩阵改为：OAuth 仅 basic，API key 同时支持 basic/native；`SelectAccountWithSchedulerForImages` 删除 native 失败后自动选择 basic 的分支。

```go
func (a *Account) SupportsOpenAIImageCapability(cap OpenAIImagesCapability) bool {
	if cap == "" { return true }
	if !a.IsOpenAI() { return false }
	switch cap {
	case OpenAIImagesCapabilityBasic: return a.Type == AccountTypeOAuth || a.Type == AccountTypeAPIKey
	case OpenAIImagesCapabilityNative: return a.Type == AccountTypeAPIKey
	default: return false
	}
}
```

`input_fidelity`、mask、multipart 多图、显式 native 参数和 `n>1` 继续分类为 native。bridge builder 对所有不能发送的显式字段返回 `unsupported_parameter`，不能省略后继续请求。`background=transparent` 仍在 native 路径原样透传，上游错误原样映射。

- [ ] **Step 4: 审查能力改动并运行调度回归**

Run: `git diff -- backend/internal/service/openai_account_scheduler.go backend/internal/service/openai_gateway_service.go`

Run: `git -C /home/ubuntu/sub2api diff -- backend/internal/service/openai_account_scheduler.go backend/internal/service/openai_gateway_service.go`

Expected: 独立 worktree 中本任务只修改图片 capability 选择；第二条命令显示的原工作树 fallback diff 与实施前记录一致。

Run: `cd backend && go test ./internal/service -run 'OpenAI(Image|AccountScheduler)' -count=1`

Expected: PASS；OAuth basic 仍可用，native 请求无 API key 账号时明确失败。

- [ ] **Step 5: 提交能力矩阵修复**

```bash
git add backend/internal/service/openai_images.go backend/internal/service/openai_images_responses.go backend/internal/service/openai_images_test.go backend/internal/service/account.go backend/internal/service/openai_account_scheduler.go backend/internal/service/openai_account_scheduler_test.go
git commit -m "fix: enforce image parameter capabilities"
```

### Task 11: 把图片响应解析重构为可复用 result sink

**Files:**
- Create: `backend/internal/service/openai_images_sink.go`
- Create: `backend/internal/service/openai_images_sink_test.go`
- Modify: `backend/internal/service/openai_images.go`
- Modify: `backend/internal/service/openai_images_responses.go`
- Modify: `backend/internal/service/openai_images_test.go`

- [ ] **Step 1: 写非流式、SSE 和 HTTP wire contract 失败测试**

```go
func TestCollectingImageResultSinkReceivesFourFinalImages(t *testing.T) {
	sink := NewCollectingImageResultSink()
	for i := 0; i < 4; i++ {
		require.NoError(t, sink.Final(context.Background(), ImageArtifact{Index: i, Data: []byte{byte(i)}, MIMEType: "image/png"}))
	}
	require.Len(t, sink.Results(), 4)
}

func TestHTTPImageResultSinkPreservesOpenAIJSON(t *testing.T) {
	w := httptest.NewRecorder()
	sink := NewHTTPImageResultSink(w, ImageSinkOptions{ResponseFormat: "b64_json"})
	require.NoError(t, sink.Final(context.Background(), ImageArtifact{Index: 0, Data: []byte("png"), MIMEType: "image/png"}))
	require.NoError(t, sink.Complete(context.Background(), ImageExecutionSummary{CreatedAt: 123}))
	require.JSONEq(t, `{"created":123,"data":[{"b64_json":"cG5n"}]}`, w.Body.String())
}
```

- [ ] **Step 2: 运行测试并确认红灯**

Run: `cd backend && go test ./internal/service -run 'ImageResultSink|HTTPImageResultSink' -count=1`

Expected: FAIL，缺少 sink 类型。

- [ ] **Step 3: 定义 artifact、summary 和 sink 接口**

```go
type ImageArtifact struct {
	Index int
	Data []byte
	MIMEType string
	Width int
	Height int
	SizeTier string
	RevisedPrompt string
	UpstreamOutputID string
}

type ImageExecutionSummary struct {
	CreatedAt int64
	Usage OpenAIUsage
	ForwardResult *OpenAIForwardResult
}

type ImageSinkOptions struct {
	Stream bool
	ResponseFormat string
	OutputFormat string
}

type ImageResultSink interface {
	Partial(ctx context.Context, index int, data []byte, mimeType string) error
	Final(ctx context.Context, image ImageArtifact) error
	Complete(ctx context.Context, summary ImageExecutionSummary) error
}
```

`CollectingImageResultSink` 用于测试和 sequence 中间帧；`HTTPImageResultSink` 支持 JSON 与 SSE，并沿用现有 keepalive、event name、response headers 和上游错误格式。

- [ ] **Step 4: 把 API key 和 OAuth 两条解析路径改为输出 artifact**

非流式 body 通过现有 `collectOpenAIImagePointers` 和 `resolveOpenAIImageBytes` 得到真实字节；SSE 在 final event 时解码 Base64 或下载 URL；OAuth Responses 使用现有 `openAIResponsesImageResult`。partial 只进入 `Partial`，不得计入 completed count 或计费。

下列函数签名成为唯一解析入口：

```go
func (s *OpenAIGatewayService) consumeOpenAIImagesResponse(ctx context.Context, resp *http.Response, parsed *OpenAIImagesRequest, sink ImageResultSink) (*OpenAIForwardResult, error)
func (s *OpenAIGatewayService) consumeOpenAIImagesResponsesSSE(ctx context.Context, resp *http.Response, parsed *OpenAIImagesRequest, sink ImageResultSink) (*OpenAIForwardResult, error)
```

- [ ] **Step 5: 运行现有同步协议回归**

Run: `cd backend && go test ./internal/service -run 'OpenAIImages' -count=1`

Expected: PASS；JSON、URL response_format、SSE partial/final/completed、OAuth 和 API key fixture 的响应保持原测试预期。

- [ ] **Step 6: 提交 sink 重构**

```bash
git add backend/internal/service/openai_images_sink.go backend/internal/service/openai_images_sink_test.go backend/internal/service/openai_images.go backend/internal/service/openai_images_responses.go backend/internal/service/openai_images_test.go
git commit -m "refactor: expose reusable image result sinks"
```

### Task 12: 提取非 HTTP 图片执行器和统一故障转移

**Files:**
- Create: `backend/internal/service/openai_images_executor.go`
- Create: `backend/internal/service/openai_images_executor_test.go`
- Modify: `backend/internal/handler/openai_images.go`
- Modify: `backend/internal/handler/openai_images_failover_test.go`
- Modify: `backend/internal/service/openai_images.go`

- [ ] **Step 1: 写账号切换、槽位释放和无响应 writer 测试**

```go
func TestOpenAIImageExecutorFailsOverBeforeAnyFinalImage(t *testing.T) {
	exec, upstream := newImageExecutorFixture(t, 500, 200)
	sink := NewCollectingImageResultSink()
	result, err := exec.Execute(context.Background(), imageExecutionFixture(4), sink)
	require.NoError(t, err)
	require.Equal(t, []int{500, 200}, upstream.Statuses())
	require.Len(t, sink.Results(), 4)
	require.Equal(t, 2, upstream.CallCount())
}

func TestOpenAIImageExecutorDoesNotFailOverAfterFinalImage(t *testing.T) {
	exec, upstream := newPartialThenErrorExecutorFixture(t)
	_, err := exec.Execute(context.Background(), imageExecutionFixture(4), NewCollectingImageResultSink())
	require.Error(t, err)
	require.Equal(t, 1, upstream.CallCount())
}
```

- [ ] **Step 2: 运行测试并确认红灯**

Run: `cd backend && go test ./internal/service -run TestOpenAIImageExecutor -count=1`

Expected: FAIL，缺少 executor。

- [ ] **Step 3: 定义执行输入和输出**

```go
type ImageExecutionInput struct {
	APIKey *APIKey
	User *User
	Subscription *UserSubscription
	Body []byte
	Parsed *OpenAIImagesRequest
	ChannelMapping ChannelMappingResult
	SessionHash string
	InboundEndpoint string
	UserAgent string
	IPAddress string
	RequestPayloadHash string
}

type ImageExecutionResult struct {
	Forward *OpenAIForwardResult
	Account *Account
	ChannelMapping ChannelMappingResult
}

type ImageExecutor interface {
	Execute(ctx context.Context, input ImageExecutionInput, sink ImageResultSink) (*ImageExecutionResult, error)
}
```

`OpenAIImageExecutor` 依赖 `OpenAIGatewayService` 和 `ConcurrencyService`，不依赖 Gin。它先获取进程图片 semaphore 和用户槽，再调用 `SelectAccountWithSchedulerForImages`，处理 selection 自带槽或 wait plan，执行同账号重试、账号切换、调度结果上报和 release。semaphore 在 `gateway.image_concurrency.enabled=true` 时使用其 `max_concurrent_requests`，因此异步 worker 仍受现有全局图片并发限制。错误统一为 `ImageExecutionError{Status,Type,Code,Message,Retryable,Indeterminate}`，写 Job 前必须经过现有 `sanitizeUpstreamErrorMessage` 并截断到 500 字节。

- [ ] **Step 4: 把现有 handler 改为调用 executor**

同步 handler 继续负责认证、审核、HTTP 错误映射和全局图片 limiter，随后构造 `HTTPImageResultSink` 调 executor。把原来 `openai_images.go` handler 中的账号选择循环删除，确保 handler 和 worker 不再维护两份 switch/retry 逻辑。

- [ ] **Step 5: 运行 service 与 handler 故障转移回归**

Run: `cd backend && go test ./internal/service ./internal/handler -run 'ImageExecutor|Images.*Failover|OpenAIImages' -count=1`

Expected: PASS；上游未产出 final 时可切换，产出至少一张后不切换，所有 user/account slot 都执行一次 release。

- [ ] **Step 6: 提交执行器重构**

```bash
git add backend/internal/service/openai_images_executor.go backend/internal/service/openai_images_executor_test.go backend/internal/service/openai_images.go backend/internal/handler/openai_images.go backend/internal/handler/openai_images_failover_test.go
git commit -m "refactor: share image execution and failover"
```

### Task 13: 实现 worker 和单次 `n=4` batch

**Files:**
- Create: `backend/internal/service/image_job_worker.go`
- Create: `backend/internal/service/image_job_worker_test.go`
- Create: `backend/internal/service/image_job_metrics.go`
- Modify: `backend/internal/service/image_job_service.go`

- [ ] **Step 1: 写 batch 一次上游调用和逐张持久化失败测试**

```go
func TestImageJobWorkerBatchN4UsesOneExecution(t *testing.T) {
	svc, deps := newImageJobWorkerFixture(t)
	job := deps.QueueGeneration(t, 4)
	deps.Executor.ReturnFinalImages(4)
	svc.runOnce(context.Background(), "worker-1")
	require.Equal(t, 1, deps.Executor.CallCount())
	require.Equal(t, 4, deps.Executor.Inputs()[0].Parsed.N)
	got := deps.Repository.MustGet(t, job.PublicID)
	require.Equal(t, ImageJobStatusCompleted, got.Status)
	require.Equal(t, 4, got.CompletedCount)
	require.Len(t, deps.Store.KeysWithPrefix(job.PublicID+"/results/"), 4)
}
```

- [ ] **Step 2: 运行测试并确认红灯**

Run: `cd backend && go test ./internal/service -run TestImageJobWorkerBatchN4UsesOneExecution -count=1`

Expected: FAIL，缺少 worker。

- [ ] **Step 3: 实现 worker 生命周期、认领和 heartbeat**

`Start` 启动 `worker_concurrency` 个 goroutine；每个 goroutine按 `poll_interval_milliseconds` 认领，空队列等待 ticker。每个 Job 使用 `task_timeout_seconds` context；heartbeat 独立 ticker 每 `heartbeat_interval_seconds` 调 repository。`Stop` cancel 后等待 `sync.WaitGroup`。

执行前必须用 API key ID 重新加载 key/user/group，检查 active、未过期、group 仍允许图片并重新检查订阅资格。失败发生在 `MarkUpstreamStarted` 前可安全 failed/released；调用 executor 前立即把 phase 改为 upstream。创建时解析出的 channel mapping 写入 `mapped_model`，worker 重新解析后若映射策略已变化则使用新值并通过 repository 更新审计字段。

- [ ] **Step 4: 实现异步持久化 sink**

```go
type JobImageResultSink struct {
	job *ImageJob
	attemptID string
	repo ImageJobRepository
	store ImageJobObjectStore
}
```

`Final` 校验 index 在 `[0, requested_count)`，按 MIME 选择扩展名，写 `image-jobs/{api_key_id}/{public_id}/results/{index}.{ext}`，成功后 `UpsertResult` 并递增 completed count。对象写失败不创建 result，也不计费。重复 final 使用相同 key 和唯一 `(job_id,index)` upsert，不能重复进度。

- [ ] **Step 5: 实现 batch 完成、partial 和 indeterminate**

executor 完成且结果数等于 requested count -> completed；已有结果后明确错误 -> partial；无结果的明确错误 -> failed；context 在 phase=upstream 且无法确认上游结果 -> indeterminate。worker 不自动重新运行 indeterminate。

- [ ] **Step 6: 运行 worker 测试并提交**

Run: `cd backend && go test ./internal/service -run 'ImageJobWorker|JobImageResultSink' -count=1`

Expected: PASS；另外覆盖非流式一次返回四张、SSE 四个 final、两个 worker 只有一个执行、heartbeat 和取消。

```bash
git add backend/internal/service/image_job_worker.go backend/internal/service/image_job_worker_test.go backend/internal/service/image_job_metrics.go backend/internal/service/image_job_service.go
git commit -m "feat: execute async image batches"
```

### Task 14: 锁定 edits、mask 和多图异步语义

**Files:**
- Modify: `backend/internal/service/image_job_worker_test.go`
- Modify: `backend/internal/service/image_job_request.go`
- Modify: `backend/internal/service/image_job_request_test.go`
- Modify: `backend/internal/service/openai_images_executor_test.go`

- [ ] **Step 1: 写四种 edits worker 失败测试**

加入表驱动用例：单图 edit、图加 mask、三图 compose、JSON URL images + mask URL。每个用例断言 executor 收到 endpoint `/v1/images/edits`、输入顺序、mask 单独字段、prompt 和 `input_fidelity=high`。

```go
tests := []struct{
	name string
	inputCount int
	hasMask bool
}{
	{"single_edit", 1, false},
	{"masked_inpaint", 1, true},
	{"three_image_compose", 3, false},
	{"url_images_and_mask", 2, true},
}
```

- [ ] **Step 2: 运行测试并确认红灯**

Run: `cd backend && go test ./internal/service -run 'TestImageJobWorkerEdits' -count=1`

Expected: 至少一个用例 FAIL，指出重建字段名、mask 或 URL 输入未保留。

- [ ] **Step 3: 修正请求重建的精确字段行为**

multipart 的普通图片沿用原 `FieldName`，只接受 `image` 和 `image[]`；mask 始终使用 `mask`；JSON edit 使用现有 `images:[{"image_url":...}]` 和 `mask:{"image_url":...}`。对输入计数先执行管理员上限，再执行适配器上限；超限返回 400，不截断。

- [ ] **Step 4: 运行 parser、executor 和 worker 回归**

Run: `cd backend && go test ./internal/service -run 'OpenAIImages|ImageJob(Request|WorkerEdits|Executor)' -count=1`

Expected: PASS；每种 Job 只执行一次对应上游 edit 请求。

- [ ] **Step 5: 提交 edits 覆盖**

```bash
git add backend/internal/service/image_job_worker_test.go backend/internal/service/image_job_request.go backend/internal/service/image_job_request_test.go backend/internal/service/openai_images_executor_test.go
git commit -m "test: lock async image edit semantics"
```

### Task 15: 实现 continuity 连续场景编排

**Files:**
- Create: `backend/internal/service/image_job_sequence.go`
- Create: `backend/internal/service/image_job_sequence_test.go`
- Modify: `backend/internal/service/image_job_worker.go`
- Modify: `backend/internal/service/image_job_worker_test.go`

- [ ] **Step 1: 写无参考图的一次 generation 加三次 edit 测试**

```go
func TestImageSequenceWithoutReferencesUsesAnchorAndPrevious(t *testing.T) {
	seq, exec := newSequenceFixture(t, nil, []string{"s1", "s2", "s3", "s4"})
	require.NoError(t, seq.Execute(context.Background()))
	require.Equal(t, []string{"generation", "edit", "edit", "edit"}, exec.Operations())
	require.Equal(t, []string{"frame-0", "frame-0"}, exec.InputObjectIDs(1))
	require.Equal(t, []string{"frame-0", "frame-1"}, exec.InputObjectIDs(2))
	require.Equal(t, []string{"frame-0", "frame-2"}, exec.InputObjectIDs(3))
}
```

- [ ] **Step 2: 写有参考图的四次 edit 测试**

```go
func TestImageSequenceWithReferencesUsesEveryAnchor(t *testing.T) {
	seq, exec := newSequenceFixture(t, []string{"ref-a", "ref-b"}, []string{"s1", "s2", "s3", "s4"})
	require.NoError(t, seq.Execute(context.Background()))
	require.Equal(t, []string{"edit", "edit", "edit", "edit"}, exec.Operations())
	require.Equal(t, []string{"ref-a", "ref-b"}, exec.InputObjectIDs(0))
	require.Equal(t, []string{"ref-a", "ref-b", "frame-0"}, exec.InputObjectIDs(1))
}
```

- [ ] **Step 3: 运行测试并确认红灯**

Run: `cd backend && go test ./internal/service -run 'TestImageSequence' -count=1`

Expected: FAIL，缺少 sequence 编排器。

- [ ] **Step 4: 实现确定性的帧请求构造**

每帧 prompt 使用 `global prompt + "\n\nScene " + strconv.Itoa(index+1) + ": " + scene`。没有用户参考图时 Frame 0 generation；其 artifact 同时成为固定 anchor。存在用户参考图时 Frame 0 edit。Frame 1 以后固定 anchors 在前、上一帧在最后；默认 `input_fidelity=high`，用户显式值优先。

```go
type ImageSequenceExecutor struct {
	job *ImageJob
	executor ImageExecutor
	sinkFactory func(index int) ImageResultSink
	store ImageJobObjectStore
}
```

每帧 `n=1` 串行等待，完成立即存储并结算。帧间检查 canceled；第 k 帧失败而前面已有结果时 Job 为 partial。任何时候都不调用文本模型补写 scenes。

- [ ] **Step 5: 验证上限、partial 和费用行为**

Run: `cd backend && go test ./internal/service -run 'ImageSequence|ImageJobWorker.*Sequence' -count=1`

Expected: PASS；覆盖 references+previous 超上限创建失败、第二帧失败得到 partial、每帧稳定 billing request ID、用户 reference 顺序不变。

- [ ] **Step 6: 提交 continuity**

```bash
git add backend/internal/service/image_job_sequence.go backend/internal/service/image_job_sequence_test.go backend/internal/service/image_job_worker.go backend/internal/service/image_job_worker_test.go
git commit -m "feat: orchestrate continuous image sequences"
```

### Task 16: 实现结果 TTL、对象清理和 stale 恢复

**Files:**
- Create: `backend/internal/service/image_job_cleanup.go`
- Create: `backend/internal/service/image_job_cleanup_test.go`
- Modify: `backend/internal/repository/image_job_repo.go`
- Modify: `backend/internal/repository/image_job_repo_integration_test.go`
- Modify: `backend/internal/service/image_job_worker.go`

- [ ] **Step 1: 写对象全部删除后才 expired 的失败测试**

```go
func TestImageJobCleanupExpiresOnlyAfterObjectsDeleted(t *testing.T) {
	cleanup, deps := newImageJobCleanupFixture(t)
	job := deps.ExpiredCompletedJobWithInputMaskAndResults(t, 2)
	require.NoError(t, cleanup.RunOnce(context.Background()))
	require.Empty(t, deps.Store.KeysWithPrefix(job.PublicID))
	require.Equal(t, ImageJobStatusExpired, deps.Repository.MustGet(t, job.PublicID).Status)
}

func TestImageJobCleanupKeepsStateWhenDeleteFails(t *testing.T) {
	cleanup, deps := newImageJobCleanupFixture(t)
	job := deps.ExpiredCompletedJobWithInputMaskAndResults(t, 1)
	deps.Store.FailDeleteAt(1)
	require.Error(t, cleanup.RunOnce(context.Background()))
	require.Equal(t, ImageJobStatusCompleted, deps.Repository.MustGet(t, job.PublicID).Status)
}
```

- [ ] **Step 2: 运行测试并确认红灯**

Run: `cd backend && go test ./internal/service -run TestImageJobCleanup -count=1`

Expected: FAIL，缺少 cleanup service。

- [ ] **Step 3: 实现批量清理和幂等删除**

`ListExpired(now, 100)` 只返回终态且 `expires_at <= now` 的 Job，同时带 inputs/results object keys。删除不存在对象视为成功；所有对象删除成功后用 CAS 把原终态改成 expired，并清空下载元数据但保留 Job 审计字段。单个 Job 删除失败时保留状态供下轮重试，并记录对象存储错误指标。

- [ ] **Step 4: 实现启动和周期 stale 恢复**

worker `Start` 首先调用 `RecoverStale(now-task_timeout)`；之后每分钟运行一次。preflight stale 回到 queued 并清除 attempt/worker；upstream stale 进入 indeterminate、释放尚未结算的预留但保留已结算结果。清理 ticker 每 5 分钟运行一次，并共用 worker cancel context。

- [ ] **Step 5: 运行 cleanup 与恢复测试并提交**

Run: `cd backend && go test ./internal/service ./internal/repository -run 'ImageJob(Cleanup|RecoverStale)' -count=1`

Expected: PASS。

```bash
git add backend/internal/service/image_job_cleanup.go backend/internal/service/image_job_cleanup_test.go backend/internal/service/image_job_worker.go backend/internal/repository/image_job_repo.go backend/internal/repository/image_job_repo_integration_test.go
git commit -m "feat: expire and recover image jobs"
```

### Task 17: 完成依赖注入、生命周期和运行指标

**Files:**
- Modify: `backend/internal/repository/wire.go`
- Modify: `backend/internal/service/wire.go`
- Modify: `backend/internal/handler/wire.go`
- Modify: `backend/internal/handler/openai_gateway_handler.go`
- Modify: `backend/cmd/server/wire.go`
- Modify: `backend/cmd/server/wire_gen.go`
- Modify: `backend/cmd/server/wire_gen_test.go`
- Modify: `backend/internal/service/image_job_metrics.go`
- Create: `backend/internal/service/image_job_metrics_test.go`

- [ ] **Step 1: 写 provider 启停和指标失败测试**

```go
func TestImageJobMetricsSnapshot(t *testing.T) {
	m := &ImageJobMetrics{}
	m.JobCreated("batch")
	m.JobClaimed(250 * time.Millisecond)
	m.JobFinished(ImageJobStatusCompleted, 4, 2*time.Second, 0.15)
	m.AccountSwitched()
	m.StorageError()
	s := m.Snapshot()
	require.Equal(t, uint64(1), s.CreatedBatch)
	require.Equal(t, uint64(1), s.Completed)
	require.Equal(t, uint64(4), s.Outputs)
	require.Equal(t, uint64(250), s.QueueDurationMilliseconds)
	require.Equal(t, uint64(2000), s.ExecutionDurationMilliseconds)
	require.Equal(t, int64(150000), s.ReservationDeltaMicroUSD)
	require.Equal(t, uint64(1), s.AccountSwitches)
	require.Equal(t, uint64(1), s.StorageErrors)
}
```

在 `wire_gen_test.go` 的 cleanup fixture 增加 fake ImageJobService，断言 application cleanup 调用一次 `Stop()`。指标实现还必须提供 process-local queued gauge、started、completed/partial/failed/indeterminate/canceled、queue/execute duration sample count、outputs、storage errors、account switches 和 reservation-vs-settlement delta；状态页以后可读取 `Snapshot()`，本任务不引入 Prometheus 依赖。

- [ ] **Step 2: 运行测试并确认红灯**

Run: `cd backend && go test ./internal/service ./cmd/server -run 'ImageJobMetrics|WireDependencies' -count=1`

Expected: FAIL，缺少 metrics 方法或新的 Wire 参数。

- [ ] **Step 3: 增加 provider 并启动 worker**

```go
func ProvideImageJobService(
	repo ImageJobRepository,
	store ImageJobObjectStore,
	executor *OpenAIImageExecutor,
	apiKeys *APIKeyService,
	subscriptions *SubscriptionService,
	billing *ImageJobBilling,
	cfg *config.Config,
) *ImageJobService {
	svc := NewImageJobService(repo, store, executor, apiKeys, subscriptions, billing, cfg)
	svc.Start()
	return svc
}
```

repository provider 创建配置对应的 object store；功能关闭时返回一个 disabled store，不接触 S3。`NewOpenAIGatewayHandler` 增加 `imageJobs *ImageJobService` 参数。`provideCleanup` 增加 ImageJobService 并在关闭 PostgreSQL/Redis 之前执行 `Stop()`。

- [ ] **Step 4: 生成 Wire 并审查重叠文件**

Run: `cd backend && make generate`

Expected: Wire 成功生成，无 missing provider 或 cycle。

Run: `git diff aacb627 -- backend/cmd/server/wire_gen.go backend/internal/service/openai_account_scheduler.go backend/internal/service/openai_gateway_service.go`

Expected: worktree 中只包含 ImageJob 构造链和本功能的 capability/executor 改动。随后在原工作树只读运行同三个文件的 `git diff`，确认原有 fallback 修改仍然存在且未被 worktree 操作改变。

- [ ] **Step 5: 运行启动依赖和指标测试**

Run: `cd backend && go test ./cmd/server ./internal/service ./internal/handler ./internal/repository -run 'Wire|ImageJob' -count=1`

Expected: PASS；配置 disabled 时不启动 worker、不访问 bucket，enabled 时启动设定数量的 worker。

- [ ] **Step 6: 提交接线和指标**

```bash
git add backend/internal/repository/wire.go backend/internal/service/wire.go backend/internal/handler/wire.go backend/internal/handler/openai_gateway_handler.go backend/cmd/server/wire.go backend/cmd/server/wire_gen.go backend/cmd/server/wire_gen_test.go backend/internal/service/image_job_metrics.go backend/internal/service/image_job_metrics_test.go
git commit -m "feat: wire image job workers"
```

### Task 18: 创建 `sub2api-image` Codex Plugin

**Files:**
- Create: `plugins/sub2api-image/.codex-plugin/plugin.json`
- Create: `plugins/sub2api-image/skills/image/SKILL.md`
- Create: `plugins/sub2api-image/skills/image/scripts/sub2api_image.py`
- Create: `plugins/sub2api-image/skills/image/references/api.md`
- Create: `plugins/sub2api-image/README.md`
- Create: `plugins/sub2api-image/tests/test_sub2api_image.py`

- [ ] **Step 1: 写模拟 Sub2API 的失败测试**

Python 测试启动 `ThreadingHTTPServer`，记录请求 method/path/header/body，并按调用返回 queued、running、completed 以及四个 PNG result。至少包含：

```python
class ImageCLIContractTest(unittest.TestCase):
    def test_generate_keeps_n_four_in_one_create_request(self):
        result = self.run_cli("generate", "--prompt", "city", "--n", "4")
        self.assertEqual(result.returncode, 0)
        creates = [r for r in self.server.requests if r.path == "/v1/images/generations"]
        self.assertEqual(len(creates), 1)
        self.assertEqual(json.loads(creates[0].body)["n"], 4)
        self.assertEqual(creates[0].headers["Prefer"], "respond-async")

    def test_inpaint_sends_image_and_mask_multipart(self):
        result = self.run_cli("inpaint", "--image", self.image, "--mask", self.mask, "--prompt", "snow")
        self.assertEqual(result.returncode, 0)
        self.assertIn(b'name="image"', self.server.last_create.body)
        self.assertIn(b'name="mask"', self.server.last_create.body)
```

另加 sequence、compose 重复 image 字段、status 恢复、partial、indeterminate、expired 和 stderr 不含 API key 的测试。

- [ ] **Step 2: 运行 Python 测试并确认红灯**

Run: `python3 -m unittest discover -s plugins/sub2api-image/tests -v`

Expected: FAIL，脚本文件不存在。

- [ ] **Step 3: 实现只使用标准库的 CLI**

```python
def request(method, path, *, json_body=None, multipart=None, prefer_async=False):
    base = os.environ["SUB2API_BASE_URL"].rstrip("/")
    key = os.environ["SUB2API_API_KEY"]
    headers = {"Authorization": f"Bearer {key}", "Accept": "application/json"}
    if prefer_async:
        headers["Prefer"] = "respond-async"
    # json 用 json.dumps；multipart 用 secrets.token_hex 生成 boundary 并写 bytes。

def poll_job(job_id, initial=1.0, maximum=5.0):
    # queued/running 退避；completed/partial 下载；failed/indeterminate/canceled/expired 返回非零。

def download_results(job, output_root):
    # 使用 Content-Type 映射 png/jpg/webp；先写 .part，再 os.replace。
```

子命令精确为 `generate/sequence/edit/inpaint/compose/status`。generate 默认 `n=4`，不循环创建请求；sequence 接收 2–4 个 `--scene` 或 `--scenes-json`；edit/inpaint/compose 使用 multipart。公共参数必须包含 `--model`（默认 `gpt-image-2`）、`--prompt`、`--size`、`--quality`、`--background`、`--output-format`、`--output-compression`、`--moderation` 和 `--partial-images`；edit/sequence 另含 `--input-fidelity`，inpaint 必须含 `--mask`，compose 可重复 `--image`。这些参数按用户是否显式提供决定是否发送，所以 `background=transparent`、`gpt-image-1.5` 和未来兼容字段不会被 CLI 固定值覆盖。每次 create 生成 UUID idempotency key，并把 `{job_id,idempotency_key,saved_indices}` 写到输出目录 `.sub2api-image-job.json`；网络重试复用该 key。日志只打印 base URL、Job ID、进度和目标路径。

- [ ] **Step 4: 编写 manifest 和 Skill**

```json
{
  "name": "sub2api-image",
  "version": "0.1.0",
  "description": "Generate, edit, inpaint, compose, and sequence images through Sub2API.",
  "author": {"name": "Sub2API"},
  "license": "MIT",
  "keywords": ["sub2api", "images", "gpt-image", "codex"],
  "skills": "./skills/"
}
```

`SKILL.md` frontmatter 的 name 为 `image`，description 明确列出生成、编辑、局部重绘、多图合成、四幕连续场景和恢复 Job 的触发语句。Skill 要求优先调用 bundled CLI；连续场景先把用户叙述整理成 2–4 个明确 scenes，但不得宣称绝对角色一致。

- [ ] **Step 5: 写 API reference 和安装说明**

README 给出 Plugin 安装目录、`SUB2API_BASE_URL`、`SUB2API_API_KEY`、可选 `SUB2API_IMAGE_OUTPUT_DIR`，并说明 Codex 稳定入口是自然语言或 `$image`，不是修改客户端注册顶级 `/image`。reference 列出五个 server endpoint、所有状态和 error 字段。

- [ ] **Step 6: 运行 Plugin 测试和 manifest 校验**

Run: `python3 -m unittest discover -s plugins/sub2api-image/tests -v`

Expected: PASS，所有六个子命令和错误状态用例通过。

Run: `python3 -m json.tool plugins/sub2api-image/.codex-plugin/plugin.json >/dev/null`

Expected: exit 0。

- [ ] **Step 7: 提交 Plugin**

```bash
git add plugins/sub2api-image
git commit -m "feat: add Sub2API image Codex plugin"
```

### Task 19: 文档、端到端验收和完整回归

**Files:**
- Create: `docs/openai-images.md`
- Create: `backend/internal/integration/openai_image_jobs_test.go`
- Modify: `README.md`

- [ ] **Step 1: 写短请求/长 worker 的端到端失败测试**

测试用真实 Gin router、内存 store、测试 repository 和一个阻塞 executor。POST 必须在 executor 放行前返回 202；随后放行 executor，轮询得到四个结果并逐个下载。

```go
func TestAsyncImageJobReturnsBeforeUpstreamAndDownloadsFour(t *testing.T) {
	app := newAsyncImageIntegrationApp(t)
	app.Executor.Block()
	started := time.Now()
	job := app.PostGeneration(t, 4, true)
	require.Less(t, time.Since(started), 500*time.Millisecond)
	require.Equal(t, 0, app.Executor.CallCount())
	app.Executor.UnblockWithImages(4)
	completed := app.WaitCompleted(t, job.ID)
	require.Equal(t, 4, completed.CompletedCount)
	for i := 0; i < 4; i++ { require.NotEmpty(t, app.Download(t, job.ID, i)) }
	require.Equal(t, 1, app.Executor.CallCount())
}
```

- [ ] **Step 2: 运行红灯并实现测试应用构造器**

Run: `cd backend && go test ./internal/integration -run TestAsyncImageJobReturnsBeforeUpstreamAndDownloadsFour -count=1`

Expected: 首次 FAIL，缺少 `newAsyncImageIntegrationApp`。

测试应用构造器必须创建 `memoryImageJobRepository`、`memoryImageObjectStore`、`blockingImageExecutor`、enabled 的 local ImageJobsConfig、`ImageJobService` 和带 API key auth context 的 Gin router；`t.Cleanup(app.Service.Stop)` 保证 goroutine 退出。它只实现测试调用的 `PostGeneration/WaitCompleted/Download`，所有响应都通过真实 handler，executor 不发网络请求。

```go
type asyncImageIntegrationApp struct {
	Router http.Handler
	Service *service.ImageJobService
	Executor *blockingImageExecutor
}

func newAsyncImageIntegrationApp(t *testing.T) *asyncImageIntegrationApp {
	repo := newMemoryImageJobRepository()
	store := newMemoryImageObjectStore()
	executor := newBlockingImageExecutor()
	svc := newIntegrationImageJobService(t, repo, store, executor)
	router := newIntegrationImageJobRouter(t, svc, integrationAPIKey())
	svc.Start()
	t.Cleanup(svc.Stop)
	return &asyncImageIntegrationApp{Router: router, Service: svc, Executor: executor}
}
```

Run: `cd backend && go test ./internal/integration -run TestAsyncImageJobReturnsBeforeUpstreamAndDownloadsFour -count=1`

Expected: PASS，且测试全程不访问外网。

- [ ] **Step 3: 编写用户文档**

`docs/openai-images.md` 必须包含：

- 同步 generations/edits 仍兼容；
- `Prefer: respond-async` 的 curl 示例和 202 body；
- sequence JSON/multipart 示例；
- status/download/cancel；
- batch `n=4` 是一次上游调用；
- continuity 的 anchor/previous 算法、速度/费用和非绝对一致性声明；
- S3/R2 配置、24 小时 TTL 和私有 bucket；
- local driver 只适用于单实例或所有实例共享同一持久卷；
- Cloudflare 只承载短创建/查询/下载请求；
- advanced parameter 无兼容账号时的明确错误；
- Plugin 安装与 `$image` 示例。

- [ ] **Step 4: 运行格式、单元、集成和 Plugin 回归**

Run: `cd backend && gofmt -w internal/config/image_jobs.go internal/service/image_job*.go internal/service/openai_images*.go internal/repository/image_job*.go internal/handler/openai_image_jobs.go`

Run: `cd backend && make test-unit`

Expected: PASS。

Run: `cd backend && make test-integration`

Expected: PASS；需要测试 PostgreSQL/Redis 的项目既有环境变量已经配置。

Run: `python3 -m unittest discover -s plugins/sub2api-image/tests -v`

Expected: PASS。

- [ ] **Step 5: 对照设计逐项验收**

Run: `cd backend && go test ./internal/service ./internal/handler ./internal/integration -run 'Image(Job|Sequence|Executor|RespondAsync)' -count=1`

Expected: PASS，并能从测试名称定位以下证据：batch 一次调用、四个结果、edit/mask/multi-image、continuity anchors、幂等、归属隔离、预留结算、stale/indeterminate、TTL cleanup、同步协议不变。

- [ ] **Step 6: 检查工作树完整性并提交文档**

Run: `git diff --check`

Expected: 无输出，exit 0。

Run: `git status --short`

Expected: 独立 worktree 只剩本任务的 `README.md`、`docs/openai-images.md` 和 integration test；没有凭据、生成图片或 `.sub2api-image-job.json`。原 `/home/ubuntu/sub2api` 工作树仍保持实施开始时记录的 11 个 fallback 改动。

```bash
git add README.md docs/openai-images.md backend/internal/integration/openai_image_jobs_test.go
git commit -m "docs: document async image jobs and plugin"
```

- [ ] **Step 7: 最终提交审计**

Run: `git log --oneline aacb627..HEAD`

Expected: 能看到本计划中的小步提交，且每个提交主题只覆盖一个任务。

Run: `git diff --name-only aacb627..HEAD | sort`

Expected: 不包含 `frontend/src/i18n/locales/en.ts`、`frontend/src/i18n/locales/zh.ts`、`frontend/src/views/admin/GroupsView.vue`；这些仍属于用户原 fallback 工作。

- [ ] **Step 8: 通过用户检查点后做 combined integration**

先暂停并请用户选择如何为原工作树的 fallback 修改建立检查点；不得代替用户提交、暂存或丢弃这些修改。用户确认检查点后，把功能分支合入包含 fallback 的分支，冲突文件逐块保留两边语义，然后运行：

Run: `cd backend && go test ./internal/service ./internal/handler ./cmd/server -run 'Fallback|Image|RecordUsage|Wire' -count=1`

Expected: PASS；`openai_account_scheduler.go` 同时具有分组 fallback 和严格图片 capability，`openai_gateway_service.go` 同时具有 fallback usage 逻辑和 `RecordUsageWithCost`，`wire_gen.go` 同时具有 fallback 构造参数和 ImageJob provider 链。

Run: `git diff --check`

Expected: 无输出，exit 0。只有完成这一步后，才能宣称功能已与用户的 fallback 工作集成完成。

## 完成定义

- 带 `Prefer: respond-async` 的 generations/edits 在上游开始前返回 `202 + job_id`。
- batch `n=4` 的自动测试证明只有一次 executor/upstream 调用，并能下载四个文件。
- edit、mask、重复 image 多图、URL 图片输入均可异步完成。
- continuity 无参考图严格为一次 generation 加三次 edit；有参考图严格为四次 edit；每次都保留固定 anchors 和上一帧。
- Job 在 PostgreSQL 中可跨进程恢复；upstream phase 失联不会自动重复调用。
- 私有对象存储、归属校验、幂等、费用预留、实际结算、取消和 TTL 清理全部有测试。
- 同步 Images JSON/SSE、账号故障转移、分组 fallback 和计费回归通过。
- Codex 用户可以通过自然语言或 `$image` 调用 generate/sequence/edit/inpaint/compose/status；客户端不会把 `n=4` 拆成四次创建。
