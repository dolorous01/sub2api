# HAProxy Blue-Green Deployment Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 为当前单机 Sub2API 生产环境增加外部 CI 镜像构建、HAProxy 蓝绿切换、长连接排空、自动回退和可重复回滚能力。

**Architecture:** 保留 Cloudflare Tunnel、Python `:8080` 路径代理、PostgreSQL 和 Redis；HAProxy 接管本机 `127.0.0.1:18080`，Blue/Green 分别监听 `127.0.0.1:18081/18082`。GitHub Actions 构建以完整 commit SHA 标记的 GHCR 镜像，生产发布脚本先启动 backup、验证后平滑重载 HAProxy 交换 primary/backup，等待旧连接归零再停止旧实例。

**Tech Stack:** Bash、Docker Compose v2、HAProxy master-worker/systemd、GitHub Actions、GHCR、PostgreSQL 16、Redis 7、`curl`、`jq`、`socat`、`shellcheck`。

---

## 实施边界

- 当前 `/home/ubuntu/sub2api` 工作树包含用户未提交的后端和前端修改。开发阶段必须先使用 `superpowers:using-git-worktrees` 创建独立 worktree，不得在原工作树执行格式化、生成代码、暂存或合并。
- 版本控制内的实现全部放在 `deploy/blue-green/` 和 `.github/workflows/`。生产密钥继续只存在 `/home/ubuntu/ResearchWang13/.env`，测试、日志和提交中不得打印其值。
- Tasks 1-9 只开发和验证工具，不改变生产流量。Task 10 只安装并预演。Task 11 是首次生产切换，开始前必须再次获得用户对维护窗口的明确确认。
- 不执行 `docker compose down`、`docker compose down -v`，不重启 PostgreSQL、Redis、Python 路径代理或 Cloudflare Tunnel。
- 生产镜像只接受 `ghcr.io/...:sha-<40 位提交哈希>`。首次引导允许沿用当前已知正常镜像 `ghcr.io/wei-shaw/sub2api:0.1.132`。
- 数据库迁移必须符合 expand-migrate-contract。检测到迁移文件变化时，发布前必须创建并验证 PostgreSQL 备份。

## 文件职责映射

### 新文件

- `.github/workflows/production-image.yml`：手动选择 Git ref，运行验证并推送完整 SHA 标签的 amd64 GHCR 镜像。
- `deploy/blue-green/compose.yml`：Blue/Green 两个 host-network 应用服务；不管理 PostgreSQL、Redis 或 Cloudflare。
- `deploy/blue-green/versions.env.example`：非敏感状态文件格式示例。
- `deploy/blue-green/haproxy.cfg.tmpl`：HAProxy localhost frontend、primary/backup 和长连接超时模板。
- `deploy/blue-green/render-haproxy.sh`：按活动颜色渲染完整 HAProxy 配置。
- `deploy/blue-green/lib.sh`：状态解析、镜像校验、资源门禁和原子状态写入。
- `deploy/blue-green/deploy.sh`：发布编排、健康检查、平滑切换、观察、排空和失败恢复。
- `deploy/blue-green/rollback.sh`：切回状态文件中保留的另一颜色和镜像。
- `deploy/blue-green/status.sh`：只读显示 HAProxy、容器、端口、资源和当前状态。
- `deploy/blue-green/README.md`：安装、首次切换、正常发布、回滚和故障处理手册。
- `deploy/blue-green/tests/*.sh`：纯 Bash 单元、配置和 dry-run 测试。

### 修改文件

- `.github/workflows/backend-ci.yml`：增加 shellcheck、Compose 和 HAProxy 配置测试 job。
- `/home/ubuntu/ResearchWang13/docker-compose.yml`：首次切换前把旧 `sub2api` 放入 `legacy` profile，并移除 `cloudflared -> sub2api` 依赖。
- `/home/ubuntu/ResearchWang13/docs/ARCHITECTURE.md`：记录新链路。
- `/home/ubuntu/ResearchWang13/docs/CHANGE_PROCESS.md`：改为调用蓝绿发布/回滚脚本。
- `/home/ubuntu/ResearchWang13/docs/CHANGELOG.md`：记录安装、验证、窗口和回滚路径。
- `/etc/haproxy/haproxy.cfg`：由已验证模板生成；写入前创建时间戳备份。

## Task 1: 创建独立 worktree 并记录基线

**Files:**
- Read: `/home/ubuntu/sub2api`
- Create worktree: `/home/ubuntu/sub2api-haproxy-blue-green`

- [ ] **Step 1: 调用 worktree 技能并保存原工作树状态**

```bash
git -C /home/ubuntu/sub2api status --short --branch
git -C /home/ubuntu/sub2api diff --stat
```

Expected: 输出当前用户修改；不得清理、暂存或恢复这些文件。

- [ ] **Step 2: 从包含本计划的 HEAD 创建功能分支 worktree**

```bash
BG_WORKTREE=/home/ubuntu/sub2api-haproxy-blue-green
git -C /home/ubuntu/sub2api worktree add "$BG_WORKTREE" -b ops/haproxy-blue-green HEAD
git -C "$BG_WORKTREE" status --short --branch
```

Expected: 新 worktree 位于 `ops/haproxy-blue-green`，工作区无修改。

- [ ] **Step 3: 运行现有基线测试**

```bash
cd /home/ubuntu/sub2api-haproxy-blue-green/backend
make test-unit
cd /home/ubuntu/sub2api-haproxy-blue-green
make test-frontend
```

Expected: 两个命令退出码均为 0。若基线失败，保存完整输出并暂停实现。

## Task 2: 定义 Blue/Green Compose 拓扑

**Files:**
- Create: `deploy/blue-green/compose.yml`
- Create: `deploy/blue-green/versions.env.example`
- Create: `deploy/blue-green/tests/test_helper.sh`
- Create: `deploy/blue-green/tests/test_compose.sh`
- Create: `deploy/blue-green/tests/run.sh`

- [ ] **Step 1: 写 Compose 失败测试**

`test_helper.sh`：

```bash
#!/usr/bin/env bash
set -euo pipefail
fail() { printf 'FAIL: %s\n' "$*" >&2; exit 1; }
assert_eq() { [[ "$1" == "$2" ]] || fail "expected '$2', got '$1'"; }
assert_contains() { [[ "$1" == *"$2"* ]] || fail "missing '$2'"; }
```

`test_compose.sh` 创建临时非敏感 env 并断言配置：

```bash
TEST_DIR="$(mktemp -d)"
trap 'rm -rf "$TEST_DIR"' EXIT
mkdir -p "$TEST_DIR/data"
printf 'AUTO_SETUP=true\n' > "$TEST_DIR/runtime.env"
BLUE_IMAGE="ghcr.io/example/sub2api:sha-$(printf '1%.0s' {1..40})" \
GREEN_IMAGE="ghcr.io/example/sub2api:sha-$(printf '2%.0s' {1..40})" \
DEPLOY_ENV_FILE="$TEST_DIR/runtime.env" DEPLOY_DATA_PATH="$TEST_DIR/data" \
docker compose -f "$ROOT_DIR/compose.yml" config --format json > "$TEST_DIR/config.json"
assert_eq "$(jq -r '.services.blue.network_mode' "$TEST_DIR/config.json")" host
assert_eq "$(jq -r '.services.green.network_mode' "$TEST_DIR/config.json")" host
assert_eq "$(jq -r '.services.blue.environment.SERVER_HOST' "$TEST_DIR/config.json")" 127.0.0.1
assert_eq "$(jq -r '.services.blue.environment.SERVER_PORT' "$TEST_DIR/config.json")" 18081
assert_eq "$(jq -r '.services.green.environment.SERVER_PORT' "$TEST_DIR/config.json")" 18082
assert_eq "$(jq -r '.services.blue.environment.LOG_OUTPUT_TO_FILE' "$TEST_DIR/config.json")" false
```

- [ ] **Step 2: 运行测试并确认红灯**

Run: `bash deploy/blue-green/tests/test_compose.sh`

Expected: FAIL，`compose.yml` 不存在。

- [ ] **Step 3: 实现独立 Compose 文件**

```yaml
name: sub2api-bg
x-app-env: &app-env
  AUTO_SETUP: "true"
  SERVER_HOST: "127.0.0.1"
  SERVER_MODE: "release"
  LOG_OUTPUT_TO_STDOUT: "true"
  LOG_OUTPUT_TO_FILE: "false"
x-app: &app
  network_mode: host
  restart: unless-stopped
  env_file:
    - ${DEPLOY_ENV_FILE:?DEPLOY_ENV_FILE is required}
  volumes:
    - ${DEPLOY_DATA_PATH:?DEPLOY_DATA_PATH is required}:/app/data
  ulimits:
    nofile: {soft: 100000, hard: 100000}
  logging:
    driver: json-file
    options: {max-size: 20m, max-file: "5"}
  stop_grace_period: 15s
services:
  blue:
    <<: *app
    image: ${BLUE_IMAGE:?BLUE_IMAGE is required}
    container_name: sub2api_blue
    environment:
      <<: *app-env
      SERVER_PORT: "18081"
    healthcheck:
      test: ["CMD", "wget", "-q", "-T", "5", "-O", "/dev/null", "http://localhost:18081/health"]
      interval: 10s
      timeout: 5s
      retries: 3
      start_period: 40s
  green:
    <<: *app
    image: ${GREEN_IMAGE:?GREEN_IMAGE is required}
    container_name: sub2api_green
    environment:
      <<: *app-env
      SERVER_PORT: "18082"
    healthcheck:
      test: ["CMD", "wget", "-q", "-T", "5", "-O", "/dev/null", "http://localhost:18082/health"]
      interval: 10s
      timeout: 5s
      retries: 3
      start_period: 40s
```

`versions.env.example`：

```dotenv
ACTIVE_COLOR=blue
BLUE_IMAGE=ghcr.io/example/sub2api:sha-1111111111111111111111111111111111111111
GREEN_IMAGE=ghcr.io/example/sub2api:sha-2222222222222222222222222222222222222222
DEPLOY_ENV_FILE=/home/ubuntu/ResearchWang13/.env
DEPLOY_DATA_PATH=/home/ubuntu/ResearchWang13/data
```

- [ ] **Step 4: 增加测试入口、运行绿灯并提交**

```bash
#!/usr/bin/env bash
set -euo pipefail
TEST_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
for test_file in "$TEST_DIR"/test_*.sh; do bash "$test_file"; done
```

Run and commit:

```bash
bash deploy/blue-green/tests/run.sh
chmod +x deploy/blue-green/tests/run.sh deploy/blue-green/tests/test_helper.sh deploy/blue-green/tests/test_compose.sh
git add deploy/blue-green/compose.yml deploy/blue-green/versions.env.example deploy/blue-green/tests
git commit -m "ops: add blue-green compose topology"
```

Expected: PASS，且只提交本任务文件。

## Task 3: 实现 HAProxy 配置渲染

**Files:**
- Create: `deploy/blue-green/haproxy.cfg.tmpl`
- Create: `deploy/blue-green/render-haproxy.sh`
- Create: `deploy/blue-green/tests/test_render_haproxy.sh`

- [ ] **Step 1: 写角色和语法失败测试**

```bash
bash "$ROOT_DIR/render-haproxy.sh" blue "$TEST_DIR/blue.cfg"
assert_contains "$(grep 'server green' "$TEST_DIR/blue.cfg")" backup
bash "$ROOT_DIR/render-haproxy.sh" green "$TEST_DIR/green.cfg"
assert_contains "$(grep 'server blue' "$TEST_DIR/green.cfg")" backup
if grep 'server green' "$TEST_DIR/green.cfg" | grep -q backup; then fail 'green must be primary'; fi
haproxy -c -f "$TEST_DIR/blue.cfg"
haproxy -c -f "$TEST_DIR/green.cfg"
```

- [ ] **Step 2: 运行测试并确认红灯**

Run: `bash deploy/blue-green/tests/test_render_haproxy.sh`

Expected: FAIL，`render-haproxy.sh` 不存在。

- [ ] **Step 3: 创建 HAProxy 模板**

```haproxy
global
    log /dev/log local0
    log /dev/log local1 notice
    user haproxy
    group haproxy
    daemon
    stats socket /run/haproxy/admin.sock mode 660 level admin expose-fd listeners
defaults
    log global
    mode http
    option httplog
    option dontlognull
    option http-keep-alive
    timeout connect 5s
    timeout http-request 30s
    timeout http-keep-alive 2m
    timeout client 2h
    timeout server 2h
    timeout tunnel 2h
frontend sub2api_frontend
    bind 127.0.0.1:18080
    default_backend sub2api_backend
backend sub2api_backend
    option httpchk GET /health
    http-check expect status 200
    server blue 127.0.0.1:18081 check inter 2s fall 3 rise 2 @@BLUE_ROLE@@
    server green 127.0.0.1:18082 check inter 2s fall 3 rise 2 @@GREEN_ROLE@@
```

- [ ] **Step 4: 实现严格颜色渲染**

```bash
#!/usr/bin/env bash
set -euo pipefail
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
[[ $# -eq 2 ]] || { echo "usage: $0 <blue|green> <output>" >&2; exit 64; }
case "$1" in
  blue) blue_role=""; green_role="backup" ;;
  green) blue_role="backup"; green_role="" ;;
  *) echo "invalid primary color: $1" >&2; exit 64 ;;
esac
sed -e "s/@@BLUE_ROLE@@/$blue_role/" -e "s/@@GREEN_ROLE@@/$green_role/" \
  "$SCRIPT_DIR/haproxy.cfg.tmpl" > "$2"
if grep -q '@@' "$2"; then
  echo 'unresolved HAProxy template token' >&2
  exit 1
fi
```

- [ ] **Step 5: 运行绿灯、shellcheck 并提交**

```bash
bash deploy/blue-green/tests/test_render_haproxy.sh
shellcheck deploy/blue-green/render-haproxy.sh deploy/blue-green/tests/*.sh
chmod +x deploy/blue-green/render-haproxy.sh deploy/blue-green/tests/test_render_haproxy.sh
git add deploy/blue-green/haproxy.cfg.tmpl deploy/blue-green/render-haproxy.sh deploy/blue-green/tests/test_render_haproxy.sh
git commit -m "ops: add HAProxy blue-green renderer"
```

## Task 4: 实现状态与资源门禁库

**Files:**
- Create: `deploy/blue-green/lib.sh`
- Create: `deploy/blue-green/tests/test_lib.sh`

- [ ] **Step 1: 写状态、镜像和资源失败测试**

```bash
load_state "$STATE_FILE"
assert_eq "$ACTIVE_COLOR" blue
assert_eq "$(inactive_color "$ACTIVE_COLOR")" green
validate_target_image 'ghcr.io/example/sub2api:sha-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa'
if validate_target_image 'ghcr.io/example/sub2api:sha-abc123'; then fail 'short SHA accepted'; fi
if resource_values_ok 100 4096 200 2048; then fail 'low memory accepted'; fi
resource_values_ok 300 4096 200 2048
```

测试还必须覆盖未知状态键、无效颜色、缺失 env/data 路径和原子状态写入。

- [ ] **Step 2: 运行测试并确认红灯**

Run: `bash deploy/blue-green/tests/test_lib.sh`

Expected: FAIL，`lib.sh` 不存在或函数未定义。

- [ ] **Step 3: 实现无 `source` 的状态解析和纯函数**

```bash
load_state() {
  local file="$1" key value
  ACTIVE_COLOR= BLUE_IMAGE= GREEN_IMAGE= DEPLOY_ENV_FILE= DEPLOY_DATA_PATH=
  while IFS='=' read -r key value; do
    [[ -z "$key" || "$key" == \#* ]] && continue
    case "$key" in
      ACTIVE_COLOR) ACTIVE_COLOR="$value" ;;
      BLUE_IMAGE) BLUE_IMAGE="$value" ;;
      GREEN_IMAGE) GREEN_IMAGE="$value" ;;
      DEPLOY_ENV_FILE) DEPLOY_ENV_FILE="$value" ;;
      DEPLOY_DATA_PATH) DEPLOY_DATA_PATH="$value" ;;
      *) echo "unknown state key: $key" >&2; return 1 ;;
    esac
  done < "$file"
  [[ "$ACTIVE_COLOR" == blue || "$ACTIVE_COLOR" == green ]] || return 1
  [[ -n "$BLUE_IMAGE" && -n "$GREEN_IMAGE" && -f "$DEPLOY_ENV_FILE" && -d "$DEPLOY_DATA_PATH" ]]
}
inactive_color() { [[ "$1" == blue ]] && printf 'green\n' || printf 'blue\n'; }
validate_target_image() { [[ "$1" =~ ^ghcr\.io/[a-z0-9._/-]+:sha-[0-9a-f]{40}$ ]]; }
resource_values_ok() {
  local available_mb="$1" disk_mb="$2" min_memory_mb="$3" min_disk_mb="$4"
  (( available_mb >= min_memory_mb && disk_mb >= min_disk_mb ))
}
```

`write_state` 使用同目录 `mktemp`、权限 `0640` 和 `mv` 原子替换，不得先截断正式状态文件。

- [ ] **Step 4: 运行绿灯并提交**

```bash
bash deploy/blue-green/tests/test_lib.sh
shellcheck deploy/blue-green/lib.sh deploy/blue-green/tests/test_lib.sh
chmod +x deploy/blue-green/tests/test_lib.sh
git add deploy/blue-green/lib.sh deploy/blue-green/tests/test_lib.sh
git commit -m "ops: add deployment state and resource gates"
```

## Task 5: 实现发布编排和连接排空

**Files:**
- Create: `deploy/blue-green/deploy.sh`
- Create: `deploy/blue-green/tests/test_deploy_plan.sh`

- [ ] **Step 1: 写 dry-run 发布顺序失败测试**

从 `ACTIVE_COLOR=blue` 开始，用合法镜像执行 `deploy.sh --dry-run IMAGE`，依次断言：pull、启动 Green、验证 `18082`、渲染 Green primary、校验/reload、观察 Green、排空并停止 Blue。从 Green 开始时方向必须完全反转；无效镜像必须在任何变更动作前失败。

```bash
assert_contains "$output" 'pull target image'
assert_contains "$output" 'start green on 18082'
assert_contains "$output" 'verify green health'
assert_contains "$output" 'render HAProxy primary=green backup=blue'
assert_contains "$output" 'validate and reload HAProxy'
assert_contains "$output" 'observe green and drain blue'
assert_contains "$output" 'stop blue after 30 seconds at zero connections'
```

- [ ] **Step 2: 运行测试并确认红灯**

Run: `bash deploy/blue-green/tests/test_deploy_plan.sh`

Expected: FAIL，`deploy.sh` 不存在。

- [ ] **Step 3: 实现参数、锁和前置检查**

真实模式要求 root，并检查依赖、互斥锁、当前入口/活动实例/PostgreSQL/Redis、镜像格式、内存和磁盘：

```bash
required_commands=(docker curl jq haproxy systemctl ss flock)
for command_name in "${required_commands[@]}"; do
  command -v "$command_name" >/dev/null || { echo "missing command: $command_name" >&2; exit 1; }
done
exec 9>"${DEPLOY_LOCK_FILE:-/run/lock/sub2api-blue-green.lock}"
flock -n 9 || { echo 'another deployment is running' >&2; exit 1; }
```

默认资源门禁为可用内存 200 MiB、磁盘 2048 MiB，Task 10 按实测调整。失败时不得启动候选容器。

- [ ] **Step 4: 拉取镜像、比较迁移指纹并创建可验证备份**

先执行 `docker pull "$target_image"`，再读取目标和活动镜像标签 `io.sub2api.migrations-sha`。标签不同或任一缺失时，在启动候选实例前执行：

```bash
backup_file="/home/ubuntu/ResearchWang13/backups/pre-deploy-$(date +%Y%m%d-%H%M%S).dump"
umask 077
docker exec sub2api_postgres pg_dump -U sub2api -d sub2api -Fc > "$backup_file"
test -s "$backup_file"
docker exec -i sub2api_postgres pg_restore -l < "$backup_file" >/dev/null
```

任一命令失败就终止发布。备份路径写入部署审计记录，不自动执行反向数据库迁移。

- [ ] **Step 5: 实现候选实例启动和镜像验证**

先写候选状态临时文件，正式状态保持不变：

```bash
docker compose --project-name sub2api-bg --env-file "$candidate_state" \
  -f "$COMPOSE_FILE" up -d "$inactive"
container_id="$(docker inspect --format '{{.Image}}' "sub2api_${inactive}")"
image_id="$(docker image inspect --format '{{.Id}}' "$target_image")"
[[ "$container_id" == "$image_id" ]]
```

按颜色选择 `18081/18082`，用 `jq -e '.status == "ok"'` 解析 `/health`，要求连续三次成功。失败时停止候选并删除临时状态，不修改 HAProxy。

- [ ] **Step 6: 实现 HAProxy 原子切换和失败恢复**

渲染到临时文件，执行 `haproxy -c`，备份正式配置，`install -m 0644` 后 `systemctl reload haproxy`。修改前注册恢复 trap；只有 `8080/health`、新 primary 直接健康和状态原子写入全部成功后解除恢复标志。

- [ ] **Step 7: 实现观察、排空和审计**

- 新版本观察至少 120 秒，每 2 秒检查 primary、入口和 HAProxy。
- 使用 `ss -Hnt state established '( sport = :PORT )'` 统计旧实例连接。
- 旧连接连续 30 秒为零后才停止旧容器。
- 到达最大观察时间仍有连接时保留双实例并退出非零，不强杀。
- 使用 `jq -cn` 向 `deployments.jsonl` 追加 SHA、颜色、时间和结果，不手写 JSON。

- [ ] **Step 8: 运行绿灯并提交**

```bash
bash deploy/blue-green/tests/test_deploy_plan.sh
shellcheck deploy/blue-green/deploy.sh deploy/blue-green/tests/test_deploy_plan.sh
chmod +x deploy/blue-green/deploy.sh deploy/blue-green/tests/test_deploy_plan.sh
git add deploy/blue-green/deploy.sh deploy/blue-green/tests/test_deploy_plan.sh
git commit -m "ops: add guarded blue-green deployment flow"
```

## Task 6: 增加状态查询和回滚入口

**Files:**
- Create: `deploy/blue-green/status.sh`
- Create: `deploy/blue-green/rollback.sh`
- Modify: `deploy/blue-green/tests/test_deploy_plan.sh`

- [ ] **Step 1: 写回滚方向失败测试**

从 Blue 活动状态运行 `rollback.sh --dry-run`，断言启动并验证 Green 保存的旧镜像、将 Green 设为 primary、排空 Blue；从 Green 活动状态断言反向顺序。

- [ ] **Step 2: 运行测试并确认红灯**

Run: `bash deploy/blue-green/tests/test_deploy_plan.sh`

Expected: FAIL，`rollback.sh` 不存在。

- [ ] **Step 3: 实现回滚包装器**

回滚读取另一颜色保存的镜像，并调用 `deploy.sh` 的相同切换实现，不复制 HAProxy 或排空逻辑：

```bash
inactive="$(inactive_color "$ACTIVE_COLOR")"
if [[ "$inactive" == blue ]]; then rollback_image="$BLUE_IMAGE"; else rollback_image="$GREEN_IMAGE"; fi
exec "$SCRIPT_DIR/deploy.sh" "${dry_run_args[@]}" --activate-existing "$inactive" "$rollback_image"
```

`--activate-existing` 仍须启动容器、验证摘要和健康，只跳过新目标的 SHA 格式限制，不跳过 HAProxy 校验、观察或排空。

- [ ] **Step 4: 实现只读状态脚本**

`status.sh` 输出但不改变：状态颜色/镜像、HAProxy systemd 状态、两个容器状态、`8080/18081/18082` 健康、HAProxy `show stat` 摘要、内存/Swap、磁盘和最后部署记录。每个读取设置超时；缺少组件时继续收集其他信息并最终退出非零。

- [ ] **Step 5: 运行绿灯并提交**

```bash
bash deploy/blue-green/tests/run.sh
shellcheck deploy/blue-green/*.sh deploy/blue-green/tests/*.sh
chmod +x deploy/blue-green/status.sh deploy/blue-green/rollback.sh
git add deploy/blue-green/status.sh deploy/blue-green/rollback.sh deploy/blue-green/tests/test_deploy_plan.sh
git commit -m "ops: add blue-green status and rollback commands"
```

## Task 7: 增加 CI 验证和不可变生产镜像工作流

**Files:**
- Modify: `.github/workflows/backend-ci.yml`
- Create: `.github/workflows/production-image.yml`

- [ ] **Step 1: 在 CI 中加入部署产物验证 job**

在 `backend-ci.yml` 增加独立 `deployment-assets` job：checkout，安装 `haproxy`、`shellcheck`、`jq`，然后执行：

```bash
shellcheck deploy/blue-green/*.sh deploy/blue-green/tests/*.sh
bash deploy/blue-green/tests/run.sh
```

- [ ] **Step 2: 本地运行与 CI 相同的命令**

Expected: 两个命令退出码均为 0。

- [ ] **Step 3: 创建手动生产镜像工作流**

工作流入口和权限必须为：

```yaml
name: Production Image
on:
  workflow_dispatch:
    inputs:
      ref:
        description: Tested Git ref to build
        required: true
        default: main
        type: string
permissions:
  contents: read
  packages: write
```

`verify` job checkout 指定 ref，使用仓库现有 Go/Node/pnpm 版本，运行 `make test-unit`、`make test-integration`、`make test-frontend`。`build` job `needs: verify`，再次 checkout 同一 ref，得到完整 SHA 和小写 GHCR 路径：

```bash
echo "sha=$(git rev-parse HEAD)" >> "$GITHUB_OUTPUT"
echo "image=ghcr.io/${GITHUB_REPOSITORY,,}" >> "$GITHUB_OUTPUT"
```

同一步计算迁移指纹并写入 `migrations_sha` output：

```bash
migrations_sha="$(find backend/migrations -type f -name '*.sql' -print0 | sort -z | xargs -0 sha256sum | sha256sum | awk '{print $1}')"
echo "migrations_sha=$migrations_sha" >> "$GITHUB_OUTPUT"
```

使用 `docker/login-action@v3`、`docker/setup-buildx-action@v3`、`docker/build-push-action@v6` 构建根 `Dockerfile`：

```yaml
tags: ${{ steps.meta.outputs.image }}:sha-${{ steps.meta.outputs.sha }}
push: true
platforms: linux/amd64
cache-from: type=gha
cache-to: type=gha,mode=max
labels: io.sub2api.migrations-sha=${{ steps.meta.outputs.migrations_sha }}
```

将完整镜像引用和 digest 写入 Step Summary，不通过 SSH 自动部署生产。

- [ ] **Step 4: 校验 workflow 并提交**

```bash
docker run --rm -v "$PWD:/repo" -w /repo rhysd/actionlint:1.7.7
git add .github/workflows/backend-ci.yml .github/workflows/production-image.yml
git commit -m "ci: build immutable production images"
```

Expected: actionlint 无错误，workflow 无生产 SSH 凭据。

## Task 8: 编写操作手册和检查表

**Files:**
- Create: `deploy/blue-green/README.md`

- [ ] **Step 1: 编写无密钥操作手册**

手册包含以下精确入口：

```bash
sudo ./status.sh
sudo ./deploy.sh --dry-run ghcr.io/example/sub2api:sha-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
sudo ./deploy.sh ghcr.io/example/sub2api:sha-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
sudo ./rollback.sh --dry-run
sudo ./rollback.sh
```

同时记录依赖、安装目录、状态格式、首次切换、迁移门禁、排空行为、日志、HAProxy 恢复和 legacy 回退。示例不能复制生产 `.env`。

- [ ] **Step 2: 增加部署前后检查表**

检查表要求 CI 绿色、完整 SHA、数据库备份、资源门禁、当前健康、维护窗口、直接端口/入口/长连接测试、观察期、连接归零和生产 changelog。

- [ ] **Step 3: 验证引用并提交**

```bash
for script in status.sh deploy.sh rollback.sh render-haproxy.sh; do test -x "deploy/blue-green/$script"; done
rg -n 'docker compose down|down -v|latest' deploy/blue-green/README.md
git add deploy/blue-green/README.md
git commit -m "docs: add blue-green deployment runbook"
```

Expected: 脚本均可执行；`latest` 只能出现在禁止说明中。

## Task 9: 完整开发验证与代码审查

**Files:**
- Verify: all files changed in Tasks 2-8

- [ ] **Step 1: 运行部署工具测试**

```bash
shellcheck deploy/blue-green/*.sh deploy/blue-green/tests/*.sh
bash deploy/blue-green/tests/run.sh
```

Expected: 全部退出码为 0。

- [ ] **Step 2: 运行项目回归测试**

```bash
cd backend && make test-unit && make test-integration
cd .. && make test-frontend
```

Expected: 后端单元/集成和前端测试通过。

- [ ] **Step 3: 检查范围和敏感信息**

```bash
git status --short
git diff main...HEAD --check
git diff main...HEAD --name-only
git diff main...HEAD | rg -n '(PASSWORD|TOKEN|SECRET)=([^$<{]|$)' && exit 1 || true
```

Expected: 只含计划文件，无生产 secret 值。

- [ ] **Step 4: 调用代码审查技能**

使用 `superpowers:requesting-code-review` 检查 shell 安全、失败恢复、HAProxy 长连接和生产命令。修复后重新运行 Steps 1-3，并为修复创建独立提交。

## Task 10: 安装到生产并进行无流量切换预演

**Files:**
- Create directory: `/home/ubuntu/ResearchWang13/blue-green/`
- Create: `/home/ubuntu/ResearchWang13/blue-green/versions.env`
- Install: version-controlled `deploy/blue-green/` artifacts
- Modify: `/home/ubuntu/ResearchWang13/docker-compose.yml`
- Backup/Create: `/etc/haproxy/haproxy.cfg`

- [ ] **Step 1: 记录生产基线并创建备份**

```bash
cd /home/ubuntu/ResearchWang13
docker ps --format 'table {{.Names}}\t{{.Image}}\t{{.Status}}'
systemctl --user is-active sub2api-path-proxy.service sub2api-auto-clean.service
curl -fsS http://127.0.0.1:8080/health
curl -fsS http://127.0.0.1:18080/health
cp docker-compose.yml "docker-compose.yml.bak-$(date +%Y%m%d-%H%M%S)-pre-haproxy"
```

Expected: 两个健康接口返回 `{"status":"ok"}`，用户服务 active，备份存在。

- [ ] **Step 2: 安装依赖但不启动 HAProxy**

```bash
sudo apt-get update
sudo apt-get install -y haproxy socat
sudo systemctl stop haproxy
sudo systemctl disable haproxy
```

Expected: HAProxy/Socat 可执行，`18080` 仍由 `sub2api_core` 使用。

- [ ] **Step 3: 安装产物并创建非敏感状态**

从功能 worktree 用 `install` 复制脚本、Compose 和模板到生产目录；脚本 `0750`，配置 `0640`。状态内容：

```dotenv
ACTIVE_COLOR=blue
BLUE_IMAGE=ghcr.io/wei-shaw/sub2api:0.1.132
GREEN_IMAGE=ghcr.io/wei-shaw/sub2api:0.1.132
DEPLOY_ENV_FILE=/home/ubuntu/ResearchWang13/.env
DEPLOY_DATA_PATH=/home/ubuntu/ResearchWang13/data
```

不得复制或打印 `.env` 内容。

- [ ] **Step 4: 隔离 legacy 服务**

给生产 Compose 的旧 `sub2api` 增加：

```yaml
profiles: ["legacy"]
```

删除 `cloudflared` 的 `depends_on: sub2api` 块。验证：

```bash
docker compose config >/tmp/sub2api-compose-after-haproxy-plan.yml
docker compose --profile legacy config >/tmp/sub2api-compose-with-legacy.yml
```

Expected: 均退出 0，运行容器未重启。

- [ ] **Step 5: 启动当前镜像 Blue 并预演双实例**

```bash
cd /home/ubuntu/ResearchWang13/blue-green
docker compose --project-name sub2api-bg --env-file versions.env -f compose.yml up -d blue
curl -fsS http://127.0.0.1:18081/health
docker stats --no-stream sub2api_core sub2api_blue
```

观察至少 2 分钟。Expected: `18080/18081` 同时健康，无 OOM 或重复业务副作用。

- [ ] **Step 6: 渲染首次配置但不启动 HAProxy**

```bash
sudo /home/ubuntu/ResearchWang13/blue-green/render-haproxy.sh blue /etc/haproxy/haproxy.cfg.candidate
sudo haproxy -c -f /etc/haproxy/haproxy.cfg.candidate
ss -lntp | rg ':18080|:18081|:18082'
```

Expected: 配置有效，公网仍走旧 `sub2api_core:18080`。

- [ ] **Step 7: 验证首次回退命令**

```bash
sudo systemctl stop haproxy
docker start sub2api_core
curl -fsS http://127.0.0.1:18080/health
curl -fsS http://127.0.0.1:8080/health
```

Expected: 回退不依赖数据库、Redis、Cloudflare 或 Python 代理重启。

## Task 11: 执行首次 HAProxy 生产切换

**Files:**
- Activate: `/etc/haproxy/haproxy.cfg`
- Modify: `/home/ubuntu/ResearchWang13/docs/ARCHITECTURE.md`
- Modify: `/home/ubuntu/ResearchWang13/docs/CHANGE_PROCESS.md`
- Modify: `/home/ubuntu/ResearchWang13/docs/CHANGELOG.md`

- [ ] **Step 1: 获取维护窗口确认**

报告 Task 10 的资源、健康和回退演练结果；只有用户再次明确确认 5-15 秒窗口后继续。

- [ ] **Step 2: 最终健康和连接检查**

```bash
curl -fsS http://127.0.0.1:8080/health
curl -fsS http://127.0.0.1:18080/health
curl -fsS http://127.0.0.1:18081/health
ss -Hnt state established '( sport = :18080 )' | wc -l
free -m
```

Expected: 三个健康检查成功，选择连接尽量少的时点。

- [ ] **Step 3: 在窗口内完成端口接管**

```bash
docker stop --time 10 sub2api_core
sudo install -m 0644 /etc/haproxy/haproxy.cfg.candidate /etc/haproxy/haproxy.cfg
sudo systemctl enable --now haproxy
curl -fsS --retry 10 --retry-delay 1 http://127.0.0.1:18080/health
curl -fsS --retry 10 --retry-delay 1 http://127.0.0.1:8080/health
```

Expected: HAProxy 监听 `127.0.0.1:18080`，窗口不超过约 15 秒。

- [ ] **Step 4: 执行登录、管理端和 AI 冒烟测试**

使用现有安全测试方式验证登录、只读管理 API、普通 AI 请求和短流式请求，不打印 API Key。

- [ ] **Step 5: 失败时立即回退**

```bash
sudo systemctl disable --now haproxy
docker start sub2api_core
curl -fsS --retry 10 --retry-delay 1 http://127.0.0.1:18080/health
curl -fsS --retry 10 --retry-delay 1 http://127.0.0.1:8080/health
```

失败后记录症状，不继续部署新镜像。

- [ ] **Step 6: 成功后更新生产文档**

记录新链路、`status.sh/deploy.sh/rollback.sh`、窗口、验证、服务 uptime 和精确回退命令。

## Task 12: 首次无停机发布验收

**Files:**
- Runtime state: `/home/ubuntu/ResearchWang13/blue-green/versions.env`
- Runtime log: `/home/ubuntu/ResearchWang13/blue-green/deployments.jsonl`

- [ ] **Step 1: 构建已测试 SHA 镜像**

手动运行 `Production Image` workflow，输入 CI 已通过的 Git ref，记录完整镜像和 digest。推送代码或触发 workflow 前必须获得用户授权。

- [ ] **Step 2: 启动长连接和并发探针**

启动一条较长 SSE/AI 流、一条 WebSocket 和持续普通 HTTP 探针；只记录时间、状态和断连，不记录密钥或正文。

- [ ] **Step 3: dry-run 后正式发布**

```bash
cd /home/ubuntu/ResearchWang13/blue-green
sudo ./deploy.sh --dry-run ghcr.io/example/sub2api:sha-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
sudo ./deploy.sh ghcr.io/example/sub2api:sha-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
```

Expected: Green 预检通过后接管新请求，Blue 只处理已有连接并在归零后停止。

- [ ] **Step 4: 验证验收标准**

- 普通探针在计划发布期间零失败。
- 发布前建立的 SSE/WebSocket 不被部署主动断开。
- 新请求进入新 primary，旧连接归零前旧实例不停止。
- PostgreSQL、Redis、Cloudflare 和 Python 代理 uptime 未重置。
- 双实例无 OOM，Swap 没有持续失控增长。
- `deployments.jsonl` 是有效 JSONL 且不含 secret。

- [ ] **Step 5: 演练回滚**

```bash
sudo ./rollback.sh --dry-run
sudo ./rollback.sh
sudo ./status.sh
```

Expected: 旧镜像健康后成为 primary，新实例排空后停止，入口持续健康。

- [ ] **Step 6: 记录证据**

将探针数量、失败数、长连接结果、资源峰值、切换/排空/回滚耗时和 SHA 写入生产 changelog。只有所有验收项有实际输出支持时才标记完成。
