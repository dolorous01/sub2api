<template>
  <section class="overflow-hidden border border-gray-200 bg-white dark:border-dark-700 dark:bg-dark-900">
    <div class="flex flex-wrap items-end justify-between gap-4 border-b border-gray-200 px-4 py-4 dark:border-dark-700 sm:px-5">
      <div>
        <h2 class="text-lg font-semibold text-gray-900 dark:text-white">{{ t('admin.imageCanvas.backendTitle') }}</h2>
        <p class="mt-1 max-w-3xl text-sm text-gray-500 dark:text-gray-400">{{ t('admin.imageCanvas.backendDescription') }}</p>
      </div>
      <div class="inline-flex max-w-full overflow-x-auto rounded-md bg-gray-100 p-1 dark:bg-dark-800">
        <button
          v-for="view in views"
          :key="view.id"
          type="button"
          class="inline-flex h-8 shrink-0 items-center gap-1.5 rounded px-3 text-xs font-medium transition-colors"
          :class="activeView === view.id ? 'bg-white text-gray-900 shadow-sm dark:bg-dark-700 dark:text-white' : 'text-gray-500 hover:text-gray-800 dark:text-gray-400 dark:hover:text-gray-200'"
          :aria-pressed="activeView === view.id"
          @click="activeView = view.id"
        >
          <Icon :name="view.icon" size="xs" />
          {{ t(view.label) }}
        </button>
      </div>
    </div>

    <ol v-if="activeView === 'flow'" class="grid gap-px bg-gray-200 dark:bg-dark-700 sm:grid-cols-2 xl:grid-cols-3 2xl:grid-cols-6">
      <li v-for="(step, index) in flowSteps" :key="step.service" class="min-w-0 bg-white px-4 py-5 dark:bg-dark-900">
        <div class="flex items-center gap-2">
          <span class="flex h-6 w-6 shrink-0 items-center justify-center rounded bg-gray-900 text-xs font-semibold text-white dark:bg-gray-100 dark:text-gray-900">{{ index + 1 }}</span>
          <span class="text-xs font-semibold uppercase text-gray-500 dark:text-gray-400">{{ t(step.title) }}</span>
        </div>
        <div class="mt-3 break-words font-mono text-xs font-medium text-gray-800 dark:text-gray-100">{{ step.service }}</div>
        <p class="mt-2 text-xs leading-5 text-gray-500 dark:text-gray-400">{{ t(step.detail) }}</p>
      </li>
    </ol>

    <div v-else-if="activeView === 'objects'" class="overflow-x-auto">
      <table class="w-full min-w-[920px] text-left text-sm">
        <thead class="bg-gray-50 text-xs uppercase text-gray-500 dark:bg-dark-950 dark:text-gray-400">
          <tr>
            <th class="px-4 py-3">{{ t('admin.imageCanvas.objectName') }}</th>
            <th class="px-4 py-3">{{ t('admin.imageCanvas.objectPurpose') }}</th>
            <th class="px-4 py-3">{{ t('admin.imageCanvas.keyFields') }}</th>
            <th class="px-4 py-3">{{ t('admin.imageCanvas.ownerModule') }}</th>
          </tr>
        </thead>
        <tbody class="divide-y divide-gray-100 dark:divide-dark-800">
          <tr v-for="object in dataObjects" :key="object.name" class="align-top">
            <td class="whitespace-nowrap px-4 py-3 font-mono text-xs font-medium text-gray-800 dark:text-gray-100">{{ object.name }}</td>
            <td class="max-w-[360px] px-4 py-3 text-xs leading-5 text-gray-600 dark:text-gray-300">{{ t(object.purpose) }}</td>
            <td class="max-w-[420px] px-4 py-3 font-mono text-xs leading-5 text-gray-500 dark:text-gray-400">{{ object.fields }}</td>
            <td class="whitespace-nowrap px-4 py-3 font-mono text-xs text-gray-500 dark:text-gray-400">{{ object.owner }}</td>
          </tr>
        </tbody>
      </table>
    </div>

    <div v-else class="grid min-h-[430px] lg:grid-cols-[260px_minmax(0,1fr)]">
      <div class="border-b border-gray-200 bg-gray-50/70 p-2 dark:border-dark-700 dark:bg-dark-950/40 lg:border-b-0 lg:border-r">
        <button
          v-for="query in queries"
          :key="query.id"
          type="button"
          class="flex w-full items-start gap-2 rounded px-3 py-2.5 text-left transition-colors"
          :class="activeQueryID === query.id ? 'bg-white text-gray-900 shadow-sm dark:bg-dark-700 dark:text-white' : 'text-gray-600 hover:bg-white/70 dark:text-gray-400 dark:hover:bg-dark-800'"
          :aria-pressed="activeQueryID === query.id"
          @click="activeQueryID = query.id"
        >
          <Icon name="database" size="xs" class="mt-0.5 shrink-0" />
          <span class="min-w-0">
            <span class="block text-xs font-medium">{{ t(query.label) }}</span>
            <span class="mt-0.5 block truncate font-mono text-[11px] opacity-70">{{ query.source }}</span>
          </span>
        </button>
      </div>
      <div class="min-w-0 bg-[#111827]">
        <div class="flex flex-wrap items-center justify-between gap-2 border-b border-white/10 px-4 py-3 text-xs text-gray-300">
          <span>{{ t(activeQuery.label) }}</span>
          <span class="font-mono text-gray-500">{{ activeQuery.source }}</span>
        </div>
        <pre class="thin-scrollbar max-h-[520px] overflow-auto p-4 text-xs leading-6 text-gray-200"><code>{{ activeQuery.sql }}</code></pre>
      </div>
    </div>
  </section>
</template>

<script setup lang="ts">
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import Icon from '@/components/icons/Icon.vue'

type ViewID = 'flow' | 'objects' | 'sql'
type TranslationKey = string
type Query = { id: string; label: TranslationKey; source: string; sql: string }

const { t } = useI18n()
const activeView = ref<ViewID>('flow')
const activeQueryID = ref('catalog')

const views: Array<{ id: ViewID; label: TranslationKey; icon: 'sort' | 'document' | 'database' }> = [
  { id: 'flow', label: 'admin.imageCanvas.logicFlow', icon: 'sort' },
  { id: 'objects', label: 'admin.imageCanvas.dataObjects', icon: 'document' },
  { id: 'sql', label: 'admin.imageCanvas.sqlQueries', icon: 'database' }
]

const flowSteps = [
  { title: 'admin.imageCanvas.flowGate', service: 'ImageCanvasEnabled middleware', detail: 'admin.imageCanvas.flowGateDetail' },
  { title: 'admin.imageCanvas.flowConfig', service: 'ImageCanvasHandler.GetConfig', detail: 'admin.imageCanvas.flowConfigDetail' },
  { title: 'admin.imageCanvas.flowPolicy', service: 'BuildImageAttemptPlan', detail: 'admin.imageCanvas.flowPolicyDetail' },
  { title: 'admin.imageCanvas.flowReserve', service: 'ImageJobService.Create', detail: 'admin.imageCanvas.flowReserveDetail' },
  { title: 'admin.imageCanvas.flowWorker', service: 'ImageJobWorker', detail: 'admin.imageCanvas.flowWorkerDetail' },
  { title: 'admin.imageCanvas.flowPersist', service: 'ImageCanvasProjectService', detail: 'admin.imageCanvas.flowPersistDetail' }
]

const dataObjects = [
  { name: 'settings', purpose: 'admin.imageCanvas.objectSettings', fields: 'key, value, updated_at', owner: 'SettingService' },
  { name: 'api_keys', purpose: 'admin.imageCanvas.objectKeys', fields: 'id, user_id, group_id, status, expires_at, quota, quota_used', owner: 'APIKeyRepository' },
  { name: 'groups', purpose: 'admin.imageCanvas.objectGroups', fields: 'id, status, allow_image_generation, models_list_config', owner: 'GroupRepository' },
  { name: 'accounts + account_groups', purpose: 'admin.imageCanvas.objectAccounts', fields: 'platform, type, status, schedulable, credentials, group_id, priority', owner: 'AccountRepository' },
  { name: 'image_model_policies + items', purpose: 'admin.imageCanvas.objectPolicy', fields: 'version, enabled, model, position', owner: 'ImageModelPolicyRepository' },
  { name: 'image_model_policy_audits', purpose: 'admin.imageCanvas.objectAudit', fields: 'operator_user_id, old_version, new_version, before_value, after_value', owner: 'ImageModelPolicyRepository' },
  { name: 'image_jobs', purpose: 'admin.imageCanvas.objectJobs', fields: 'status, selected_model, attempt_plan, attempt_log, successful_model, execution_phase', owner: 'ImageJobRepository' },
  { name: 'image_job_results + image_assets', purpose: 'admin.imageCanvas.objectResults', fields: 'job_id, index, object_key, asset_id, project_id, owner_user_id', owner: 'ImageJobRepository' }
]

const queries: Query[] = [
  {
    id: 'entry',
    label: 'admin.imageCanvas.queryEntry',
    source: 'setting_repo.go',
    sql: `SELECT id, key, value, updated_at
FROM settings
WHERE key = 'image_canvas_enabled'
LIMIT 1;`
  },
  {
    id: 'catalog',
    label: 'admin.imageCanvas.queryCatalog',
    source: 'account_repo.go + image_model_catalog.go',
    sql: `SELECT *
FROM accounts
WHERE status = 'active'
  AND schedulable = TRUE
  AND deleted_at IS NULL
  AND (temp_unschedulable_until IS NULL OR temp_unschedulable_until <= NOW())
  AND (expires_at IS NULL OR expires_at > NOW() OR auto_pause_on_expired = FALSE)
  AND (overload_until IS NULL OR overload_until <= NOW())
  AND (rate_limit_reset_at IS NULL OR rate_limit_reset_at <= NOW())
ORDER BY priority ASC;

SELECT account_id, group_id, priority
FROM account_groups
WHERE account_id = ANY($1)
ORDER BY account_id, priority;

SELECT id, status, allow_image_generation, models_list_config
FROM groups
WHERE id = ANY($2);

SELECT id, user_id, group_id, status, expires_at, quota, quota_used
FROM api_keys
WHERE group_id = $3 AND deleted_at IS NULL
ORDER BY id ASC
LIMIT $4 OFFSET $5;`
  },
  {
    id: 'policy',
    label: 'admin.imageCanvas.queryPolicy',
    source: 'image_model_policy_repo.go',
    sql: `SELECT version, enabled
FROM image_model_policies
WHERE id = 1;

SELECT model, enabled, position
FROM image_model_policy_items
WHERE policy_id = 1
ORDER BY position ASC;

SELECT id, operator_user_id, old_version, new_version,
       before_value, after_value, created_at
FROM image_model_policy_audits
ORDER BY id DESC
LIMIT $1;`
  },
  {
    id: 'create',
    label: 'admin.imageCanvas.queryCreateJob',
    source: 'image_job_repo.go:CreateReserved',
    sql: `INSERT INTO image_jobs (
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
RETURNING id, public_id, status, attempt_plan, created_at;`
  },
  {
    id: 'worker',
    label: 'admin.imageCanvas.queryWorker',
    source: 'image_job_repo.go:ClaimNext',
    sql: `WITH next AS (
  SELECT id
  FROM image_jobs
  WHERE status = 'queued'
  ORDER BY created_at, id
  LIMIT 1
  FOR UPDATE SKIP LOCKED
)
UPDATE image_jobs j
SET status = 'running', worker_id = $1, attempt_id = $2,
    execution_phase = 'preflight', heartbeat_at = NOW(),
    started_at = COALESCE(started_at, NOW()), updated_at = NOW()
FROM next
WHERE j.id = next.id
RETURNING j.*;`
  },
  {
    id: 'attempts',
    label: 'admin.imageCanvas.queryAttempts',
    source: 'image_job_repo.go:RecordImageModelAttempt',
    sql: `UPDATE image_jobs
SET attempt_log = attempt_log || $3::jsonb,
    successful_model = COALESCE(NULLIF($4, ''), successful_model),
    heartbeat_at = NOW(), updated_at = NOW()
WHERE id = $1
  AND attempt_id = $2
  AND status = 'running';`
  },
  {
    id: 'recent',
    label: 'admin.imageCanvas.queryRecentJobs',
    source: 'image_job_repo.go:ListRecentAdmin',
    sql: `SELECT id, public_id, user_id, api_key_id, group_id,
       operation, mode, requested_model, mapped_model, status,
       requested_count, completed_count, execution_phase,
       error_type, error_code, error_message, error_retryable,
       started_at, finished_at, created_at, updated_at,
       project_id, selected_model, policy_version,
       attempt_plan, successful_model, attempt_log
FROM image_jobs
ORDER BY created_at DESC, id DESC
LIMIT $1;`
  }
]

const activeQuery = computed<Query>(() => queries.find((query) => query.id === activeQueryID.value) || queries[0]!)
</script>
