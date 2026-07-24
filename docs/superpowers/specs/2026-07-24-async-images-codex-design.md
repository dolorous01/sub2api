# Sub2API 异步图片任务、连续场景与 Codex Plugin 设计

日期：2026-07-24

状态：已批准，待实施计划
范围：OpenAI 兼容图片接口、异步图片任务、连续场景编排、Codex Plugin

## 1. 背景

Sub2API 已支持 OpenAI 图片生成和编辑的主要同步路径：

- `POST /v1/images/generations`
- `POST /v1/images/edits`
- JSON 和 multipart 请求
- 文生图、单图或多图编辑、mask 局部重绘
- `gpt-image-*` 模型路由
- 同步 JSON 与 SSE 流式响应
- 图片账号调度、故障转移、并发控制、用量统计和计费

当前问题不是 `gpt-image-2` 无法生成图片，而是批量图片请求耗时可能超过 Cloudflare 等前置代理的同步等待时间。把 `n=4` 拆成四个互不相关的 `n=1` 请求虽然能缩短单次等待，却会削弱角色、场景和风格的连续性。

Codex 当前公开、稳定的扩展方式是 Skill/Plugin。设计不依赖未公开的顶级 `/image` 命令，而是提供自然语言触发和 `$image` 显式调用。

## 2. 目标

1. 保持现有同步图片接口兼容，不改变未选择异步模式的客户端行为。
2. 为生成和编辑接口增加持久化异步任务，使客户端无需维持长连接。
3. `batch` 模式必须把 `n=4` 作为一次上游请求发送，不能拆成四个独立文生图请求。
4. 增加 `continuity` 模式，使用固定参考图和上一帧进行连续编辑，提高四幕场景的一致性。
5. 完整覆盖文生图、图生图、普通编辑、mask 局部重绘、多图参考或合成。
6. 输入图和输出图存入私有对象存储，数据库只保存任务和资源元数据。
7. 保持鉴权、分组权限、账号调度、故障转移、并发、审核和计费语义一致。
8. 提供可安装的 Codex Plugin，让用户通过自然语言或 `$image` 使用上述能力。

## 3. 非目标

- 不实现或代理旧版 `/v1/images/variations`。
- 不承诺 `n=4` 能提供视频模型级别的逐像素角色一致性。
- 不整体嵌入 LiteLLM、New API、ComfyUI 或其他完整网关。
- 第一版不增加新的 GPU 推理服务，也不把 StoryDiffusion、InstantID 等扩散模型作为默认依赖。
- 不修改 Codex 客户端或维护 Codex 分支来注册顶级 `/image` 命令。
- 不在数据库中保存图片 Base64、大型 multipart 原文或明文 API Key。

## 4. 已评估方案

### 4.1 仅使用 `n=4, stream=true`

优点是改动小，且能通过 SSE 心跳缓解空闲超时。缺点是客户端仍需维持长连接；如果上游迟迟不返回响应头、部署平台有总请求时限或中间代理缓冲 SSE，仍可能失败。

结论：保留为兼容路径，但不作为 Codex Plugin 的默认可靠路径。

### 4.2 把 `n=4` 拆成多个 `n=1`

优点是请求短、失败隔离简单。缺点是四次文生图之间没有共享图片上下文，不能满足连续场景要求。

结论：不用于 `batch` 模式。

### 4.3 异步 Job + 对象存储 + Codex Plugin

创建请求立即返回 Job ID，后台执行原始图片调用，客户端短轮询状态并逐张下载。该模式已被 fal Queue、Replicate Predictions 等长耗时媒体 API 广泛采用。

结论：采用。

### 4.4 引入外部项目

- LiteLLM 和 New API 可作为图片参数适配和响应转换的参考，但仍会与 Sub2API 的鉴权、账号调度和计费大量重叠。
- River 和 Asynq 能提供任务队列，但 Sub2API 已具备 PostgreSQL 任务认领、后台 worker、Redis 和 S3/R2 基础设施。
- ComfyUI、StoryDiffusion、InstantID 和 PhotoMaker 可作为未来的可选一致性图片渠道，但不直接增强 `gpt-image-2`。

结论：第一版复用 Sub2API 现有架构，不引入新的任务框架。

## 5. 总体架构

实现分为五个边界清晰的组件。

### 5.1 Image Request Parser

继续使用现有 `OpenAIImagesRequest` 解析生成和编辑请求，并扩展为可序列化的规范化请求。职责包括：

- 校验 endpoint、模型和参数类型；
- 解析 JSON 或 multipart；
- 区分普通输入图、多个参考图和 mask；
- 计算请求摘要；
- 生成内容审核输入；
- 明确请求所需的上游图片能力。

解析器不得静默丢弃客户端显式传入的高级参数。

### 5.2 Image Executor

把当前直接写入 `gin.Context` 的上游执行逻辑拆成可复用执行器：

```text
ImageExecutor.Execute(ctx, normalizedRequest, executionContext, resultSink)
```

执行器负责：

- 账号选择和故障转移；
- API Key 与 OAuth 上游适配；
- 上游请求、SSE 解析和错误映射；
- usage、最终图片和响应元数据提取；
- 调用统一结果 sink。

同步 HTTP 使用 `HTTPImageResultSink`；异步任务使用 `JobImageResultSink`。两条路径共享同一个执行器，避免维护两套图片协议实现。

### 5.3 Image Job Service

负责创建、查询、取消、执行、结算和清理任务。数据库任务是事实来源，worker 通过 PostgreSQL 原子认领任务。

### 5.4 Image Object Store

从现有备份 S3 实现中提取通用对象存储接口，但使用独立图片配置和对象前缀。生产支持 S3、R2 和兼容服务；开发和单实例测试可使用本地目录。

### 5.5 Codex Plugin

Plugin 包含 `$image` Skill、标准库辅助程序、参数说明和安装文档。它只负责调用 Sub2API、轮询任务并保存结果，不复制服务器端调度或计费规则。

## 6. API 设计

### 6.1 标准端点异步扩展

以下端点在请求包含 `Prefer: respond-async` 时创建异步任务：

```http
POST /v1/images/generations
POST /v1/images/edits
```

请求体、Content-Type 和已有参数保持不变。未携带该 Header 时继续执行现有同步或 SSE 路径。

创建成功返回 HTTP 202：

```json
{
  "id": "imgjob_01J...",
  "object": "image.job",
  "status": "queued",
  "operation": "generation",
  "status_url": "/v1/images/jobs/imgjob_01J...",
  "created_at": 1784880000,
  "expires_at": 1784966400
}
```

响应包含：

```http
Preference-Applied: respond-async
Location: /v1/images/jobs/imgjob_01J...
```

### 6.2 连续场景端点

```http
POST /v1/images/sequences
```

该端点固定创建异步任务。支持 JSON；需要本地参考图时使用 multipart。第一版支持 2 至 4 帧，默认 4 帧。

规范化字段：

```json
{
  "model": "gpt-image-2",
  "prompt": "统一角色、环境和视觉规则",
  "scenes": [
    "第一幕描述",
    "第二幕描述",
    "第三幕描述",
    "第四幕描述"
  ],
  "size": "1024x1024",
  "quality": "high",
  "output_format": "png",
  "input_fidelity": "high"
}
```

multipart 可重复上传 `image` 或 `image[]` 作为固定参考图。服务端不调用文本模型生成 `scenes`；Codex Skill 负责把用户叙述整理为明确分镜，并把最终分镜随请求提交。

### 6.3 查询、下载和取消

```http
GET    /v1/images/jobs/{job_id}
GET    /v1/images/jobs/{job_id}/results/{index}
DELETE /v1/images/jobs/{job_id}
```

查询结果示例：

```json
{
  "id": "imgjob_01J...",
  "object": "image.job",
  "status": "running",
  "operation": "sequence",
  "mode": "continuity",
  "model": "gpt-image-2",
  "requested_count": 4,
  "completed_count": 2,
  "data": [
    {
      "index": 0,
      "status": "completed",
      "url": "/v1/images/jobs/imgjob_01J.../results/0",
      "mime_type": "image/png",
      "size": "1024x1024"
    },
    {
      "index": 1,
      "status": "completed",
      "url": "/v1/images/jobs/imgjob_01J.../results/1",
      "mime_type": "image/png",
      "size": "1024x1024"
    }
  ],
  "created_at": 1784880000,
  "started_at": 1784880001,
  "expires_at": 1784966400
}
```

结果端点默认返回原始图片字节和正确的 `Content-Type`。任务查询不内联 Base64。同步端点仍按原协议支持 `b64_json` 或 URL。

### 6.4 Job 状态

- `queued`：已持久化，等待 worker；
- `running`：worker 已认领并开始执行；
- `completed`：所有结果和计费均已完成；
- `partial`：部分最终图片可用，后续步骤失败；
- `failed`：无最终图片，失败原因明确；
- `indeterminate`：上游可能已经执行，但进程在确认结果前中断；
- `canceled`：在可安全取消阶段被取消；
- `expired`：结果已超过保留期。

终态不可被普通 worker 重写。

## 7. 图片能力与参数策略

### 7.1 文生图

`/v1/images/generations` 支持模型、prompt、n、size、quality、background、output_format、output_compression、moderation、partial_images、stream 和 response_format 等已解析字段。

### 7.2 编辑和图生图

`/v1/images/edits` 支持：

- 单张输入图；
- 多张 `image` 或 `image[]` 输入；
- URL 型图片输入；
- prompt；
- input_fidelity；
- 与生成端点共享的输出参数。

### 7.3 mask 局部重绘

mask 可作为 multipart 文件或受支持的 URL 输入。服务端校验 mask 是图片、非空且未超过单文件限制。模型关于透明区域方向、尺寸或格式的约束由适配层校验并返回明确错误。

### 7.4 多图参考和合成

第一版 Sub2API 默认最多接受 4 张普通输入图，管理员可在 1 至 16 之间配置。实际可用数量还受目标模型和上游适配器限制。超限必须在创建任务前返回 400，不能静默截断。

### 7.5 高级参数兼容

- API Key 上游尽可能原样转发原生 Images API 字段。
- OAuth/Responses 桥接只发送其上游工具明确支持的字段。
- 账号能力必须区分“完整原生图片能力”和“桥接图片能力”。
- 调度器优先选择支持请求全部显式字段的账号。
- 如果没有兼容账号，返回包含具体字段的 `unsupported_parameter` 或 `no_compatible_account`，不得静默忽略 `input_fidelity` 等参数。
- `background=transparent` 作为模型相关能力透传；上游不支持时返回原始兼容错误，不在 Sub2API 中伪造透明背景。

## 8. 批量与连续场景算法

### 8.1 Batch 模式

`batch` 任务保留原始 `n`。`n=4` 必须产生一次上游调用：

```text
one job -> one account selection/failover flow -> one upstream request with n=4
```

故障转移可以在尚未产生任何最终图片时切换账号，但不能把请求降级为四个 `n=1`。如果上游在 SSE 中逐张返回最终图，sink 可逐张持久化；如果上游只在末尾返回，进度会从 0 直接变为 4。

### 8.2 Continuity 模式

没有用户参考图时：

1. 用总提示词和第一幕执行一次 generation，得到 Frame 1；
2. 把 Frame 1 设为固定 anchor；
3. Frame 2 使用 anchor 及 Frame 1 作为编辑输入；
4. Frame 3 使用 anchor 及 Frame 2；
5. Frame 4 使用 anchor 及 Frame 3。

有用户参考图时，所有用户参考图都是固定 anchor；第一幕也通过 edit 生成。每一步串行执行，完成后立即持久化最终图并更新进度。

固定参考图、上一帧和用户参考图的总数不得超过适配器上限。超限时创建请求失败，不能自动丢弃参考图。

该算法提高角色和环境连续性，但文档和 Plugin 必须说明它不构成绝对一致性保证，且通常比单次 `n=4` 更慢、更贵。

## 9. 数据模型与持久化

### 9.1 image_jobs

核心字段：

- public ID；
- user ID、API key ID、group ID；
- endpoint、operation、mode；
- requested model、mapped model；
- status、requested count、completed count；
- 规范化请求 JSON，其中图片仅保存对象键；
- 请求摘要和 Idempotency-Key；
- 计费预留 ID、最终 usage、结算状态；
- 当前 attempt ID、worker ID、heartbeat；
- sanitized error type、code、message；
- created、started、finished、expires、updated 时间。

在 `(api_key_id, idempotency_key)` 上建立条件唯一约束。相同 Key 但不同请求摘要返回 409。

### 9.2 image_job_results

每张最终图片一条记录：

- job ID 和 index；
- object key、mime type、byte size；
- width、height、API size tier；
- revised prompt；
- upstream output ID；
- status 和 timestamps。

### 9.3 image_job_inputs

保存输入图和 mask 的对象键、顺序、类型、mime type、大小和摘要，支持安全清理及审计。

## 10. 对象存储

生产环境使用独立图片存储配置，不与数据库备份的保留策略混用。可以复用 AWS S3 客户端实现，但使用独立 bucket 或 prefix。

建议键格式：

```text
image-jobs/{api_key_id}/{job_id}/inputs/{index}-{digest}.{ext}
image-jobs/{api_key_id}/{job_id}/mask/{digest}.png
image-jobs/{api_key_id}/{job_id}/results/{index}.{ext}
```

安全要求：

- bucket 默认私有；
- 下载通过已鉴权的 Sub2API 端点或短时签名 URL；
- 日志不记录签名 URL、Base64 或完整 prompt；
- 默认结果 TTL 为 24 小时，可配置；
- 清理 worker 删除对象后把 Job 标记为 expired；
- 本地存储只用于开发或配置了共享持久卷的单实例部署；
- 异步功能启用但没有可用存储时，创建任务返回 503。

如果上游成功但结果写入对象存储失败，用户不得为不可获取的图片付费；任务进入 failed 或 partial，运营侧记录上游成本损失。

## 11. Worker、并发和恢复

Worker 复用现有 PostgreSQL `FOR UPDATE SKIP LOCKED` 任务模式。图片 Job 使用独立并发配置，并继续受现有用户、账号和图片并发限制约束。

默认配置：

- worker concurrency：2；
- task timeout：30 分钟；
- max outputs per batch：4；
- max input images：4；
- result TTL：24 小时；
- polling recommendation：1 秒起步，退避至 5 秒。

认领后定期更新 heartbeat。恢复规则：

- 在上游调用前崩溃：安全地重新入队；
- 明确的限流、账号不可用或可故障转移错误：在同一 attempt 内使用现有策略；
- 上游调用开始后进程失联：标记 indeterminate，不自动重新调用；
- 任务取消发生在 queued：直接 canceled 并释放预留；
- running 取消为尽力而为；如果上游已经产生可计费结果，按实际结果结算。

## 12. 鉴权、审核和权限

- 创建任务沿用现有 Bearer API Key 鉴权。
- 创建时校验分组允许图片生成。
- 查询、下载和取消必须匹配创建任务的 API key 或具备相应管理员权限。
- 在上传输入写入长期对象键前完成文件类型和大小校验；内容审核使用现有图片请求审核逻辑。
- Job 不保存明文 API Key。执行时根据持久化 API key ID 和用户归属加载仍然有效的授权状态。
- API key 在排队期间被禁用、过期或换组时，worker 开始前重新校验并安全失败。

## 13. 计费和幂等

### 13.1 创建阶段

- 执行现有余额和订阅资格检查；
- 根据模型、尺寸、质量和最大输出数计算预估上限；
- 创建余额或配额预留；
- 原子写入 Job、预留和 Idempotency-Key。

如果某种 token 计费模式无法精确预估，使用配置的最大图片费用作为预留上限。无法得出安全预留值时拒绝异步任务，不能允许无上限排队。

### 13.2 完成阶段

- 使用上游 usage 和实际最终图片数执行一次最终结算；
- completed 按全部实际结果结算；
- partial 按已成功且可下载的结果结算；
- 无结果失败或 queued 取消释放全部预留；
- 差额退回；
- 结算操作以 job ID 为幂等键。

### 13.3 重复请求

客户端应发送 `Idempotency-Key`。同一 API key、同一 Key 和相同请求摘要返回原 Job；同一 Key 但不同请求返回 409。Codex Plugin 自动生成并持久化本次调用的 Key，网络重试时复用。

## 14. 错误语义

Job 错误包含稳定字段：

```json
{
  "error": {
    "type": "invalid_request_error",
    "code": "unsupported_parameter",
    "message": "No compatible account supports input_fidelity=high",
    "retryable": false
  }
}
```

用户错误、内容策略错误和不支持参数不自动重试。限流和暂时性上游错误沿用现有同账号重试和账号故障转移。任何对外错误都经过现有敏感信息清理。

`partial` 返回已完成结果和失败信息。`indeterminate` 明确提示上游可能已经执行，用户需决定是否创建新任务；系统不会把它伪装成普通 failed。

## 15. Codex Plugin 设计

Plugin 名称：`sub2api-image`。包含：

```text
plugins/sub2api-image/
  .codex-plugin/plugin.json
  skills/image/SKILL.md
  skills/image/scripts/sub2api_image.py
  skills/image/references/api.md
  README.md
```

Skill 可被自然语言触发，也可显式使用 `$image`。

### 15.1 用户操作

- `$image generate`：文生图；默认 4 张 batch；
- `$image sequence`：连续 4 幕；
- `$image edit`：普通图片编辑；
- `$image inpaint`：原图加 mask；
- `$image compose`：多图参考或合成；
- `$image status <job_id>`：恢复查询已有任务。

自然语言示例：

- “生成四张同一主题的候选海报”；
- “用同一个角色生成连续四幕”；
- “把这张图片的背景改成雪山”；
- “只修改 mask 的透明区域”；
- “参考这三张图合成一张产品主图”。

### 15.2 配置

辅助程序读取：

- `SUB2API_BASE_URL`；
- `SUB2API_API_KEY`；
- 可选 `SUB2API_IMAGE_OUTPUT_DIR`。

密钥不写入插件文件、项目文件、命令输出或日志。默认输出目录为当前项目的 `generated-images/{job_id}/`。

第一版辅助程序仅使用 Python 标准库，避免 SDK 版本耦合。文档明确 Python 版本要求并给出 curl 手工调用回退。未来可以用 Go 静态二进制替换，而不改变服务器 API 或 Skill 语义。

### 15.3 行为

- generate、edit、inpaint 和 compose 默认发送 `Prefer: respond-async`；
- sequence 调用专用异步端点；
- 轮询使用退避并显示 `completed_count/requested_count`；
- 每张结果独立下载并使用原始扩展名；
- 会话中断时打印 Job ID；
- 再次调用 status 可继续下载未保存结果；
- 不在客户端重写 `n=4` 为多个生成请求。

## 16. 测试设计

### 16.1 解析和协议测试

- 生成 JSON 参数完整解析；
- edits multipart 单图、多图和 mask；
- URL 型多图和 mask；
- 高级参数不被静默丢弃；
- 输入图数量、文件大小和总请求限制；
- 未携带 Prefer 时同步行为不变；
- 携带 Prefer 时返回 202、Location 和 Preference-Applied。

### 16.2 Job 服务测试

- Job 创建、归属和状态转换；
- 相同 Idempotency-Key 返回同一 Job；
- Key 冲突返回 409；
- API key 所有权隔离；
- queued 和 running 取消；
- TTL 到期及对象清理；
- 对象存储失败；
- stale preflight 重新入队和 stale upstream indeterminate。

### 16.3 Worker 测试

- 两个 worker 不能认领同一 Job；
- batch `n=4` 只产生一次上游调用；
- SSE 和非 SSE 上游都能持久化四个结果；
- generation、edit、mask 和多图请求保持原始语义；
- continuity 无参考图时一次 generation 加三次 edit；
- continuity 有参考图时四次 edit；
- 每次 continuity edit 都包含固定 anchor 和上一帧；
- 部分完成后失败进入 partial；
- 现有同账号重试和账号切换仍生效。

### 16.4 计费测试

- 创建时预留；
- completed、partial、failed、canceled 的结算或释放；
- 结算幂等；
- 重复 worker 完成事件不重复扣费；
- 不可下载的存储失败结果不向用户计费。

### 16.5 Plugin 测试

对模拟 Sub2API 服务验证：

- generate、sequence、edit、inpaint、compose；
- multipart 文件和重复 image 字段；
- Job 轮询、退避和恢复；
- 四个结果文件名、内容和 MIME 扩展名；
- partial、failed、indeterminate 和过期错误展示；
- 日志和异常中不出现 API Key。

### 16.6 回归和验收

完成标准：

1. 异步创建在不等待图片模型的情况下返回；
2. batch `n=4` 在测试中只有一次上游请求并得到四个文件；
3. Cloudflare 只处理短创建、查询和下载请求；
4. 文生图、编辑、mask、多图参考都能异步完成；
5. continuity 严格复用固定参考和上一帧；
6. 任务和结果跨进程重启可恢复；
7. 所有权、幂等、计费和清理测试通过；
8. 现有 OpenAI 图片同步、流式、故障转移和计费测试通过；
9. Plugin 的模拟端到端测试通过；
10. 现有未关联的工作树改动不被覆盖或纳入本功能提交。

## 17. 配置和上线

新功能默认关闭。管理员需配置对象存储后开启异步图片 Job。建议配置分组：

```yaml
gateway:
  image_jobs:
    enabled: false
    worker_concurrency: 2
    task_timeout_seconds: 1800
    max_outputs_per_job: 4
    max_input_images: 4
    result_ttl_seconds: 86400
    storage:
      driver: s3
      bucket: ""
      prefix: image-jobs/
```

上线顺序：

1. 数据库迁移和对象存储抽象；
2. Job API、worker、计费和清理；
3. batch 异步路径；
4. edits、mask 和多图异步路径；
5. continuity 编排；
6. Codex Plugin；
7. 管理配置、指标、文档和灰度开启。

关键指标：queued 数、排队时间、执行时间、completed/partial/failed/indeterminate 比例、单 Job 图片数、对象存储错误、预留和结算差异、账号切换次数。

## 18. 已确定的设计决策

- 采用单次 `n=4` 的 batch，不拆请求；
- 增加基于 edits 的 continuity；
- 采用持久化异步 Job 和对象存储；
- 复用 Sub2API 自身 PostgreSQL worker 模式；
- 同步接口完全兼容；
- 使用 `Prefer: respond-async` 扩展标准端点；
- 使用 `$image` 和自然语言，不维护 Codex 客户端分支；
- 不静默忽略不支持的高级图片参数；
- 不自动重跑上游状态不确定的任务；
- 第一版结果保留 24 小时，默认最多 4 个输出和 4 张输入参考图。
