# Infinite Canvas Images Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 在 Sub2API 用户端和管理端接入服务端持久化的完整图片无限画布，并提供管理员全站有序模型链、用户 API Key 选择、模型级故障回退和按实际成功模型结算。

**Architecture:** Vue 继续作为 Sub2API 宿主，独立构建的 React/TypeScript Infinite Canvas 通过同源 ESM manifest 延迟挂载。React 只调用 Vue 注入的认证 API bridge；Go 后端复用既有异步图片 Job、Image Executor 和对象存储，并增加模型策略、画布项目、资产和模型级 attempt orchestration。

**Tech Stack:** Go、Gin、PostgreSQL、Ent 生成代码所在现有项目、Vue 3、React 19、TypeScript、Vite、Zustand、Vitest、Go `testing`/`testify`、Playwright、S3/R2、AGPL-3.0。

---

## 实施前约束

- 设计来源：`docs/superpowers/specs/2026-08-02-infinite-canvas-images-design.md`。
- 服务端前置计划：`docs/superpowers/plans/2026-07-24-async-images-codex.md` 的 Task 1–17。该计划提供 Image Executor、异步 Job、费用预占、对象存储、worker 和恢复语义；本计划不得复制这些基础设施。
- 实施开始时使用 `superpowers:using-git-worktrees` 创建新的隔离工作树。当前 `/home/ubuntu/sub2api` 有未提交的图片 fallback 和 scheduler 修改，不得在该目录运行 `git add`、生成 Ent/Wire 文件或合并。
- 新工作树必须基于“异步图片 Task 1–17 已完成”且“用户现有 fallback 修改已建立可恢复提交”的整合提交，而不是直接基于本文档分支的旧代码快照。
- 上游固定为 `basketikun/infinite-canvas@ea0414e88cffa6b522cc13c0613b3c8085983a53`。不得从浮动 `main` 拉取源码。
- 所有任务遵循红灯、最小实现、绿灯、提交。每次提交只包含当前任务文件。
- 第一版保留完整的图片画布体验：图片、文本、配置、分组节点，缩放、拖拽、选择、连接、撤销、重做、导入、导出、文生图、编辑、mask、裁剪、分割和连续分支。上游的音频、视频、本地 agent 和第三方插件运行时不进入挂载入口。
- 任何浏览器产物都不得包含上游 Provider API Key、可编辑 Provider URL 或 Sub2API JWT 持久化逻辑。
- 模型级回退只发生在当前任务尚未收到最终图片时；收到至少一张最终图片后发生错误必须进入 partial，不得调用下一模型。
- 本计划完成不授权生产切流。HAProxy 首次切换仍需要单独确认维护窗口。

## 文件职责映射

### Backend 新文件

- `backend/migrations/160_image_canvas.sql`：模型策略、审计、画布项目、资产表，以及异步图片 Job 的画布字段。
- `backend/internal/service/image_canvas_types.go`：项目、资产、模型策略、能力、API DTO 和领域错误。
- `backend/internal/service/image_model_policy.go`：策略校验、版本更新和 attempt 顺序构造。
- `backend/internal/service/image_model_catalog.go`：把 API Key 分组权限与可调度图片能力适配为模型目录。
- `backend/internal/service/image_canvas_project.go`：项目 CRUD、乐观并发和资产所有权。
- `backend/internal/service/image_canvas_job.go`：登录用户 Canvas 请求到异步 Image Job 的适配。
- `backend/internal/repository/image_model_policy_repo.go`：策略、条目和审计事务。
- `backend/internal/repository/image_canvas_repo.go`：项目、资产和关联任务的 PostgreSQL repository。
- `backend/internal/handler/image_canvas_handler.go`：用户登录态 config、项目、上传、任务、事件和下载 API。
- `backend/internal/handler/admin/image_canvas_handler.go`：管理员模型策略和审计 API。
- `backend/internal/server/middleware/image_canvas_enabled.go`：只门禁画布任务，不门禁管理员策略配置。

### Backend 修改文件

- `backend/internal/service/domain_constants.go`、`setting_service.go`、`settings_view.go`：`image_canvas_enabled` 默认关闭并进入 public settings。
- `backend/internal/handler/dto/settings.go`、`setting_handler.go`、`admin/setting_handler.go`：功能开关 DTO、公开注入和管理员更新。
- `backend/internal/service/image_job.go`、`image_job_service.go`、`image_job_worker.go`、`image_job_billing.go`：画布元数据、候选模型最高预占、模型级 attempts、partial 边界和资产落库。
- `backend/internal/service/image_job_cleanup.go`、`image_job_metrics.go`：孤立 Canvas 资产延迟清理和模型 attempt 指标。
- `backend/internal/repository/image_job_repo.go`：扫描和更新新增 Job 字段。
- `backend/internal/repository/wire.go`、`backend/internal/service/wire.go`、`backend/internal/handler/wire.go`、`backend/internal/handler/handler.go`、`backend/cmd/server/wire.go`、`backend/cmd/server/wire_gen.go`：依赖注入。
- `backend/internal/server/routes/user.go`、`admin.go`：Canvas 与管理员策略路由。

### React 微前端新目录

- `frontend/infinite-canvas/`：AGPL 派生 React 包和独立 Vite 构建。
- `frontend/infinite-canvas/src/entry.tsx`：`mountCanvas/updateContext/unmount` 公共入口。
- `frontend/infinite-canvas/src/host-context.tsx`：Vue 注入 API bridge、主题、语言、导航与通知。
- `frontend/infinite-canvas/src/api/canvas-api.ts`：类型化 Canvas API SDK 和 SSE parser。
- `frontend/infinite-canvas/src/stores/server-canvas-store.ts`：服务端项目状态、自动保存和冲突恢复。
- `frontend/infinite-canvas/src/stores/canvas-session-store.ts`：所选 API Key、模型和未完成任务，仅保存非敏感 UI 状态。
- `frontend/infinite-canvas/src/i18n.tsx`：挂载画布的中英文词典和 locale context。
- `frontend/infinite-canvas/src/upstream/`：固定提交的上游源码快照及 Sub2API 修改。
- `frontend/infinite-canvas/LICENSE`、`LICENSE.upstream`、`UPSTREAM.md`、`NOTICE`：AGPL、来源和修改说明。
- `scripts/vendor-infinite-canvas.sh`：只允许固定提交的初次源码导入脚本。

### Vue 宿主新文件

- `frontend/src/components/image-canvas/ImageCanvasHost.vue`：加载 ESM manifest、挂载和销毁 React。
- `frontend/src/components/image-canvas/canvasLoader.ts`：同源 manifest、CSS 和模块加载。
- `frontend/src/components/image-canvas/canvasHostBridge.ts`：包装现有 `apiClient` 和认证流式 fetch。
- `frontend/src/views/user/ImageCanvasView.vue`：用户沉浸式宿主页。
- `frontend/src/views/admin/ImageCanvasAdminView.vue`：管理员“生成画布/模型配置”标签。
- `frontend/src/components/admin/image-canvas/ModelPolicyEditor.vue`：有序模型链编辑器。
- `frontend/src/api/admin/imageCanvas.ts`：Vue 管理端模型策略 API 类型。

### Vue 宿主修改文件

- `frontend/package.json`、`pnpm-lock.yaml`、`pnpm-workspace.yaml`、`vite.config.ts`：workspace 和双构建。
- `frontend/src/router/index.ts`、`components/layout/AppSidebar.vue`、`utils/featureFlags.ts`：两个路由；用户入口受开关控制，管理员配置入口始终可访问。
- `frontend/src/types/index.ts`、`api/admin/settings.ts`、`views/admin/SettingsView.vue`：功能开关。
- `frontend/src/i18n/locales/en.ts`、`zh.ts`：界面文本。
- `.github/workflows/backend-ci.yml`、`.github/workflows/release.yml`：双前端构建、许可证和对应源码 URL 阻断检查。

### 测试与文档新文件

- `backend/internal/service/image_model_policy_test.go`
- `backend/internal/service/image_canvas_project_test.go`
- `backend/internal/service/image_canvas_job_test.go`
- `backend/internal/repository/image_model_policy_repo_integration_test.go`
- `backend/internal/repository/image_canvas_repo_integration_test.go`
- `backend/internal/handler/image_canvas_handler_test.go`
- `backend/internal/handler/admin/image_canvas_handler_test.go`
- `frontend/infinite-canvas/src/**/*.test.tsx`
- `frontend/src/components/image-canvas/__tests__/ImageCanvasHost.spec.ts`
- `frontend/src/components/admin/image-canvas/__tests__/ModelPolicyEditor.spec.ts`
- `frontend/e2e/image-canvas.spec.ts`
- `docs/infinite-canvas-images.md`

### Task 0: 验证执行基线与前置依赖

**Files:**
- Read: `docs/superpowers/plans/2026-07-24-async-images-codex.md`
- Read: `docs/superpowers/specs/2026-08-02-infinite-canvas-images-design.md`
- Read: `/tmp/infinite-canvas-review.axlOIV/LICENSE`

- [ ] **Step 1: 建立隔离工作树并记录基线**

Run:

```bash
git -C /home/ubuntu/sub2api status --short --branch
test "$(git rev-parse --verify refs/heads/feat/async-images-codex)" != ""
git worktree add -b feat/infinite-canvas-images-impl /home/ubuntu/sub2api-infinite-canvas-impl refs/heads/feat/async-images-codex
git -C /home/ubuntu/sub2api-infinite-canvas-impl status --short --branch
```

Expected: 原工作区仍显示用户的未提交文件；`feat/async-images-codex` 已由前置阶段整合异步图片实现和用户 fallback 检查点；新工作树为 clean。该 ref 尚未整合时停止，不允许改用浮动 `main`。

- [ ] **Step 2: 验证异步图片基础已存在**

Run:

```bash
test -f backend/internal/service/image_job_worker.go
test -f backend/internal/service/openai_images_executor.go
test -f backend/internal/service/image_job_billing.go
test -f backend/internal/repository/image_job_store_s3.go
cd backend && go test ./internal/service ./internal/handler -run 'Image(Job|Executor|Billing)' -count=1
```

Expected: 四个文件均存在且测试 PASS。任一文件缺失时停止本计划，先执行异步图片计划 Task 1–17。

- [ ] **Step 3: 验证上游提交和许可证**

Run:

```bash
git -C /tmp/infinite-canvas-review.axlOIV rev-parse HEAD
sha256sum /tmp/infinite-canvas-review.axlOIV/LICENSE
```

Expected: HEAD 为 `ea0414e88cffa6b522cc13c0613b3c8085983a53`；记录 LICENSE SHA-256 到执行日志，后续导入测试使用同一值。

### Task 1: 增加默认关闭的 Image Canvas 功能开关

**Files:**
- Modify: `backend/internal/service/domain_constants.go`
- Modify: `backend/internal/service/settings_view.go`
- Modify: `backend/internal/service/setting_service.go`
- Modify: `backend/internal/handler/dto/settings.go`
- Modify: `backend/internal/handler/admin/setting_handler.go`
- Modify: `backend/internal/handler/setting_handler_public_test.go`
- Modify: `backend/internal/handler/dto/public_settings_injection_schema_test.go`
- Modify: `frontend/src/types/index.ts`
- Modify: `frontend/src/api/admin/settings.ts`
- Modify: `frontend/src/utils/featureFlags.ts`
- Modify: `frontend/src/views/admin/SettingsView.vue`

- [ ] **Step 1: 写 public settings 和默认值失败测试**

```go
func TestPublicSettingsIncludesDisabledImageCanvas(t *testing.T) {
	h := newPublicSettingsHandlerForTest(t, map[string]string{
		service.SettingKeyImageCanvasEnabled: "false",
	})
	w := performPublicSettingsRequest(t, h)
	require.JSONEq(t, `{"code":0,"message":"success","data":{"image_canvas_enabled":false}}`, selectJSONFields(t, w.Body.Bytes(), "image_canvas_enabled"))
}
```

在 `public_settings_injection_schema_test.go` 的 expected key 集合加入 `image_canvas_enabled`。

- [ ] **Step 2: 运行测试确认红灯**

Run: `cd backend && go test ./internal/handler ./internal/handler/dto -run 'ImageCanvas|PublicSettingsInjection' -count=1`

Expected: FAIL，缺少 `SettingKeyImageCanvasEnabled` 或 JSON 字段。

- [ ] **Step 3: 实现后端设置字段**

```go
const SettingKeyImageCanvasEnabled = "image_canvas_enabled"
```

在 `PublicSettings`、`SystemSettings` 和对应 handler DTO 增加：

```go
ImageCanvasEnabled bool `json:"image_canvas_enabled"`
```

`InitDefaultSettings` 写入 `false`，`GetPublicSettings` 使用严格 opt-in：

```go
ImageCanvasEnabled: settings[SettingKeyImageCanvasEnabled] == "true",
```

管理员更新请求使用 `*bool`，只有字段出现时才更新该 key，并沿用现有 settings audit diff。

- [ ] **Step 4: 注册前端 opt-in flag 和设置开关**

```ts
imageCanvas: defineFlag({
  key: 'image_canvas_enabled',
  mode: 'opt-in',
  label: 'Image Canvas',
}),
```

在 `PublicSettings`、admin settings DTO 和设置表单加入 `image_canvas_enabled: boolean`。使用现有 Toggle 组件，标签 key 为 `admin.settings.imageCanvasEnabled`。

- [ ] **Step 5: 运行测试和类型检查**

Run: `cd backend && go test ./internal/service ./internal/handler ./internal/handler/dto -run 'ImageCanvas|PublicSettings' -count=1`

Run: `cd frontend && pnpm test:run -- src/utils src/api/__tests__ && pnpm typecheck`

Expected: PASS；未配置或 false 时 `FeatureFlags.imageCanvas` 为 false。

- [ ] **Step 6: 提交功能开关**

```bash
git add backend/internal/service/domain_constants.go backend/internal/service/settings_view.go backend/internal/service/setting_service.go backend/internal/handler/dto/settings.go backend/internal/handler/admin/setting_handler.go backend/internal/handler/setting_handler_public_test.go backend/internal/handler/dto/public_settings_injection_schema_test.go frontend/src/types/index.ts frontend/src/api/admin/settings.ts frontend/src/utils/featureFlags.ts frontend/src/views/admin/SettingsView.vue
git commit -m "feat: add image canvas feature flag"
```

### Task 2: 建立模型策略、项目、资产和 Job 扩展迁移

**Files:**
- Create: `backend/migrations/160_image_canvas.sql`
- Modify: `backend/internal/repository/migrations_schema_integration_test.go`

- [ ] **Step 1: 写迁移失败测试**

```go
func TestMigrationsCreateImageCanvasSchema(t *testing.T) {
	db := openMigrationTestDB(t)
	for _, table := range []string{
		"image_model_policies", "image_model_policy_items", "image_model_policy_audits",
		"image_canvas_projects", "image_assets", "image_canvas_asset_references",
	} {
		var got string
		require.NoError(t, db.QueryRow(`SELECT table_name FROM information_schema.tables WHERE table_schema='public' AND table_name=$1`, table).Scan(&got))
		require.Equal(t, table, got)
	}
	for _, column := range []string{"project_id", "client_node_id", "selected_model", "policy_version", "attempt_plan", "successful_model", "attempt_log"} {
		var got string
		require.NoError(t, db.QueryRow(`SELECT column_name FROM information_schema.columns WHERE table_name='image_jobs' AND column_name=$1`, column).Scan(&got))
	}
}
```

- [ ] **Step 2: 运行迁移测试确认红灯**

Run: `cd backend && go test -tags=integration ./internal/repository -run TestMigrationsCreateImageCanvasSchema -count=1`

Expected: FAIL，第一张新表不存在。

- [ ] **Step 3: 编写增量迁移**

`160_image_canvas.sql` 使用现有迁移 runner 支持的事务格式，创建以下约束：

```sql
CREATE TABLE image_model_policies (
  id smallint PRIMARY KEY CHECK (id = 1),
  version bigint NOT NULL DEFAULT 0,
  enabled boolean NOT NULL DEFAULT false,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now()
);
INSERT INTO image_model_policies (id) VALUES (1) ON CONFLICT (id) DO NOTHING;

CREATE TABLE image_model_policy_items (
  id bigserial PRIMARY KEY,
  policy_id smallint NOT NULL REFERENCES image_model_policies(id) ON DELETE CASCADE,
  model varchar(128) NOT NULL,
  enabled boolean NOT NULL DEFAULT true,
  position integer NOT NULL CHECK (position >= 0),
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (policy_id, model),
  UNIQUE (policy_id, position)
);

CREATE TABLE image_model_policy_audits (
  id bigserial PRIMARY KEY,
  operator_user_id bigint NOT NULL REFERENCES users(id),
  old_version bigint NOT NULL,
  new_version bigint NOT NULL,
  before_value jsonb NOT NULL,
  after_value jsonb NOT NULL,
  created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE image_canvas_projects (
  id bigserial PRIMARY KEY,
  public_id varchar(64) NOT NULL UNIQUE,
  user_id bigint NOT NULL REFERENCES users(id),
  name varchar(160) NOT NULL,
  document jsonb NOT NULL,
  version bigint NOT NULL DEFAULT 1,
  thumbnail_asset_id bigint,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  deleted_at timestamptz
);
CREATE INDEX idx_image_canvas_projects_user_updated ON image_canvas_projects(user_id, updated_at DESC) WHERE deleted_at IS NULL;

CREATE TABLE image_assets (
  id bigserial PRIMARY KEY,
  public_id varchar(64) NOT NULL UNIQUE,
  owner_user_id bigint NOT NULL REFERENCES users(id),
  project_id bigint REFERENCES image_canvas_projects(id),
  source_type varchar(20) NOT NULL CHECK (source_type IN ('upload','generated','derived')),
  object_key text NOT NULL UNIQUE,
  thumbnail_object_key text,
  mime_type varchar(100) NOT NULL,
  width integer NOT NULL CHECK (width > 0),
  height integer NOT NULL CHECK (height > 0),
  byte_size bigint NOT NULL CHECK (byte_size > 0),
  sha256 char(64) NOT NULL,
  origin_job_id bigint REFERENCES image_jobs(id),
  parent_asset_ids jsonb NOT NULL DEFAULT '[]'::jsonb,
  created_at timestamptz NOT NULL DEFAULT now(),
  deleted_at timestamptz
);
ALTER TABLE image_canvas_projects ADD CONSTRAINT fk_image_canvas_thumbnail FOREIGN KEY (thumbnail_asset_id) REFERENCES image_assets(id);
CREATE INDEX idx_image_assets_owner_project ON image_assets(owner_user_id, project_id) WHERE deleted_at IS NULL;

CREATE TABLE image_canvas_asset_references (
  project_id bigint NOT NULL REFERENCES image_canvas_projects(id) ON DELETE CASCADE,
  asset_id bigint NOT NULL REFERENCES image_assets(id) ON DELETE CASCADE,
  node_id varchar(128) NOT NULL,
  created_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (project_id, asset_id, node_id)
);
CREATE INDEX idx_image_canvas_asset_references_asset ON image_canvas_asset_references(asset_id);

ALTER TABLE image_jobs ADD COLUMN project_id bigint REFERENCES image_canvas_projects(id);
ALTER TABLE image_jobs ADD COLUMN client_node_id varchar(128);
ALTER TABLE image_jobs ADD COLUMN selected_model varchar(128);
ALTER TABLE image_jobs ADD COLUMN policy_version bigint;
ALTER TABLE image_jobs ADD COLUMN attempt_plan jsonb NOT NULL DEFAULT '[]'::jsonb;
ALTER TABLE image_jobs ADD COLUMN successful_model varchar(128);
ALTER TABLE image_jobs ADD COLUMN attempt_log jsonb NOT NULL DEFAULT '[]'::jsonb;
ALTER TABLE image_job_results ADD COLUMN asset_id bigint REFERENCES image_assets(id);
CREATE INDEX idx_image_jobs_project_created ON image_jobs(project_id, created_at DESC) WHERE project_id IS NOT NULL;
```

- [ ] **Step 4: 运行迁移测试并检查回滚兼容性**

Run: `cd backend && go test -tags=integration ./internal/repository -run 'Migrations(CreateImageCanvasSchema|Schema)' -count=1`

Expected: PASS；旧 image job 查询仍能读取默认空 attempt JSON。

- [ ] **Step 5: 提交迁移**

```bash
git add backend/migrations/160_image_canvas.sql backend/internal/repository/migrations_schema_integration_test.go
git commit -m "feat: add image canvas database schema"
```

### Task 3: 实现有版本的全站模型策略

**Files:**
- Create: `backend/internal/service/image_canvas_types.go`
- Create: `backend/internal/service/image_model_policy.go`
- Create: `backend/internal/service/image_model_policy_test.go`
- Create: `backend/internal/repository/image_model_policy_repo.go`
- Create: `backend/internal/repository/image_model_policy_repo_integration_test.go`
- Modify: `backend/internal/repository/wire.go`

- [ ] **Step 1: 写策略校验和顺序失败测试**

```go
func TestImageModelPolicyBuildAttemptPlan(t *testing.T) {
	policy := ImageModelPolicy{Version: 7, Enabled: true, Items: []ImageModelPolicyItem{
		{Model: "model-a", Enabled: true, Position: 0},
		{Model: "model-b", Enabled: true, Position: 1},
		{Model: "model-c", Enabled: true, Position: 2},
	}}
	allowed := map[string]ImageModelCapability{
		"model-a": {Generation: true, Edit: false},
		"model-b": {Generation: true, Edit: true},
		"model-c": {Generation: true, Edit: true},
	}
	plan, err := BuildImageAttemptPlan(policy, "model-b", ImageOperationEdit, allowed)
	require.NoError(t, err)
	require.Equal(t, []string{"model-b", "model-c"}, plan)
}

func TestImageModelPolicyRejectsDuplicateModels(t *testing.T) {
	err := ValidateImageModelPolicyItems([]ImageModelPolicyItem{
		{Model: "model-a", Enabled: true, Position: 0},
		{Model: "model-a", Enabled: true, Position: 1},
	})
	require.ErrorIs(t, err, ErrImageModelPolicyInvalid)
}
```

- [ ] **Step 2: 运行测试确认红灯**

Run: `cd backend && go test ./internal/service -run ImageModelPolicy -count=1`

Expected: FAIL，缺少策略类型和构造函数。

- [ ] **Step 3: 定义领域类型和纯顺序函数**

```go
type ImageOperation string
const (
	ImageOperationGeneration ImageOperation = "generation"
	ImageOperationEdit ImageOperation = "edit"
)

type ImageModelCapability struct {
	Generation bool `json:"generation"`
	Edit bool `json:"edit"`
	MultiImage bool `json:"multi_image"`
	Mask bool `json:"mask"`
	MaxInputImages int `json:"max_input_images"`
	MaxOutputs int `json:"max_outputs"`
	Sizes []string `json:"sizes"`
}

type ImageModelPolicyItem struct {
	Model string `json:"model"`
	Enabled bool `json:"enabled"`
	Position int `json:"position"`
	Capability ImageModelCapability `json:"capability"`
}

type ImageModelPolicy struct {
	Version int64 `json:"version"`
	Enabled bool `json:"enabled"`
	Items []ImageModelPolicyItem `json:"models"`
}
```

`ValidateImageModelPolicyItems` trim model，拒绝空值、重复、非连续 position 和零个启用项。`BuildImageAttemptPlan` 首先确认 selected 位于启用链并出现在 allowed，再按 `[selected] + admin order excluding selected` 过滤 operation capability；空结果返回 `ErrNoCompatibleImageModel`。

- [ ] **Step 4: 写 repository 版本冲突集成测试**

```go
func TestImageModelPolicyRepositoryReplaceUsesCASAndAudit(t *testing.T) {
	repo, operatorID := newImageModelPolicyIntegrationRepo(t)
	first, err := repo.Replace(context.Background(), 0, operatorID, true, []service.ImageModelPolicyItem{{Model: "model-a", Enabled: true, Position: 0}})
	require.NoError(t, err)
	require.Equal(t, int64(1), first.Version)
	_, err = repo.Replace(context.Background(), 0, operatorID, true, []service.ImageModelPolicyItem{{Model: "model-b", Enabled: true, Position: 0}})
	require.ErrorIs(t, err, service.ErrImageModelPolicyVersionConflict)
	audits, err := repo.ListAudit(context.Background(), 10)
	require.NoError(t, err)
	require.Len(t, audits, 1)
}
```

- [ ] **Step 5: 实现 repository 原子替换**

接口固定为：

```go
type ImageModelPolicyRepository interface {
	Get(ctx context.Context) (*ImageModelPolicy, error)
	Replace(ctx context.Context, expectedVersion, operatorUserID int64, enabled bool, items []ImageModelPolicyItem) (*ImageModelPolicy, error)
	ListAudit(ctx context.Context, limit int) ([]ImageModelPolicyAudit, error)
}
```

`Replace` 在一个 SQL 事务中执行 `SELECT version FOR UPDATE`、expectedVersion 比较、读取 before JSON、删除旧条目、批量插入新条目、version+1、写 audit、commit。任何一步失败回滚。

- [ ] **Step 6: 运行单元与集成测试并提交**

Run: `cd backend && go test ./internal/service -run ImageModelPolicy -count=1`

Run: `cd backend && go test -tags=integration ./internal/repository -run ImageModelPolicyRepository -count=1`

Expected: PASS。

```bash
git add backend/internal/service/image_canvas_types.go backend/internal/service/image_model_policy.go backend/internal/service/image_model_policy_test.go backend/internal/repository/image_model_policy_repo.go backend/internal/repository/image_model_policy_repo_integration_test.go backend/internal/repository/wire.go
git commit -m "feat: add versioned image model policy"
```

### Task 4: 适配 API Key 分组与图片模型能力目录

**Files:**
- Create: `backend/internal/service/image_model_catalog.go`
- Create: `backend/internal/service/image_model_catalog_test.go`
- Modify: `backend/internal/service/group_service.go`
- Modify: `backend/internal/service/account.go`

- [ ] **Step 1: 写 Key 所有权和分组过滤失败测试**

```go
func TestImageModelCatalogFiltersPolicyByOwnedAPIKeyGroup(t *testing.T) {
	catalog, deps := newImageModelCatalogFixture(t)
	deps.Key(42, 9, 3, true)
	deps.GroupModels(3, "model-a", "model-c")
	deps.Capability("model-a", ImageModelCapability{Generation: true})
	deps.Capability("model-c", ImageModelCapability{Generation: true, Edit: true})
	models, err := catalog.ForAPIKey(context.Background(), 9, 42)
	require.NoError(t, err)
	require.ElementsMatch(t, []string{"model-a", "model-c"}, modelNames(models))
	_, err = catalog.ForAPIKey(context.Background(), 10, 42)
	require.ErrorIs(t, err, ErrImageCanvasAPIKeyNotFound)
}
```

- [ ] **Step 2: 运行测试确认红灯**

Run: `cd backend && go test ./internal/service -run ImageModelCatalog -count=1`

Expected: FAIL，缺少 catalog。

- [ ] **Step 3: 定义 catalog ports 和服务**

```go
type ImageCanvasAPIKey struct {
	ID int64 `json:"id"`
	Name string `json:"name"`
	GroupID int64 `json:"group_id"`
	GroupName string `json:"group_name"`
}

type ImageModelCatalog interface {
	ListOwnedAPIKeys(ctx context.Context, userID int64) ([]ImageCanvasAPIKey, error)
	ForAPIKey(ctx context.Context, userID, apiKeyID int64) (map[string]ImageModelCapability, error)
	ListSchedulable(ctx context.Context) (map[string]ImageModelCapability, error)
}
```

实现必须通过 repository 的 `user_id + id + status` 条件查询 Key，不读取客户端 user/group。`ForAPIKey` 取该 Key 当前 group 的模型白名单或映射，再与账号调度器报告的 image capability 取交集。返回 DTO 只含 ID、名称和 group 信息，不含 key 字符串、前缀或 hash。

- [ ] **Step 4: 锁定现有 fallback 改动兼容性**

Run:

```bash
git diff -- backend/internal/service/openai_account_scheduler.go backend/internal/service/openai_gateway_service.go
git -C /home/ubuntu/sub2api diff -- backend/internal/service/openai_account_scheduler.go backend/internal/service/openai_gateway_service.go
```

Expected: 当前任务不修改 scheduler/gateway；原工作区用户 fallback 差异仍在。Catalog 只调用稳定的 group/account 查询接口。

- [ ] **Step 5: 运行测试并提交**

Run: `cd backend && go test ./internal/service -run 'ImageModelCatalog|Group.*Model|ImageCapability' -count=1`

Expected: PASS。

```bash
git add backend/internal/service/image_model_catalog.go backend/internal/service/image_model_catalog_test.go backend/internal/service/group_service.go backend/internal/service/account.go
git commit -m "feat: resolve image models by api key group"
```

### Task 5: 暴露管理员模型策略 API

**Files:**
- Create: `backend/internal/handler/admin/image_canvas_handler.go`
- Create: `backend/internal/handler/admin/image_canvas_handler_test.go`
- Modify: `backend/internal/handler/handler.go`
- Modify: `backend/internal/handler/wire.go`
- Modify: `backend/internal/server/routes/admin.go`

- [ ] **Step 1: 写管理员权限、更新和冲突失败测试**

```go
func TestAdminImageCanvasPolicyUpdate(t *testing.T) {
	router, repo := newAdminImageCanvasRouter(t, middleware.RoleAdmin)
	body := `{"version":0,"enabled":true,"models":[{"model":"model-a","enabled":true,"position":0}]}`
	w := performJSON(t, router, http.MethodPut, "/api/v1/admin/image-canvas/model-policy", body)
	require.Equal(t, http.StatusOK, w.Code)
	require.Equal(t, int64(1), repo.MustGet(t).Version)
	w = performJSON(t, router, http.MethodPut, "/api/v1/admin/image-canvas/model-policy", body)
	require.Equal(t, http.StatusConflict, w.Code)
}
```

- [ ] **Step 2: 运行测试确认红灯**

Run: `cd backend && go test ./internal/handler/admin ./internal/server/routes -run AdminImageCanvas -count=1`

Expected: FAIL，路由或 handler 不存在。

- [ ] **Step 3: 实现 handler 和稳定错误映射**

```go
type UpdateImageModelPolicyRequest struct {
	Version int64 `json:"version" binding:"min=0"`
	Enabled bool `json:"enabled"`
	Models []service.ImageModelPolicyItem `json:"models" binding:"required"`
}
```

`GET` 返回当前策略和 schedulable capability；`PUT` 从认证 context 获取 operator user ID，调用 service Replace；version conflict 返回 HTTP 409/code `policy_version_conflict`，validation 返回 422/code `invalid_image_model_policy`；audit limit clamp 到 1–100。

更新前必须从 catalog 读取当前 schedulable models，忽略客户端 capability 字段，并拒绝任何启用但不可调度的 model。GET 响应再由服务端 catalog 补充 capability，数据库条目只保存 model、enabled 和 position。

- [ ] **Step 4: 注册管理员路由和 DI**

```go
func registerImageCanvasRoutes(admin *gin.RouterGroup, h *handler.Handlers) {
	canvas := admin.Group("/image-canvas")
	canvas.GET("/model-policy", h.Admin.ImageCanvas.GetPolicy)
	canvas.PUT("/model-policy", h.Admin.ImageCanvas.UpdatePolicy)
	canvas.GET("/model-policy/audit", h.Admin.ImageCanvas.ListAudit)
}
```

`AdminHandlers` 增加 `ImageCanvas *admin.ImageCanvasHandler`，Wire 构造器注入 policy service 和 catalog。

- [ ] **Step 5: 运行 handler 测试并提交**

Run: `cd backend && go test ./internal/handler/admin ./internal/server/routes -run 'ImageCanvas|ModelPolicy' -count=1`

Expected: PASS；非管理员经现有 AdminAuth 返回 403。

```bash
git add backend/internal/handler/admin/image_canvas_handler.go backend/internal/handler/admin/image_canvas_handler_test.go backend/internal/handler/handler.go backend/internal/handler/wire.go backend/internal/server/routes/admin.go
git commit -m "feat: expose admin image model policy"
```

### Task 6: 实现画布项目和资产 repository

**Files:**
- Create: `backend/internal/repository/image_canvas_repo.go`
- Create: `backend/internal/repository/image_canvas_repo_integration_test.go`
- Modify: `backend/internal/repository/wire.go`

- [ ] **Step 1: 写所有权和乐观并发失败测试**

```go
func TestImageCanvasRepositoryOwnershipAndVersion(t *testing.T) {
	repo, users := newImageCanvasIntegrationRepo(t)
	p, err := repo.CreateProject(context.Background(), users.A, "概念图", json.RawMessage(`{"schema_version":1,"nodes":[],"edges":[]}`), nil)
	require.NoError(t, err)
	_, err = repo.GetProject(context.Background(), users.B, p.PublicID)
	require.ErrorIs(t, err, service.ErrImageCanvasProjectNotFound)
	updated, err := repo.UpdateProject(context.Background(), users.A, p.PublicID, 1, "新名称", p.Document, nil)
	require.NoError(t, err)
	require.Equal(t, int64(2), updated.Version)
	_, err = repo.UpdateProject(context.Background(), users.A, p.PublicID, 1, "旧写入", p.Document, nil)
	require.ErrorIs(t, err, service.ErrImageCanvasProjectVersionConflict)
}
```

- [ ] **Step 2: 运行测试确认红灯**

Run: `cd backend && go test -tags=integration ./internal/repository -run ImageCanvasRepository -count=1`

Expected: FAIL，repository 构造器不存在。

- [ ] **Step 3: 定义 repository 接口和 SQL CAS**

```go
type ImageCanvasRepository interface {
	ListProjects(ctx context.Context, userID int64) ([]ImageCanvasProject, error)
	CreateProject(ctx context.Context, userID int64, name string, document json.RawMessage, assetRefs []ImageCanvasAssetReference) (*ImageCanvasProject, error)
	GetProject(ctx context.Context, userID int64, publicID string) (*ImageCanvasProject, error)
	UpdateProject(ctx context.Context, userID int64, publicID string, version int64, name string, document json.RawMessage, assetRefs []ImageCanvasAssetReference) (*ImageCanvasProject, error)
	DeleteProject(ctx context.Context, userID int64, publicID string) error
	CreateAsset(ctx context.Context, input ImageAssetCreate) (*ImageAsset, error)
	GetAsset(ctx context.Context, userID int64, publicID string) (*ImageAsset, error)
	ListOpenJobs(ctx context.Context, userID int64, projectID int64) ([]ImageJob, error)
}
```

Update SQL 必须包含：

```sql
UPDATE image_canvas_projects
SET name=$4, document=$5, version=version+1, updated_at=NOW()
WHERE public_id=$1 AND user_id=$2 AND version=$3 AND deleted_at IS NULL
RETURNING id, public_id, user_id, name, document, version, thumbnail_asset_id, created_at, updated_at;
```

零行时先按 `public_id + user_id` 查询：存在即返回 version conflict，否则 not found。Delete 只逻辑删除项目，不同步删除仍被 Job 引用的 asset。

- [ ] **Step 4: 实现资产所有权和引用约束测试**

增加测试断言：B 不能读 A 的 asset；project 属于 A 时不能创建 owner=B 的 asset；逻辑删除 asset 后下载返回 not found；对象键不从客户端输入。

- [ ] **Step 5: 运行集成测试并提交**

Run: `cd backend && go test -tags=integration ./internal/repository -run ImageCanvasRepository -count=1`

Expected: PASS。

```bash
git add backend/internal/repository/image_canvas_repo.go backend/internal/repository/image_canvas_repo_integration_test.go backend/internal/repository/wire.go backend/internal/service/image_canvas_types.go
git commit -m "feat: persist image canvas projects and assets"
```

### Task 7: 实现项目服务、JSON schema 和上传资产

**Files:**
- Create: `backend/internal/service/image_canvas_project.go`
- Create: `backend/internal/service/image_canvas_project_test.go`
- Modify: `backend/internal/service/image_canvas_types.go`

- [ ] **Step 1: 写文档 schema 和上传校验失败测试**

```go
func TestImageCanvasProjectRejectsEmbeddedDataURL(t *testing.T) {
	svc := newImageCanvasProjectFixture(t)
	doc := json.RawMessage(`{"schema_version":1,"nodes":[{"id":"n1","type":"image","metadata":{"content":"data:image/png;base64,cG5n"}}],"edges":[]}`)
	_, err := svc.Create(context.Background(), 9, "bad", doc)
	require.ErrorIs(t, err, ErrImageCanvasDocumentInvalid)
}

func TestImageCanvasUploadRejectsSpoofedMIME(t *testing.T) {
	svc := newImageCanvasProjectFixture(t)
	_, err := svc.UploadAsset(context.Background(), ImageAssetUpload{UserID: 9, FileName: "x.png", MIMEType: "image/png", Data: []byte("not-png")})
	require.ErrorIs(t, err, ErrImageAssetInvalid)
}
```

- [ ] **Step 2: 运行测试确认红灯**

Run: `cd backend && go test ./internal/service -run ImageCanvasProject -count=1`

Expected: FAIL，缺少 project service。

- [ ] **Step 3: 实现版本化文档校验**

```go
type ImageCanvasDocument struct {
	SchemaVersion int `json:"schema_version"`
	Nodes []json.RawMessage `json:"nodes"`
	Edges []json.RawMessage `json:"edges"`
	Viewport json.RawMessage `json:"viewport,omitempty"`
}
```

只接受 `schema_version=1`、最多 2000 nodes、4000 edges、JSON 总大小不超过 4 MiB。递归扫描 JSON string，拒绝 `data:`、`javascript:`、签名对象存储 query 和 `api_key`/`base_url` 字段；图片节点只允许 `asset_id` 和任务 public ID 引用。

- [ ] **Step 4: 实现上传到对象存储**

上传使用异步图片计划的 `ImageJobObjectStore`，服务端通过文件签名识别 PNG/JPEG/WebP，解码 config 获取宽高，限制 20 MiB、最大 40 megapixels。对象键固定为：

```go
objectKey := fmt.Sprintf("canvas-assets/%d/%s/original.%s", input.UserID, publicID, extension)
```

对象写入成功后创建 `image_assets`；数据库失败时删除刚写对象。生成 512px 内缩略图并写 `thumbnail_object_key`；缩略图失败不删除原图，但记录 metric 并返回无缩略图 asset。

- [ ] **Step 5: 运行服务测试并提交**

Run: `cd backend && go test ./internal/service -run 'ImageCanvasProject|ImageAsset' -count=1`

Expected: PASS。

```bash
git add backend/internal/service/image_canvas_project.go backend/internal/service/image_canvas_project_test.go backend/internal/service/image_canvas_types.go
git commit -m "feat: validate canvas documents and uploads"
```

### Task 8: 实现登录态 Canvas 项目和配置 API

**Files:**
- Create: `backend/internal/handler/image_canvas_handler.go`
- Create: `backend/internal/handler/image_canvas_handler_test.go`
- Create: `backend/internal/server/middleware/image_canvas_enabled.go`
- Create: `backend/internal/server/middleware/image_canvas_enabled_test.go`
- Modify: `backend/internal/handler/handler.go`
- Modify: `backend/internal/handler/wire.go`
- Modify: `backend/internal/server/routes/user.go`

- [ ] **Step 1: 写 Key 脱敏、项目所有权和 feature gate 测试**

```go
func TestImageCanvasConfigReturnsOwnedKeysWithoutSecrets(t *testing.T) {
	router := newImageCanvasRouter(t, imageCanvasFixture{Enabled: true, UserID: 9, APIKeyID: 42})
	w := performRequest(t, router, http.MethodGet, "/api/v1/image-canvas/config?api_key_id=42", nil)
	require.Equal(t, http.StatusOK, w.Code)
	require.NotContains(t, w.Body.String(), "sk-")
	require.Contains(t, w.Body.String(), `"id":42`)
}

func TestImageCanvasDisabledReturnsNotFound(t *testing.T) {
	router := newImageCanvasRouter(t, imageCanvasFixture{Enabled: false, UserID: 9})
	w := performRequest(t, router, http.MethodGet, "/api/v1/image-canvas/projects", nil)
	require.Equal(t, http.StatusNotFound, w.Code)
}
```

- [ ] **Step 2: 运行测试确认红灯**

Run: `cd backend && go test ./internal/handler ./internal/server/routes -run ImageCanvas -count=1`

Expected: FAIL，用户路由不存在。

- [ ] **Step 3: 实现 config 和项目 handlers**

`GET config` 返回：

```go
type ImageCanvasConfigResponse struct {
	Enabled bool `json:"enabled"`
	APIKeys []ImageCanvasAPIKey `json:"api_keys"`
	SelectedAPIKeyID *int64 `json:"selected_api_key_id"`
	PolicyVersion int64 `json:"policy_version"`
	Models []ImageModelPolicyItem `json:"models"`
}
```

没有 `api_key_id` 时返回 Keys 但 models 为空；有 ID 时验证归属并返回全站启用链与 Key group capability 的有序交集。项目 PATCH 要求 version/name/document，冲突映射 409。上传使用 multipart 单文件字段 `file` 和可选 `project_id`。

- [ ] **Step 4: 注册 JWT 路由**

```go
canvas := authenticated.Group("/image-canvas")
canvas.Use(middleware.ImageCanvasEnabled(settingService))
canvas.GET("/config", h.ImageCanvas.GetConfig)
canvas.GET("/projects", h.ImageCanvas.ListProjects)
canvas.POST("/projects", h.ImageCanvas.CreateProject)
canvas.GET("/projects/:project_id", h.ImageCanvas.GetProject)
canvas.PATCH("/projects/:project_id", h.ImageCanvas.UpdateProject)
canvas.DELETE("/projects/:project_id", h.ImageCanvas.DeleteProject)
canvas.POST("/assets", h.ImageCanvas.UploadAsset)
canvas.GET("/assets/:asset_id", h.ImageCanvas.DownloadAsset)
```

Project Create/Update 从已校验 document 提取 `{asset_public_id,node_id}`，在同一事务内验证 assets 属于当前用户、替换 `image_canvas_asset_references`，再提交项目版本；任何无权 asset 使整个保存回滚。`ImageCanvasEnabled` 每次从 SettingService 读取带现有缓存语义的 `image_canvas_enabled`；false 时返回 404/code `image_canvas_disabled`。它只挂在用户 Canvas 路由组，管理员策略路由不使用。所有 user ID 只从 `middleware.GetAuthSubjectFromContext` 取得。

- [ ] **Step 5: 运行 handler 测试并提交**

Run: `cd backend && go test ./internal/handler ./internal/server/routes -run 'ImageCanvas(Config|Project|Asset|Disabled)' -count=1`

Expected: PASS。

```bash
git add backend/internal/handler/image_canvas_handler.go backend/internal/handler/image_canvas_handler_test.go backend/internal/server/middleware/image_canvas_enabled.go backend/internal/server/middleware/image_canvas_enabled_test.go backend/internal/handler/handler.go backend/internal/handler/wire.go backend/internal/server/routes/user.go
git commit -m "feat: expose authenticated image canvas projects"
```

### Task 9: 将 Canvas 请求适配为带策略快照的 Image Job

**Files:**
- Create: `backend/internal/service/image_canvas_job.go`
- Create: `backend/internal/service/image_canvas_job_test.go`
- Modify: `backend/internal/service/image_job.go`
- Modify: `backend/internal/service/image_job_service.go`
- Modify: `backend/internal/service/image_job_billing.go`
- Modify: `backend/internal/repository/image_job_repo.go`

- [ ] **Step 1: 写 Key 归属、attempt 快照和最高预占失败测试**

```go
func TestImageCanvasJobCreateSnapshotsPolicyAndReservesMaximum(t *testing.T) {
	svc, deps := newImageCanvasJobFixture(t)
	deps.Policy("model-a", "model-b", "model-c")
	deps.KeyModels(42, 9, "model-a", "model-b", "model-c")
	deps.Price("model-a", 0.04)
	deps.Price("model-b", 0.08)
	deps.Price("model-c", 0.06)
	job, err := svc.Create(context.Background(), ImageCanvasJobCreate{
		UserID: 9, APIKeyID: 42, ProjectPublicID: "icp_test", ClientNodeID: "node_1",
		Operation: ImageOperationGeneration, SelectedModel: "model-b", Prompt: "产品图", N: 1,
		IdempotencyKey: "canvas-node-1-run-1",
	})
	require.NoError(t, err)
	require.Equal(t, []string{"model-b", "model-a", "model-c"}, job.AttemptPlan)
	require.Equal(t, 0.08, deps.ReservedUSD())
}
```

- [ ] **Step 2: 运行测试确认红灯**

Run: `cd backend && go test ./internal/service -run ImageCanvasJob -count=1`

Expected: FAIL，Canvas Job service 不存在。

- [ ] **Step 3: 扩展 Job create 和 repository 扫描字段**

```go
type ImageCanvasJobMetadata struct {
	ProjectID *int64
	ClientNodeID string
	SelectedModel string
	PolicyVersion int64
	AttemptPlan []string
}
```

`ImageJobCreate` 增加 `Canvas *ImageCanvasJobMetadata`。repository insert/scan 写入 project_id、client_node_id、selected_model、policy_version、attempt_plan，并保持非 Canvas Job 默认 Canvas=nil、attempt_plan=[]。

- [ ] **Step 4: 增加候选模型最高费用估算**

```go
func (b *ImageJobBilling) EstimateCandidates(ctx context.Context, apiKey *APIKey, sub *UserSubscription, request ImageJobRequest, models []string) (ImageJobReservation, error) {
	var highest ImageJobReservation
	for _, model := range models {
		candidate := request
		candidate.Model = model
		reservation, err := b.Estimate(ctx, apiKey, sub, candidate)
		if err != nil { return ImageJobReservation{}, err }
		if reservation.AmountUSD > highest.AmountUSD { highest = reservation }
	}
	if highest.AmountUSD <= 0 { return ImageJobReservation{}, ErrImageJobReservationUnavailable }
	return highest, nil
}
```

Canvas Create 必须验证 project ownership、Key ownership、policy enabled、selected allowed 和 input asset ownership，并调用现有图片请求 parser、参数校验和 ContentModerationService 完成与 `/v1/images/*` 相同的前置审核；审核通过后才构造 attempt plan 和调用 `EstimateCandidates`。request digest 包含 APIKeyID、policy version、attempt plan、project 和 node，防止同幂等键改变执行策略。

- [ ] **Step 5: 运行 Job 与旧异步创建回归**

Run: `cd backend && go test ./internal/service ./internal/repository -run 'ImageCanvasJob|ImageJob(Service|Billing|Repository)' -count=1`

Expected: PASS；旧 `/v1/images` Job 的空 Canvas metadata 不改变序列化结果。

- [ ] **Step 6: 提交 Canvas Job 创建**

```bash
git add backend/internal/service/image_canvas_job.go backend/internal/service/image_canvas_job_test.go backend/internal/service/image_job.go backend/internal/service/image_job_service.go backend/internal/service/image_job_billing.go backend/internal/repository/image_job_repo.go
git commit -m "feat: create canvas jobs with model policy snapshots"
```

### Task 10: 实现模型级回退、partial 边界和实际模型结算

**Files:**
- Modify: `backend/internal/service/image_job_worker.go`
- Modify: `backend/internal/service/image_job_worker_test.go`
- Modify: `backend/internal/service/image_job_billing.go`
- Modify: `backend/internal/service/image_job_cleanup.go`
- Modify: `backend/internal/service/image_job_cleanup_test.go`
- Modify: `backend/internal/service/image_job_metrics.go`
- Modify: `backend/internal/repository/image_job_repo.go`
- Modify: `backend/internal/repository/image_canvas_repo.go`
- Modify: `backend/internal/handler/admin/image_job_handler.go`

- [ ] **Step 1: 写服务故障回退和业务错误停止测试**

```go
func TestImageJobWorkerFallsBackAcrossModelsBeforeAnyFinalImage(t *testing.T) {
	worker, deps := newCanvasImageWorkerFixture(t, []string{"model-b", "model-a", "model-c"})
	deps.Executor.Fail("model-b", ImageExecutionError{Status: 504, Retryable: true})
	deps.Executor.Fail("model-a", ImageExecutionError{Status: 429, Retryable: true})
	deps.Executor.Succeed("model-c", finalPNG("model-c"))
	worker.runOnce(context.Background(), "worker-1")
	require.Equal(t, []string{"model-b", "model-a", "model-c"}, deps.Executor.Models())
	require.Equal(t, "model-c", deps.Job().SuccessfulModel)
	require.Equal(t, 1, deps.Billing.SettleCount())
}

func TestImageJobWorkerDoesNotFallbackContentPolicy(t *testing.T) {
	worker, deps := newCanvasImageWorkerFixture(t, []string{"model-b", "model-a"})
	deps.Executor.Fail("model-b", ImageExecutionError{Status: 400, Code: "content_policy_violation", Retryable: false})
	worker.runOnce(context.Background(), "worker-1")
	require.Equal(t, []string{"model-b"}, deps.Executor.Models())
}
```

- [ ] **Step 2: 写 final 后错误不回退测试**

```go
func TestImageJobWorkerKeepsPartialWithoutMixingModels(t *testing.T) {
	worker, deps := newCanvasImageWorkerFixture(t, []string{"model-b", "model-a"})
	deps.Executor.FinalThenFail("model-b", finalPNG("model-b"), ImageExecutionError{Status: 500, Retryable: true})
	worker.runOnce(context.Background(), "worker-1")
	require.Equal(t, []string{"model-b"}, deps.Executor.Models())
	require.Equal(t, ImageJobStatusPartial, deps.Job().Status)
	require.Equal(t, "model-b", deps.Job().SuccessfulModel)
	require.Equal(t, 1, deps.Billing.SettleCount())
}
```

- [ ] **Step 3: 运行测试确认红灯**

Run: `cd backend && go test ./internal/service -run 'FallsBackAcrossModels|DoesNotFallbackContentPolicy|KeepsPartialWithoutMixingModels' -count=1`

Expected: FAIL，worker 只执行 request 原模型。

- [ ] **Step 4: 实现 attempt loop 和错误分类**

```go
func isModelFallbackEligible(err error) bool {
	var execErr *ImageExecutionError
	if !errors.As(err, &execErr) { return false }
	if execErr.Code == "content_policy_violation" { return false }
	return execErr.Retryable && (execErr.Status == 0 || execErr.Status == 429 || execErr.Status >= 500)
}
```

Canvas Job 使用固化 `AttemptPlan`；普通异步 Job 使用单元素 `{request.Model}`。每次 attempt 克隆 request 并替换 model，建立独立 collecting/persisting sink。attempt log 追加 `{model,position,started_at,latency_ms,error_class,final_count}`。err=nil 完成；final_count>0 时 partial 并停止；eligible 且还有模型时保持 `status=running` 并把 `execution_phase` 置为 `falling_back`；存储阶段同理使用 `execution_phase=saving`；其他错误进入 failed。不得向数据库 status 约束加入 falling_back 或 saving。

- [ ] **Step 5: 将结果创建为 asset 并按 successful model 结算**

每张 final 图片先使用确定性对象键 `{jobPublicID}/results/{index}` 写 store，再在事务中 upsert `image_job_results` 和 `image_assets`，asset source_type=generated、owner/project/origin_job 来自 Job。结算 input 的 model 必须使用 successful_model，不使用 selected_model。重复 worker 通过 job result unique key 和 usage dedup 不重复写资产或扣费。

- [ ] **Step 6: 增加清理、指标和管理员脱敏详情**

ImageJobCleanup 扫描 `deleted_at` 已超过 24 小时，或所属 project 已删除且保留期已过的 assets；只有 `image_canvas_asset_references` 无记录、没有未过期 job/result 引用且未被其他 derived asset 的 parent list 引用时才可清理。先删除 original/thumbnail 对象再硬删除行；对象删除失败保留行等待下轮。Metrics 按 selected model、attempt model、position、error class、phase latency 和 successful model 计数。管理员 ImageJob response 增加 policy_version、selected_model、successful_model 和脱敏 attempt log；普通用户响应不含内部 upstream message。

- [ ] **Step 7: 运行 worker、billing、cleanup、metrics 和 failover 回归并提交**

Run: `cd backend && go test ./internal/service ./internal/repository ./internal/handler/admin -run 'Image(JobWorker|JobBilling|JobCleanup|JobMetrics|CanvasJob|Images.*Failover)' -count=1`

Expected: PASS；三模型服务错误链最终只结算一次；partial 不调用第二模型。

```bash
git add backend/internal/service/image_job_worker.go backend/internal/service/image_job_worker_test.go backend/internal/service/image_job_billing.go backend/internal/service/image_job_cleanup.go backend/internal/service/image_job_cleanup_test.go backend/internal/service/image_job_metrics.go backend/internal/repository/image_job_repo.go backend/internal/repository/image_canvas_repo.go backend/internal/handler/admin/image_job_handler.go
git commit -m "feat: add canvas image model fallback"
```

### Task 11: 暴露 Canvas Job、事件流、取消和结果 API

**Files:**
- Modify: `backend/internal/handler/image_canvas_handler.go`
- Modify: `backend/internal/handler/image_canvas_handler_test.go`
- Modify: `backend/internal/server/routes/user.go`

- [ ] **Step 1: 写创建、SSE 恢复和取消失败测试**

```go
func TestImageCanvasCreateJobReturnsAccepted(t *testing.T) {
	router := newImageCanvasRouter(t, imageCanvasFixture{Enabled: true, UserID: 9, APIKeyID: 42})
	body := `{"project_id":"icp_test","client_node_id":"node_1","operation":"generation","api_key_id":42,"selected_model":"model-b","prompt":"产品图","input_asset_ids":[],"parameters":{"size":"1024x1024","n":1,"quality":"high","output_format":"png"}}`
	w := performJSONWithHeader(t, router, http.MethodPost, "/api/v1/image-canvas/jobs", body, "Idempotency-Key", "node-1-run-1")
	require.Equal(t, http.StatusAccepted, w.Code)
	require.Contains(t, w.Body.String(), `"attempt_plan":["model-b","model-a"]`)
}
```

SSE 测试使用可取消 request context，断言第一帧是 `event: snapshot`，状态变更后有 `event: job`，15 秒无变化写 comment heartbeat，终态后 handler 返回。

- [ ] **Step 2: 运行测试确认红灯**

Run: `cd backend && go test ./internal/handler -run 'ImageCanvas(CreateJob|Events|Cancel|Result)' -count=1`

Expected: FAIL，任务路由不存在。

- [ ] **Step 3: 注册任务路由**

```go
canvas.POST("/jobs", h.ImageCanvas.CreateJob)
canvas.GET("/jobs/:job_id", h.ImageCanvas.GetJob)
canvas.GET("/jobs/:job_id/events", h.ImageCanvas.StreamJobEvents)
canvas.DELETE("/jobs/:job_id", h.ImageCanvas.CancelJob)
canvas.GET("/jobs/:job_id/results/:index", h.ImageCanvas.DownloadJobResult)
```

Create 强制 `Idempotency-Key` 1–255 bytes；缺失返回 400。Get/Cancel/Result 同时按 JWT user ID 和 Job 的 api_key ownership 检查，其他用户一律 404。

- [ ] **Step 4: 实现无 token query 的 SSE**

handler 使用 `text/event-stream`、`Cache-Control: no-cache`、`X-Accel-Buffering: no`。服务端每秒查询一次 Job，仅 JSON snapshot 变化时发送事件；15 秒发送 `: keepalive`；request context 取消立即退出。事件 JSON 只含 status、phase、attempt position、公开 model、completed count、asset IDs 和脱敏错误 code。

- [ ] **Step 5: 运行 handler 和路由测试并提交**

Run: `cd backend && go test ./internal/handler ./internal/server/routes -run 'ImageCanvas.*(Job|Events|Result)' -count=1`

Expected: PASS；Authorization 只从 header 读取，测试 URL 不含 token。

```bash
git add backend/internal/handler/image_canvas_handler.go backend/internal/handler/image_canvas_handler_test.go backend/internal/server/routes/user.go
git commit -m "feat: expose canvas image job lifecycle"
```

### Task 12: 完成后端依赖注入和全量回归

**Files:**
- Modify: `backend/internal/repository/wire.go`
- Modify: `backend/internal/service/wire.go`
- Modify: `backend/internal/handler/wire.go`
- Modify: `backend/cmd/server/wire.go`
- Modify: `backend/cmd/server/wire_gen.go`

- [ ] **Step 1: 写启动图失败测试**

在现有 server wiring test 增加断言：ImageCanvasHandler、Admin ImageCanvasHandler、ImageModelPolicyService、ImageCanvasProjectService 和 ImageCanvasJobService 均非 nil；feature flag=false 时 worker 仍可处理已创建的旧异步 Job，但 Canvas handler 拒绝新请求。

- [ ] **Step 2: 运行测试确认红灯**

Run: `cd backend && go test ./cmd/server ./internal/handler -run 'Wire|ImageCanvasDependencies' -count=1`

Expected: FAIL，provider graph 不完整。

- [ ] **Step 3: 注册 providers 并生成 Wire**

Repository ProviderSet 加入 `NewImageModelPolicyRepository`、`NewImageCanvasRepository`；Service ProviderSet 加入 catalog、policy、project、canvas job；Handler ProviderSet 加入两个 handlers。

Run: `cd backend && wire ./cmd/server`

Expected: `wire_gen.go` 只出现新增 provider 依赖和构造参数变化。

- [ ] **Step 4: 运行后端完整测试**

Run: `cd backend && go test ./... -count=1`

Expected: PASS。

Run: `cd backend && go test -tags=integration ./internal/repository -run 'Image(Canvas|ModelPolicy|Job)' -count=1`

Expected: PASS。

- [ ] **Step 5: 审阅与用户 dirty diff 的重叠文件**

Run:

```bash
git diff HEAD^ -- backend/cmd/server/wire_gen.go backend/internal/service/openai_account_scheduler.go backend/internal/service/openai_gateway_service.go
git -C /home/ubuntu/sub2api diff -- backend/cmd/server/wire_gen.go backend/internal/service/openai_account_scheduler.go backend/internal/service/openai_gateway_service.go
```

Expected: 本分支 scheduler/gateway 只包含已整合前置提交；不得覆盖用户 fallback 语义。

- [ ] **Step 6: 提交后端 wiring**

```bash
git add backend/internal/repository/wire.go backend/internal/service/wire.go backend/internal/handler/wire.go backend/cmd/server/wire.go backend/cmd/server/wire_gen.go
git commit -m "feat: wire image canvas backend"
```

### Task 13: 导入固定 Infinite Canvas 源码和建立独立构建

**Files:**
- Create: `scripts/vendor-infinite-canvas.sh`
- Create: `frontend/pnpm-workspace.yaml`
- Create: `frontend/infinite-canvas/package.json`
- Create: `frontend/infinite-canvas/tsconfig.json`
- Create: `frontend/infinite-canvas/postcss.config.mjs`
- Create: `frontend/infinite-canvas/vite.config.ts`
- Create: `frontend/infinite-canvas/src/entry.tsx`
- Create: `frontend/infinite-canvas/src/canvas-app.tsx`
- Create: `frontend/infinite-canvas/src/host-context.tsx`
- Create: `frontend/infinite-canvas/UPSTREAM.md`
- Create: `frontend/infinite-canvas/NOTICE`
- Copy: `frontend/infinite-canvas/LICENSE`
- Copy: `frontend/infinite-canvas/LICENSE.upstream`
- Modify: `frontend/package.json`
- Modify: `frontend/pnpm-lock.yaml`
- Modify: `frontend/.gitignore`

- [ ] **Step 1: 写固定提交 vendor 脚本**

```bash
#!/usr/bin/env bash
set -euo pipefail
readonly UPSTREAM_URL="https://github.com/basketikun/infinite-canvas.git"
readonly UPSTREAM_COMMIT="ea0414e88cffa6b522cc13c0613b3c8085983a53"
readonly DEST="frontend/infinite-canvas/src/upstream"
tmp_dir="$(mktemp -d)"
trap 'rm -rf -- "$tmp_dir"' EXIT
git clone --filter=blob:none --no-checkout "$UPSTREAM_URL" "$tmp_dir/repo"
git -C "$tmp_dir/repo" checkout --detach "$UPSTREAM_COMMIT"
test "$(git -C "$tmp_dir/repo" rev-parse HEAD)" = "$UPSTREAM_COMMIT"
mkdir -p "$DEST"
cp -a "$tmp_dir/repo/web/src/." "$DEST/"
cp "$tmp_dir/repo/LICENSE" frontend/infinite-canvas/LICENSE.upstream
cp "$tmp_dir/repo/LICENSE" frontend/infinite-canvas/LICENSE
```

脚本只用于初次导入或在干净分支重新生成，不在已有 Sub2API 修改上直接覆盖执行。

- [ ] **Step 2: 执行导入并记录来源**

Run: `bash scripts/vendor-infinite-canvas.sh`

Expected: `src/upstream/pages/canvas`、`components/canvas`、`lib/canvas` 和 LICENSE 存在。`UPSTREAM.md` 精确记录 URL、commit、导入日期 2026-08-02 和本地修改目录；`NOTICE` 说明删除 direct-provider config、改用 Sub2API API、服务端持久化及日期。

- [ ] **Step 3: 建立 workspace 和微前端 package**

`frontend/pnpm-workspace.yaml`：

```yaml
packages:
  - .
  - infinite-canvas
```

`frontend/infinite-canvas/package.json` 使用名称 `@sub2api/infinite-canvas`、`private:true`、`license:AGPL-3.0-only`，scripts 为 `build:vite build`、`typecheck:tsc --noEmit`、`test:vitest run`。依赖固定包含 React 19、react-dom、react-router-dom、zustand、antd、`@ant-design/cssinjs`、lucide-react、nanoid、file-saver、fflate、localforage、motion、radix-ui、copy-to-clipboard、dayjs、clsx、class-variance-authority、tailwind-merge、Tailwind CSS 4 和 `@tailwindcss/postcss`；不得加入 upstream agent server 依赖。

`postcss.config.mjs`：

```js
export default { plugins: { '@tailwindcss/postcss': {} } }
```

- [ ] **Step 4: 配置 ESM manifest 构建**

```ts
import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import react from '@vitejs/plugin-react'
import { defineConfig, type Plugin } from 'vite'

function releaseNotices(): Plugin {
  return {
    name: 'canvas-release-notices',
    generateBundle() {
      for (const fileName of ['LICENSE', 'LICENSE.upstream', 'NOTICE']) {
        this.emitFile({ type: 'asset', fileName, source: readFileSync(resolve(__dirname, fileName)) })
      }
    },
  }
}

export default defineConfig({
  plugins: [react(), releaseNotices()],
  resolve: { alias: { '@': resolve(__dirname, 'src/upstream'), '@sub2api': resolve(__dirname, 'src') } },
  build: {
    outDir: '../public/infinite-canvas',
    emptyOutDir: true,
    manifest: 'manifest.json',
    lib: { entry: resolve(__dirname, 'src/entry.tsx'), formats: ['es'] },
    rollupOptions: { output: { entryFileNames: 'assets/canvas-[hash].js', chunkFileNames: 'assets/chunk-[hash].js', assetFileNames: 'assets/[name]-[hash][extname]' } },
  },
})
```

`entry.tsx` 导入 `antd/dist/reset.css` 和 `@/styles/globals.css`，但这些 CSS 最终只会由 Vue host 注入 Shadow Root，不得直接添加到 document head。Root `build` 改为 `pnpm --filter @sub2api/infinite-canvas build && vue-tsc -b && vite build`，root Vite 随后把 `public/infinite-canvas` 复制进 backend dist。

- [ ] **Step 5: 添加最小生命周期入口测试**

```tsx
it('mounts and unmounts without leaking the root', () => {
  const element = document.createElement('div')
  const handle = mountCanvas(element, fixtureHostContext())
  expect(element.childElementCount).toBe(1)
  handle.unmount()
  expect(element.childElementCount).toBe(0)
})
```

- [ ] **Step 6: 安装、测试并提交导入基线**

Run: `cd frontend && pnpm install && pnpm --filter @sub2api/infinite-canvas test && pnpm --filter @sub2api/infinite-canvas typecheck && pnpm --filter @sub2api/infinite-canvas build`

Expected: 生命周期测试 PASS；若 upstream 页面尚未接入口，build 只产出最小 entry。

```bash
git add scripts/vendor-infinite-canvas.sh frontend/pnpm-workspace.yaml frontend/package.json frontend/pnpm-lock.yaml frontend/.gitignore frontend/infinite-canvas
git commit -m "feat: vendor infinite canvas source"
```

### Task 14: 实现 React Host Context 和类型化 Canvas SDK

**Files:**
- Create: `frontend/infinite-canvas/src/host-context.tsx`
- Modify: `frontend/infinite-canvas/src/canvas-app.tsx`
- Create: `frontend/infinite-canvas/src/api/canvas-api.ts`
- Create: `frontend/infinite-canvas/src/api/canvas-api.test.ts`
- Modify: `frontend/infinite-canvas/src/entry.tsx`

- [ ] **Step 1: 写 API bridge 和 SSE parser 失败测试**

```ts
it('creates a job through the injected request bridge', async () => {
  const host = fixtureHostContext()
  host.request = vi.fn().mockResolvedValue({ id: 'imgjob_test', status: 'queued' })
  const api = createCanvasAPI(host)
  await api.createJob({ project_id: 'icp_test', client_node_id: 'node_1', operation: 'generation', api_key_id: 42, selected_model: 'model-b', prompt: '产品图', input_asset_ids: [], parameters: { size: '1024x1024', n: 1, quality: 'high', output_format: 'png' } }, 'node-1-run-1')
  expect(host.request).toHaveBeenCalledWith('POST', '/image-canvas/jobs', expect.any(Object), { 'Idempotency-Key': 'node-1-run-1' })
})
```

- [ ] **Step 2: 运行测试确认红灯**

Run: `cd frontend && pnpm --filter @sub2api/infinite-canvas test -- canvas-api`

Expected: FAIL，SDK 不存在。

- [ ] **Step 3: 定义唯一公共宿主契约**

```ts
export interface CanvasHostContext {
  apiBaseURL: string
  locale: string
  theme: 'light' | 'dark'
  routeMode: 'user' | 'admin'
  request<T>(method: string, path: string, body?: unknown, headers?: Record<string, string>): Promise<T>
  stream(path: string, init?: RequestInit): Promise<ReadableStream<Uint8Array>>
  navigate(path: string): void
  notify(level: 'success' | 'warning' | 'error', message: string): void
}

export interface CanvasHandle {
  updateContext(context: CanvasHostContext): void
  unmount(): void
}
```

Context 用 React Context 保存；`updateContext` 更新 store 而不重新 mount。`canvas-app.tsx` 使用 Ant Design `StyleProvider` 把 CSS-in-JS style container 指向 mount element 所在 ShadowRoot，并用 `ConfigProvider.getPopupContainer` 把 popup、menu 和 modal 留在同一 ShadowRoot。

- [ ] **Step 4: 实现 SDK 和增量 SSE parser**

SDK 覆盖 config、project CRUD、asset upload/download URL、create/get/cancel job 和 stream。SSE parser 必须跨 chunk 保留 buffer，以空行分隔 event，忽略 comment heartbeat，只 JSON.parse `data:` 内容；stream 失败后由调用者轮询。

- [ ] **Step 5: 禁止敏感浏览器存储**

新增测试扫描 bundle source imports，断言 React package 不读取 `auth_token`、`refresh_token`、`apiKey` 或 `baseUrl`。允许 localStorage 的 key 只限 `canvas-side-panel-width`、主题外观和未同步草稿摘要。

- [ ] **Step 6: 运行测试并提交**

Run: `cd frontend && pnpm --filter @sub2api/infinite-canvas test && pnpm --filter @sub2api/infinite-canvas typecheck`

Expected: PASS。

```bash
git add frontend/infinite-canvas/src/entry.tsx frontend/infinite-canvas/src/host-context.tsx frontend/infinite-canvas/src/api
git commit -m "feat: add infinite canvas host api bridge"
```

### Task 15: 将上游项目 store 改为服务端持久化

**Files:**
- Create: `frontend/infinite-canvas/src/stores/server-canvas-store.ts`
- Create: `frontend/infinite-canvas/src/stores/server-canvas-store.test.ts`
- Create: `frontend/infinite-canvas/src/stores/canvas-session-store.ts`
- Modify: `frontend/infinite-canvas/src/upstream/stores/canvas/use-canvas-store.ts`
- Modify: `frontend/infinite-canvas/src/upstream/pages/canvas/index.tsx`
- Modify: `frontend/infinite-canvas/src/upstream/pages/canvas/project.tsx`

- [ ] **Step 1: 写 hydration、debounce 和 conflict 失败测试**

```ts
it('does not overwrite a newer server project', async () => {
  const api = fixtureCanvasAPI({ updateError: { status: 409, code: 'project_version_conflict' } })
  const store = createServerCanvasStore(api)
  await store.openProject('icp_test')
  store.updateDocument('icp_test', fixtureDocument({ title: 'local edit' }))
  await vi.advanceTimersByTimeAsync(500)
  expect(store.getState().projects.icp_test.saveState).toBe('conflict')
  expect(store.getState().projects.icp_test.localDraft).toBeDefined()
})
```

- [ ] **Step 2: 运行测试确认红灯**

Run: `cd frontend && pnpm --filter @sub2api/infinite-canvas test -- server-canvas-store`

Expected: FAIL，store 不存在。

- [ ] **Step 3: 实现服务端 store**

Store state 使用 `{projectsByID, order, hydrated, saveState}`；open 从 API 获取 document/version/open_jobs；update 立即更新内存并在 400ms 后 PATCH 当前 version；成功替换 version；409 保留 localDraft 并暴露 `reloadServer()`、`saveAsNew()`。localForage 只保存 `{projectID, baseVersion, localDraft, savedAt}`，服务端版本更新后删除草稿。

- [ ] **Step 4: 适配上游项目页**

把上游 `persist` Zustand store 替换为 server store facade，保留组件调用的 create/open/rename/delete/update 方法签名。项目 ID 使用服务端 public ID。导入会为每个项目调用 Create，图片先上传获得 asset ID；导出从 asset download 获取 Blob，不内联签名 URL。

- [ ] **Step 5: 删除不在第一版入口的本地系统**

从 mounted dependency graph 移除 `use-agent-store`、agent bridge、plugin host、audio/video request 和 provider config dialog。保留 image/text/config/group node。运行：

```bash
rg -n "requestAudioGeneration|requestVideoGeneration|useAgentStore|usePluginHost|openConfigDialog|apiKey|baseUrl" frontend/infinite-canvas/src/entry.tsx frontend/infinite-canvas/src/upstream/pages/canvas frontend/infinite-canvas/src/upstream/components/canvas
```

Expected: mounted graph 中无这些 import 或交互入口；源码归档中的未挂载历史文件可保留并在 NOTICE 标注未启用。

- [ ] **Step 6: 运行 store 测试和 build 并提交**

Run: `cd frontend && pnpm --filter @sub2api/infinite-canvas test && pnpm --filter @sub2api/infinite-canvas typecheck && pnpm --filter @sub2api/infinite-canvas build`

Expected: PASS；manifest 和 hashed JS/CSS 生成到 `frontend/public/infinite-canvas`。

```bash
git add frontend/infinite-canvas/src/stores frontend/infinite-canvas/src/upstream/stores/canvas/use-canvas-store.ts frontend/infinite-canvas/src/upstream/pages/canvas/index.tsx frontend/infinite-canvas/src/upstream/pages/canvas/project.tsx frontend/infinite-canvas/NOTICE
git commit -m "feat: persist infinite canvas projects on server"
```

### Task 16: 将上游图片生成改为异步 Canvas Job

**Files:**
- Create: `frontend/infinite-canvas/src/jobs/canvas-job-controller.ts`
- Create: `frontend/infinite-canvas/src/jobs/canvas-job-controller.test.ts`
- Modify: `frontend/infinite-canvas/src/upstream/services/api/image.ts`
- Modify: `frontend/infinite-canvas/src/upstream/pages/canvas/project.tsx`
- Modify: `frontend/infinite-canvas/src/upstream/components/canvas/canvas-node-generation.ts`
- Modify: `frontend/infinite-canvas/src/upstream/types/canvas.ts`

- [ ] **Step 1: 写 API Key/model 选择和断线轮询失败测试**

```ts
it('submits the selected key and model then resumes with polling', async () => {
  const api = fixtureCanvasAPI()
  api.streamJob.mockRejectedValue(new Error('offline'))
  api.getJob.mockResolvedValueOnce({ id: 'imgjob_test', status: 'running' }).mockResolvedValueOnce({ id: 'imgjob_test', status: 'completed', results: [{ asset_id: 'asset_test' }] })
  const controller = createCanvasJobController(api)
  const result = await controller.run(fixtureGeneration({ apiKeyID: 42, model: 'model-b' }))
  expect(api.createJob).toHaveBeenCalledWith(expect.objectContaining({ api_key_id: 42, selected_model: 'model-b' }), expect.any(String))
  expect(result.status).toBe('completed')
})
```

- [ ] **Step 2: 运行测试确认红灯**

Run: `cd frontend && pnpm --filter @sub2api/infinite-canvas test -- canvas-job-controller`

Expected: FAIL，controller 不存在。

- [ ] **Step 3: 实现 controller 状态机**

状态固定为 `queued -> running -> falling_back -> saving -> completed|partial|failed|canceled`。create 后优先消费 SSE；断线以 1s、2s、4s、5s 上限退避轮询；AbortSignal 触发 DELETE cancel。刷新后从 project `open_jobs` 恢复 controller。幂等键格式为 `{projectID}:{clientNodeID}:{generationNonce}`，同一次 UI retry 保持不变，用户明确“再次生成”递增 nonce。

- [ ] **Step 4: 替换 direct provider image service**

`requestGeneration` 和 `requestEdit` 改为调用 controller，输入图片必须先是 server asset ID；本地裁剪/mask 结果先 UploadAsset。删除 axios 直连 `/v1/images/generations`、`/v1/images/edits`、Gemini URL、model plugin 和 AiConfig 参数。返回结果使用 asset download URL，不保存 Base64。

- [ ] **Step 5: 扩展节点 metadata**

```ts
type CanvasNodeMetadata = {
  jobId?: string
  assetId?: string
  selectedModel?: string
  successfulModel?: string
  attemptPosition?: number
  generationStatus?: 'queued' | 'running' | 'falling_back' | 'saving' | 'completed' | 'partial' | 'failed' | 'canceled'
  errorCode?: string
}
```

节点 loading 文案不得改变节点尺寸。completed/partial 写 asset ID 和 successful model；failed 保留 prompt 和 references 以便用户重试。

- [ ] **Step 6: 运行生成链测试并提交**

Run: `cd frontend && pnpm --filter @sub2api/infinite-canvas test && pnpm --filter @sub2api/infinite-canvas typecheck`

Expected: PASS；测试网络记录中只有 `/api/v1/image-canvas/*`。

```bash
git add frontend/infinite-canvas/src/jobs frontend/infinite-canvas/src/upstream/services/api/image.ts frontend/infinite-canvas/src/upstream/pages/canvas/project.tsx frontend/infinite-canvas/src/upstream/components/canvas/canvas-node-generation.ts frontend/infinite-canvas/src/upstream/types/canvas.ts
git commit -m "feat: run infinite canvas images through async jobs"
```

### Task 17: 完成图片画布控件、响应式和许可证入口

**Files:**
- Modify: `frontend/infinite-canvas/src/upstream/components/canvas/canvas-top-bar.tsx`
- Modify: `frontend/infinite-canvas/src/upstream/components/canvas/canvas-node-prompt-panel.tsx`
- Modify: `frontend/infinite-canvas/src/upstream/components/canvas/canvas-side-panel.tsx`
- Modify: `frontend/infinite-canvas/src/upstream/components/canvas/canvas-toolbar.tsx`
- Modify: `frontend/infinite-canvas/src/upstream/components/canvas/canvas-node.tsx`
- Create: `frontend/infinite-canvas/src/components/api-key-model-selector.tsx`
- Create: `frontend/infinite-canvas/src/components/source-notice-dialog.tsx`
- Create: `frontend/infinite-canvas/src/i18n.tsx`
- Create: `frontend/infinite-canvas/src/styles/sub2api-canvas.css`
- Create: `frontend/infinite-canvas/src/components/canvas-ui.test.tsx`

- [ ] **Step 1: 写 Key/model 能力过滤 UI 失败测试**

```tsx
it('shows only models allowed by the selected key and operation', async () => {
  render(<APIKeyModelSelector config={fixtureConfig()} operation="edit" value={{ apiKeyID: 42, model: 'model-b' }} onChange={vi.fn()} />)
  await userEvent.click(screen.getByRole('combobox', { name: '模型' }))
  expect(screen.getByRole('option', { name: 'model-b' })).toBeVisible()
  expect(screen.queryByRole('option', { name: 'model-a' })).toBeNull()
})
```

- [ ] **Step 2: 运行测试确认红灯**

Run: `cd frontend && pnpm --filter @sub2api/infinite-canvas test -- canvas-ui`

Expected: FAIL，selector 不存在。

- [ ] **Step 3: 实现顶部选择和空状态**

API Key selector 显示 name/group，不显示 secret；选择 Key 后刷新 config 并清除不再可用的 model。没有 Key 时显示唯一主要命令“创建 API Key”，调用 host.navigate('/keys')。模型 selector 使用当前 operation capability；尺寸、输入图数量、mask 和输出数量控件也按 capability 限制，disabled option tooltip 说明不兼容原因。

- [ ] **Step 4: 完成图片画布核心操作**

保留上游图片、文本、配置、分组节点；缩放、平移、多选、连线、复制、删除、撤销、重做、裁剪、分割、mask、导入、导出和连续分支必须有组件测试。音频、视频、agent 和 plugin 按钮不渲染。按钮使用 lucide-react 图标和 `aria-label`/tooltip；节点状态容器固定高度。

- [ ] **Step 5: 实现桌面和移动布局**

CSS 运行在 Vue host 创建的 ShadowRoot 内，并继续使用根前缀 `.sub2api-canvas-root`。桌面高度 `100%`，项目面板可收起；小于 768px 时工具栏变图标菜单、项目面板抽屉、prompt 为底部面板。不要用 viewport width 改字号；画布节点和工具栏用 min/max/aspect-ratio 保持稳定。

- [ ] **Step 6: 实现 AGPL 关于/源码入口**

对话框显示 upstream URL、commit、AGPL-3.0、修改日期、无担保声明和 `VITE_CANVAS_SOURCE_URL`。production build 中 URL 为空时 `vite.config.ts` 抛错；入口位于工具栏 overflow 菜单且键盘可访问。

- [ ] **Step 7: 完成 React 画布 i18n**

`i18n.tsx` 提供 `zh-CN` 和 `en` 两套静态词典，locale 来自 HostContext。把 mounted dependency graph 中所有用户可见的硬编码中文替换为 `t(key)`；测试遍历两套 key 集合完全一致，并用最长错误文案渲染节点和 toolbar 验证无溢出。

- [ ] **Step 8: 运行微前端测试和 build 并提交**

Run: `cd frontend && VITE_CANVAS_SOURCE_URL=https://example.test/corresponding-source pnpm --filter @sub2api/infinite-canvas test && VITE_CANVAS_SOURCE_URL=https://example.test/corresponding-source pnpm --filter @sub2api/infinite-canvas build`

Expected: PASS；manifest、JS、CSS、LICENSE 和 NOTICE 都在 `frontend/public/infinite-canvas`。

```bash
git add frontend/infinite-canvas
git commit -m "feat: complete infinite image canvas experience"
```

### Task 18: 挂载 Vue 用户和管理员入口

**Files:**
- Create: `frontend/src/components/image-canvas/canvasLoader.ts`
- Create: `frontend/src/components/image-canvas/canvasHostBridge.ts`
- Create: `frontend/src/components/image-canvas/ImageCanvasHost.vue`
- Create: `frontend/src/components/image-canvas/__tests__/ImageCanvasHost.spec.ts`
- Create: `frontend/src/views/user/ImageCanvasView.vue`
- Create: `frontend/src/views/admin/ImageCanvasAdminView.vue`
- Modify: `frontend/src/router/index.ts`
- Modify: `frontend/src/components/layout/AppSidebar.vue`
- Modify: `frontend/src/composables/useRoutePrefetch.ts`
- Modify: `frontend/src/api/client.ts`

- [ ] **Step 1: 写 manifest 加载和 unmount 失败测试**

```ts
it('loads, mounts, updates context and unmounts the canvas module', async () => {
  const module = fixtureCanvasModule()
  mockCanvasManifest(module)
  const wrapper = mount(ImageCanvasHost, { props: { routeMode: 'user' } })
  await flushPromises()
  expect(module.mountCanvas).toHaveBeenCalledOnce()
  expect(wrapper.element.shadowRoot?.querySelector('link[rel="stylesheet"]')).not.toBeNull()
  expect(document.head.querySelector('link[data-image-canvas]')).toBeNull()
  await wrapper.setProps({ routeMode: 'admin' })
  expect(module.handle.updateContext).toHaveBeenCalled()
  wrapper.unmount()
  expect(module.handle.unmount).toHaveBeenCalledOnce()
})
```

- [ ] **Step 2: 运行测试确认红灯**

Run: `cd frontend && pnpm test:run -- src/components/image-canvas/__tests__/ImageCanvasHost.spec.ts`

Expected: FAIL，host 不存在。

- [ ] **Step 3: 实现同源 manifest loader**

Loader 用 `fetch('/infinite-canvas/manifest.json', {cache:'no-store'})`，定位 `src/entry.tsx` entry并 `import(/* @vite-ignore */ moduleURL)`。Host 在自己的 container 上创建 ShadowRoot，把 manifest CSS link 插入 ShadowRoot，再创建 mount element；CSS 不进入 document head。只接受 pathname 位于 `/infinite-canvas/` 的同源 URL，拒绝外部 origin。module 必须导出 mountCanvas，unmount 时移除 React root、ShadowRoot 内 links、portal 和事件监听。

- [ ] **Step 4: 实现认证 API bridge**

普通 request 委托现有 `apiClient`，所以复用 Bearer 和 refresh。stream 使用 fetch 时从 localStorage 读取当前 auth token只在调用瞬间放 Authorization header，401 时调用现有共享 refresh helper 后重试一次；React 不接触 token。将 client.ts 的 refresh 逻辑抽成导出 `refreshAccessToken()`，axios interceptor 和 stream 共用同一 single-flight。

- [ ] **Step 5: 注册路由和两个左侧入口**

用户路由 `/images` 使用 `ImageCanvasView`，admin `/admin/images` 使用 admin view；两者 meta 都需要 auth，admin 额外 requiresAdmin。Sidebar 添加 Image icon；用户 buildSelfNavItems 绑定 `FeatureFlags.imageCanvas`，admin baseItems 始终显示入口。本任务先挂载管理员 canvas host；flag=false 时该区域只显示禁用状态。Task 19 在同一 view 中加入可用的模型配置 tab。

- [ ] **Step 6: 运行导航、host 和类型测试并提交**

Run: `cd frontend && pnpm test:run -- src/components/image-canvas src/components/layout/__tests__/AppSidebar.spec.ts src/__tests__/integration/navigation.spec.ts && pnpm typecheck`

Expected: PASS；flag=false 时用户菜单隐藏且用户直接路由显示不可用，不产生 canvas manifest 请求；管理员菜单和路由仍可访问。

```bash
git add frontend/src/components/image-canvas frontend/src/views/user/ImageCanvasView.vue frontend/src/views/admin/ImageCanvasAdminView.vue frontend/src/router/index.ts frontend/src/components/layout/AppSidebar.vue frontend/src/composables/useRoutePrefetch.ts frontend/src/api/client.ts
git commit -m "feat: mount image canvas in user and admin apps"
```

### Task 19: 实现管理员有序模型链编辑器

**Files:**
- Create: `frontend/src/api/admin/imageCanvas.ts`
- Create: `frontend/src/components/admin/image-canvas/ModelPolicyEditor.vue`
- Create: `frontend/src/components/admin/image-canvas/__tests__/ModelPolicyEditor.spec.ts`
- Modify: `frontend/src/views/admin/ImageCanvasAdminView.vue`

- [ ] **Step 1: 写排序、重复和 409 冲突失败测试**

```ts
it('moves a fallback and submits the complete ordered policy', async () => {
  const wrapper = mountPolicyEditor(fixturePolicy(['model-a', 'model-b', 'model-c']))
  await wrapper.get('[data-testid="move-up-model-c"]').trigger('click')
  await wrapper.get('[data-testid="save-policy"]').trigger('click')
  expect(adminImageCanvasAPI.updatePolicy).toHaveBeenCalledWith({
    version: 7,
    enabled: true,
    models: [
      { model: 'model-a', enabled: true, position: 0 },
      { model: 'model-c', enabled: true, position: 1 },
      { model: 'model-b', enabled: true, position: 2 },
    ],
  })
})
```

- [ ] **Step 2: 运行测试确认红灯**

Run: `cd frontend && pnpm test:run -- src/components/admin/image-canvas/__tests__/ModelPolicyEditor.spec.ts`

Expected: FAIL，editor 不存在。

- [ ] **Step 3: 实现编辑器**

使用 `vue-draggable-plus` 和键盘上移/下移按钮。第一个启用项标记主模型，其余标记兜底。模型从 admin API 的 schedulable catalog 选择；重复项、空启用链、未知 capability 不能保存。拖拽完成重新编号 position。完成后把 admin view 组装为“生成画布/模型配置”两个 tabs；feature flag=false 只禁用生成画布，配置 tab 始终可用。

- [ ] **Step 4: 实现版本冲突恢复和审计**

PUT 409 时保留本地草稿、重新 GET server policy，并展示“服务器版本已更新”对话框，动作仅为“放弃本地并重新加载”或“关闭后手动比较”，不自动覆盖。页面下方审计表展示 operator、old/new version、时间和结构化顺序差异。

- [ ] **Step 5: 运行测试和类型检查并提交**

Run: `cd frontend && pnpm test:run -- src/components/admin/image-canvas && pnpm typecheck`

Expected: PASS。

```bash
git add frontend/src/api/admin/imageCanvas.ts frontend/src/components/admin/image-canvas frontend/src/views/admin/ImageCanvasAdminView.vue
git commit -m "feat: add admin image model chain editor"
```

### Task 20: 完成 i18n、构建和 AGPL 发布阻断

**Files:**
- Modify: `frontend/src/i18n/locales/en.ts`
- Modify: `frontend/src/i18n/locales/zh.ts`
- Modify: `frontend/package.json`
- Modify: `frontend/vite.config.ts`
- Modify: `.github/workflows/backend-ci.yml`
- Modify: `.github/workflows/release.yml`
- Create: `frontend/infinite-canvas/scripts/check-license.mjs`

- [ ] **Step 1: 添加中英文 key 完整性测试**

在现有 locale parity 测试加入 `nav.imageGeneration`、`imageCanvas.*` 和 `admin.imageCanvas.*`，断言英文与中文 key 集合一致，不允许 fallback 到 key 字符串。

- [ ] **Step 2: 实现构建脚本和静态产物顺序**

Root build 顺序固定为 canvas build 后 Vue build。React/AntD 只由独立 canvas Vite 构建打包，Vue Vite 把 `public/infinite-canvas` 当作不透明静态目录复制，不能再次解析或合并这些模块。开发命令并行启动 canvas watch 和 Vue Vite；production 只读取已生成 manifest。

- [ ] **Step 3: 实现许可证检查脚本**

```js
import { existsSync, readFileSync } from 'node:fs'
const required = [
  'infinite-canvas/LICENSE',
  'infinite-canvas/LICENSE.upstream',
  'infinite-canvas/NOTICE',
  'public/infinite-canvas/manifest.json',
]
for (const file of required) {
  if (!existsSync(file)) throw new Error(`missing required canvas release file: ${file}`)
}
if (!process.env.VITE_CANVAS_SOURCE_URL?.startsWith('https://')) {
  throw new Error('VITE_CANVAS_SOURCE_URL must be an HTTPS corresponding-source URL')
}
if (!readFileSync('infinite-canvas/LICENSE', 'utf8').includes('GNU AFFERO GENERAL PUBLIC LICENSE')) {
  throw new Error('canvas LICENSE is not AGPL-3.0')
}
```

`backend-ci.yml` 使用 `VITE_CANVAS_SOURCE_URL=https://example.test/ci-corresponding-source` 验证构建。`release.yml` 的 build-frontend job 使用 repository variable `${{ vars.CANVAS_SOURCE_URL }}`；变量为空或不是 HTTPS 时许可证脚本令 release 失败。两个 workflow 都在打包后运行该脚本，并确认 backend dist 中包含 manifest、LICENSE、LICENSE.upstream 和 NOTICE。

- [ ] **Step 4: 运行前端完整验证**

Run: `cd frontend && VITE_CANVAS_SOURCE_URL=https://example.test/ci-corresponding-source pnpm lint:check && pnpm test:run && pnpm typecheck && pnpm build && node infinite-canvas/scripts/check-license.mjs`

Expected: 全部 PASS；`backend/internal/web/dist/infinite-canvas/manifest.json` 存在。

- [ ] **Step 5: 提交 i18n 和构建合规**

```bash
git add frontend/src/i18n/locales/en.ts frontend/src/i18n/locales/zh.ts frontend/package.json frontend/vite.config.ts frontend/infinite-canvas/scripts/check-license.mjs .github/workflows/backend-ci.yml .github/workflows/release.yml
git commit -m "build: enforce infinite canvas source compliance"
```

### Task 21: 增加端到端、视觉和回归验证

**Files:**
- Create: `frontend/e2e/image-canvas.spec.ts`
- Create: `frontend/e2e/fixtures/image-canvas-provider.ts`
- Create: `frontend/playwright.config.ts`
- Modify: `frontend/package.json`
- Modify: `frontend/pnpm-lock.yaml`

- [ ] **Step 1: 写用户主流程 E2E**

测试登录普通用户，创建 API Key，进入 `/images`，选择 Key 和 model-b，创建项目，生成图片，刷新并恢复项目，使用结果作为 edit 输入，再下载结果。假 Provider 记录模型调用并返回真实 16x16 PNG，断言浏览器中图片 `naturalWidth > 0`。

- [ ] **Step 2: 写模型回退 E2E**

管理员配置 `model-a -> model-b -> model-c`；用户选择 model-b。Provider 让 B 返回 500、A 返回 429、C 成功。断言调用顺序 `B,A,C`，UI 显示 successful model C，usage 只有一条且 model=C。

- [ ] **Step 3: 写不可回退和 partial E2E**

内容安全 400 后断言只调用 B。另一个任务由 B 返回一张 final 后 500，断言状态 partial、只调用 B、保留一张结果且不出现 A 的结果。

- [ ] **Step 4: 写权限与持久化 E2E**

第二用户尝试访问第一用户 project/asset/job 均返回 404；用户不能调用 admin policy API；管理员画布项目也只属于管理员本人。两个浏览器 context 同时更新同一 project，后提交者看到 conflict 且本地草稿仍在。

- [ ] **Step 5: 做桌面和移动视觉验证**

Run: `cd frontend && pnpm exec playwright test e2e/image-canvas.spec.ts --project=chromium`

Expected: PASS。保存 1440x900、1024x768、390x844 截图；断言 sidebar、toolbar、node、prompt panel bounding boxes 不相交；canvas 采样区域不是单色；缩放和拖拽后节点坐标变化。

- [ ] **Step 6: 运行完整回归并提交**

Run: `cd backend && go test ./... -count=1`

Run: `cd frontend && VITE_CANVAS_SOURCE_URL=https://example.test/ci-corresponding-source pnpm lint:check && pnpm test:run && pnpm typecheck && pnpm build && pnpm exec playwright test e2e/image-canvas.spec.ts --project=chromium`

Expected: 全部 PASS；现有 `/v1/images/generations` 和 `/v1/images/edits` 同步测试未变化。

```bash
git add frontend/e2e frontend/playwright.config.ts frontend/package.json frontend/pnpm-lock.yaml
git commit -m "test: cover infinite canvas image workflows"
```

### Task 22: 文档、冒烟脚本和蓝绿发布门禁

**Files:**
- Create: `docs/infinite-canvas-images.md`
- Create: `deploy/scripts/smoke-image-canvas.sh`
- Modify: `README.md`
- Modify: `deploy/blue-green/README.md`

- [ ] **Step 1: 编写管理员和用户文档**

文档包含：对象存储前置、功能开关、模型链配置、API Key 选择语义、可回退/不可回退错误、partial、费用预占和实际结算、项目恢复、AGPL 对应源码要求、备份和关闭功能步骤。不得在示例中放真实 key。

- [ ] **Step 2: 编写只读优先的冒烟脚本**

脚本参数为 base URL、测试 JWT、测试 API Key ID、project ID 和 prompt。先 GET health/config，再创建 `n=1` 小额任务、轮询终态、下载并用 `file` 验证 image MIME。默认不修改模型策略；只有显式 `--allow-billable-generation` 才创建付费任务。

- [ ] **Step 3: 更新蓝绿 runbook**

发布顺序写为：备份、非活动环境迁移、功能关闭启动、配置策略、小额 smoke、检查 source URL/license、打开 candidate flag、获得独立维护窗口确认、HAProxy 切流、观察 15 分钟。回滚先关 flag，再等待/取消新任务，切回旧版本，不执行 down migration、不删对象。

- [ ] **Step 4: 运行文档命令和最终状态检查**

Run:

```bash
bash -n deploy/scripts/smoke-image-canvas.sh
git diff --check
git status --short --branch
git log --oneline --decorate -25
```

Expected: shell 语法通过；无未预期文件；每个 Task 有独立提交。

- [ ] **Step 5: 提交文档**

```bash
git add -f docs/infinite-canvas-images.md
git add deploy/scripts/smoke-image-canvas.sh deploy/blue-green/README.md README.md
git commit -m "docs: add infinite canvas operations guide"
```

## 完成定义

- 后端单元、handler、repository integration 和完整 `go test ./...` 通过。
- 前端 React/Vue 单元测试、lint、typecheck、双 Vite build 和 Playwright 通过。
- 用户左侧菜单和画布任务由默认关闭的 feature flag 控制；管理员配置入口始终可访问，管理员画布任务同样受开关控制。
- 用户主动选择本人 API Key，模型链按 Key group/capability 过滤，Key 明文不进入 React。
- 用户选择模型优先，零最终图片的服务错误按管理员顺序回退；业务错误不回退；partial 不混用模型。
- 只按实际成功模型和实际结果结算一次，失败/取消释放预占。
- 项目、任务和资产可跨设备恢复，所有权测试覆盖 project/asset/job。
- React 不读取 Sub2API token，所有 API 经 Vue host bridge；SSE token 不进入 URL。
- production 产物包含 AGPL、NOTICE、上游基线和有效对应源码 URL。
- 蓝绿 candidate smoke 完成，但没有用户独立维护窗口确认时不得执行生产 HAProxy 切流。
