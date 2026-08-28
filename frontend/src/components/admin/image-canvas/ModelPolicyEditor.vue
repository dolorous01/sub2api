<template>
  <div class="space-y-5">
    <div class="flex flex-wrap items-end justify-between gap-3">
      <div>
        <h2 class="text-lg font-semibold text-gray-900 dark:text-white">{{ t('admin.imageCanvas.policyTitle') }}</h2>
        <p class="mt-1 max-w-3xl text-sm text-gray-500 dark:text-gray-400">{{ t('admin.imageCanvas.policyDescription') }}</p>
      </div>
      <div class="flex items-center gap-2">
        <button type="button" class="icon-button" :disabled="loading" :title="t('common.refresh')" :aria-label="t('common.refresh')" @click="load">
          <Icon name="refresh" size="sm" :class="loading ? 'animate-spin' : ''" />
        </button>
        <button type="button" class="btn btn-primary btn-sm" :disabled="loading || saving || !dirty || !hasEnabledModel || hasInvalidEnabledModel" @click="save">
          <Icon name="check" size="sm" />
          {{ saving ? t('common.saving') : t('common.save') }}
        </button>
      </div>
    </div>

    <div v-if="loading && !policy" class="flex justify-center py-12">
      <span class="h-7 w-7 animate-spin rounded-full border-2 border-primary-500 border-t-transparent" />
    </div>

    <template v-else-if="policy">
      <section class="overflow-hidden border border-gray-200 bg-white shadow-sm dark:border-dark-700 dark:bg-dark-900">
        <div class="flex flex-wrap items-center justify-between gap-4 border-b border-gray-200 px-4 py-4 dark:border-dark-700 sm:px-5">
          <div class="flex items-center gap-3">
            <span class="flex h-9 w-9 items-center justify-center rounded-lg bg-primary-50 text-primary-600 dark:bg-primary-950/40 dark:text-primary-300">
              <Icon name="sort" size="sm" />
            </span>
            <div>
              <div class="font-medium text-gray-900 dark:text-white">{{ t('admin.imageCanvas.modelPolicySwitch') }}</div>
              <div class="text-xs text-gray-500 dark:text-gray-400">{{ t('admin.imageCanvas.modelPolicySwitchHint') }}</div>
            </div>
          </div>
          <label class="inline-flex cursor-pointer items-center gap-3 text-sm font-medium text-gray-700 dark:text-gray-200">
            <span :class="draftEnabled ? 'text-emerald-600 dark:text-emerald-400' : 'text-gray-500 dark:text-gray-400'">
              {{ draftEnabled ? t('common.enabled') : t('common.disabled') }}
            </span>
            <input v-model="draftEnabled" type="checkbox" class="h-5 w-5 accent-primary-600" />
          </label>
        </div>

        <div class="overflow-x-auto">
          <table class="w-full min-w-[900px] text-left text-sm">
            <thead class="bg-gray-50 text-xs uppercase text-gray-500 dark:bg-dark-950 dark:text-gray-400">
              <tr>
                <th class="w-14 px-4 py-3 text-center">#</th>
                <th class="px-3 py-3">{{ t('admin.imageCanvas.model') }}</th>
                <th class="px-3 py-3">{{ t('admin.imageCanvas.providerMedia') }}</th>
                <th class="px-3 py-3">{{ t('admin.imageCanvas.coverage') }}</th>
                <th class="px-3 py-3">{{ t('admin.imageCanvas.modelHealth') }}</th>
                <th class="px-3 py-3 text-right">{{ t('admin.imageCanvas.modelEnabled') }}</th>
                <th class="w-44 px-4 py-3 text-right">{{ t('admin.imageCanvas.actions') }}</th>
              </tr>
            </thead>
            <tbody class="divide-y divide-gray-100 dark:divide-dark-800">
              <tr v-for="(item, index) in draft" :key="item.model" class="align-middle transition-colors hover:bg-gray-50/80 dark:hover:bg-dark-800/50">
                <td class="px-4 py-3 text-center font-mono text-xs text-gray-400">{{ index + 1 }}</td>
                <td class="max-w-[260px] px-3 py-3">
                  <div class="truncate font-medium text-gray-900 dark:text-white" :title="item.model">{{ item.model }}</div>
                  <div v-if="catalogFor(item.model)?.schedulability_reason || !catalogFor(item.model)" class="mt-1 text-xs text-amber-600 dark:text-amber-300">
                    {{ reasonLabel(catalogFor(item.model)?.schedulability_reason || 'no_schedulable_account') }}
                  </div>
                </td>
                <td class="px-3 py-3">
                  <div class="flex flex-wrap gap-1.5 text-xs">
                    <span class="inline-flex items-center rounded bg-gray-100 px-2 py-1 text-gray-600 dark:bg-dark-800 dark:text-gray-300">
                      {{ catalogFor(item.model)?.provider || item.capability?.provider || '-' }}
                    </span>
                    <span class="inline-flex items-center rounded bg-sky-50 px-2 py-1 text-sky-700 dark:bg-sky-950/40 dark:text-sky-300">
                      {{ mediaLabel(catalogFor(item.model)?.media_kind || item.capability?.media_kind) }}
                    </span>
                  </div>
                </td>
                <td class="whitespace-nowrap px-3 py-3 text-xs text-gray-600 dark:text-gray-300">
                  <span>{{ coverageFor(item.model).account_count }} {{ t('admin.imageCanvas.accountsShort') }}</span>
                  <span class="mx-1 text-gray-300 dark:text-dark-600">/</span>
                  <span>{{ coverageFor(item.model).group_count }} {{ t('admin.imageCanvas.groupsShort') }}</span>
                  <span class="mx-1 text-gray-300 dark:text-dark-600">/</span>
                  <span>{{ coverageFor(item.model).api_key_count }} {{ t('admin.imageCanvas.keysShort') }}</span>
                </td>
                <td class="px-3 py-3">
                  <div v-if="checks[item.model] === 'loading'" class="inline-flex items-center gap-1.5 text-xs text-gray-500">
                    <Icon name="refresh" size="xs" class="animate-spin" /> {{ t('admin.imageCanvas.checking') }}
                  </div>
                  <div v-else-if="checks[item.model]" class="inline-flex items-center gap-1.5 text-xs" :class="checkClass(checks[item.model])">
                    <Icon :name="checks[item.model] === 'ok' ? 'checkCircle' : (checks[item.model] === 'warning' ? 'exclamationCircle' : 'xCircle')" size="xs" />
                    {{ checkLabel(checks[item.model]) }}
                  </div>
                  <span v-else-if="catalogFor(item.model)?.schedulable" class="inline-flex items-center gap-1.5 text-xs text-emerald-600 dark:text-emerald-400">
                    <Icon name="checkCircle" size="xs" /> {{ t('admin.imageCanvas.schedulable') }}
                  </span>
                  <span v-else class="inline-flex items-center gap-1.5 text-xs text-amber-600 dark:text-amber-300">
                    <Icon name="exclamationTriangle" size="xs" /> {{ t('admin.imageCanvas.notSchedulable') }}
                  </span>
                </td>
                <td class="px-3 py-3 text-right">
                  <label class="inline-flex items-center gap-2 text-xs text-gray-600 dark:text-gray-300">
                    <input v-model="item.enabled" type="checkbox" class="h-4 w-4 accent-primary-600" :disabled="!item.enabled && catalogFor(item.model)?.schedulable !== true" />
                    {{ item.enabled ? t('common.enabled') : t('common.disabled') }}
                  </label>
                </td>
                <td class="px-4 py-3">
                  <div class="flex items-center justify-end gap-1">
                    <button type="button" class="icon-button" :disabled="Boolean(checkingModel)" :title="t('admin.imageCanvas.checkModel')" :aria-label="t('admin.imageCanvas.checkModel')" @click="checkModel(item.model)">
                      <Icon name="beaker" size="sm" :class="checkingModel === item.model ? 'animate-pulse' : ''" />
                    </button>
                    <button type="button" class="icon-button" :disabled="index === 0" :title="t('admin.imageCanvas.moveUp')" :aria-label="t('admin.imageCanvas.moveUp')" @click="move(index, -1)"><Icon name="arrowUp" size="sm" /></button>
                    <button type="button" class="icon-button" :disabled="index === draft.length - 1" :title="t('admin.imageCanvas.moveDown')" :aria-label="t('admin.imageCanvas.moveDown')" @click="move(index, 1)"><Icon name="arrowDown" size="sm" /></button>
                    <button type="button" class="icon-button text-rose-500 hover:text-rose-700 dark:text-rose-400" :title="t('common.delete')" :aria-label="t('common.delete')" @click="remove(index)"><Icon name="trash" size="sm" /></button>
                  </div>
                </td>
              </tr>
              <tr v-if="draft.length === 0">
                <td colspan="7" class="px-4 py-12 text-center text-sm text-gray-500 dark:text-gray-400">{{ t('admin.imageCanvas.noModels') }}</td>
              </tr>
            </tbody>
          </table>
        </div>

        <div class="flex flex-wrap items-end gap-3 border-t border-gray-200 bg-gray-50/70 px-4 py-4 dark:border-dark-700 dark:bg-dark-950/50 sm:px-5">
          <label class="min-w-[240px] flex-1">
            <span class="input-label">{{ t('admin.imageCanvas.addModel') }}</span>
            <select v-model="selectedToAdd" class="input">
              <option value="">{{ t('admin.imageCanvas.selectModel') }}</option>
              <option v-for="item in availableOnly" :key="item.model" :value="item.model">{{ item.model }}</option>
            </select>
          </label>
          <button type="button" class="btn btn-secondary btn-sm" :disabled="!selectedToAdd" @click="addSelected">
            <Icon name="plus" size="sm" /> {{ t('admin.imageCanvas.addToChain') }}
          </button>
          <p v-if="!hasEnabledModel" class="basis-full text-sm text-rose-600 dark:text-rose-400">{{ t('admin.imageCanvas.atLeastOne') }}</p>
          <p v-else-if="hasInvalidEnabledModel" class="basis-full text-sm text-amber-700 dark:text-amber-300">{{ t('admin.imageCanvas.disableUnavailableModels') }}</p>
        </div>
      </section>

      <section class="border border-sky-200 bg-sky-50/70 px-4 py-4 dark:border-sky-900/60 dark:bg-sky-950/20 sm:px-5">
        <div class="flex items-start gap-3">
          <Icon name="infoCircle" size="sm" class="mt-0.5 shrink-0 text-sky-600 dark:text-sky-300" />
          <div class="min-w-0">
            <h3 class="font-medium text-sky-900 dark:text-sky-100">{{ t('admin.imageCanvas.fallbackTitle') }}</h3>
            <p class="mt-1 text-sm text-sky-800/80 dark:text-sky-200/80">{{ t('admin.imageCanvas.fallbackDescription') }}</p>
            <div class="mt-3 grid gap-2 text-xs text-sky-800 dark:text-sky-200 sm:grid-cols-2 lg:grid-cols-3">
              <span class="inline-flex items-center gap-2"><Icon name="check" size="xs" /> {{ t('admin.imageCanvas.fallbackSelectedFirst') }}</span>
              <span class="inline-flex items-center gap-2"><Icon name="check" size="xs" /> {{ t('admin.imageCanvas.fallbackSameProvider') }}</span>
              <span class="inline-flex items-center gap-2"><Icon name="check" size="xs" /> {{ t('admin.imageCanvas.fallbackRetryable') }}</span>
              <span class="inline-flex items-center gap-2"><Icon name="check" size="xs" /> {{ t('admin.imageCanvas.fallbackAccountFirst') }}</span>
              <span class="inline-flex items-center gap-2"><Icon name="x" size="xs" /> {{ t('admin.imageCanvas.fallbackPolicyNo') }}</span>
            </div>
          </div>
        </div>
      </section>
    </template>

    <section v-if="audits.length" class="border border-gray-200 bg-white dark:border-dark-700 dark:bg-dark-900">
      <div class="border-b border-gray-200 px-4 py-3 dark:border-dark-700 sm:px-5">
        <h3 class="font-medium text-gray-900 dark:text-white">{{ t('admin.imageCanvas.auditTitle') }}</h3>
      </div>
      <div class="overflow-x-auto">
        <table class="w-full min-w-[620px] text-left text-sm">
          <thead class="bg-gray-50 text-xs uppercase text-gray-500 dark:bg-dark-950 dark:text-gray-400">
            <tr>
              <th class="px-4 py-3">{{ t('admin.imageCanvas.auditVersion') }}</th>
              <th class="px-4 py-3">{{ t('admin.imageCanvas.auditOperator') }}</th>
              <th class="px-4 py-3">{{ t('admin.imageCanvas.auditChange') }}</th>
              <th class="px-4 py-3 text-right">{{ t('admin.imageCanvas.auditTime') }}</th>
            </tr>
          </thead>
          <tbody class="divide-y divide-gray-100 dark:divide-dark-800">
            <tr v-for="audit in audits" :key="audit.id">
              <td class="px-4 py-3 font-mono text-xs text-gray-700 dark:text-gray-200">v{{ audit.old_version }} <span class="text-gray-400">-&gt;</span> v{{ audit.new_version }}</td>
              <td class="px-4 py-3 text-gray-600 dark:text-gray-300">#{{ audit.operator_user_id }}</td>
              <td class="max-w-[360px] truncate px-4 py-3 text-xs text-gray-500 dark:text-gray-400" :title="auditSummary(audit)">{{ auditSummary(audit) }}</td>
              <td class="whitespace-nowrap px-4 py-3 text-right text-xs text-gray-500 dark:text-gray-400">{{ formatDate(audit.created_at) }}</td>
            </tr>
          </tbody>
        </table>
      </div>
    </section>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import Icon from '@/components/icons/Icon.vue'
import { useAppStore } from '@/stores/app'
import {
  checkImageCanvasModel,
  getImageCanvasPolicy,
  listImageCanvasPolicyAudit,
  updateImageCanvasPolicy,
  type ImageCanvasCatalogEntry,
  type ImageCanvasPolicy,
  type ImageCanvasPolicyAudit,
  type ImageCanvasPolicyItem
} from '@/api/admin/imageCanvas'

const { t, locale } = useI18n()
const emit = defineEmits<{ saved: [] }>()
const appStore = useAppStore()
const policy = ref<ImageCanvasPolicy | null>(null)
const draft = ref<ImageCanvasPolicyItem[]>([])
const draftEnabled = ref(false)
const audits = ref<ImageCanvasPolicyAudit[]>([])
const loading = ref(false)
const saving = ref(false)
const original = ref('')
const selectedToAdd = ref('')
const checkingModel = ref('')
const checks = ref<Record<string, 'ok' | 'warning' | 'fail' | 'loading'>>({})

const catalog = computed<ImageCanvasCatalogEntry[]>(() => policy.value?.catalog || policy.value?.available_models.map((item) => ({
  model: item.model,
  provider: item.capability?.provider,
  media_kind: item.capability?.media_kind,
  capability: item.capability,
  coverage: { account_count: 0, group_count: 0, api_key_count: 0 },
  schedulable: true
})) || [])
const catalogMap = computed(() => new Map(catalog.value.map((item) => [item.model, item])))
const availableOnly = computed(() => catalog.value.filter((item) => item.schedulable && !draft.value.some((draftItem) => draftItem.model === item.model)).map((item) => ({
  model: item.model,
  enabled: true,
  position: draft.value.length,
  capability: item.capability
})))
const dirty = computed(() => JSON.stringify({ enabled: draftEnabled.value, models: draft.value.map(({ model, enabled }, position) => ({ model, enabled, position })) }) !== original.value)
const hasEnabledModel = computed(() => draft.value.some((item) => item.enabled && catalogFor(item.model)?.schedulable === true))
const hasInvalidEnabledModel = computed(() => draft.value.some((item) => item.enabled && catalogFor(item.model)?.schedulable !== true))

function snapshot() {
  return JSON.stringify({ enabled: draftEnabled.value, models: draft.value.map(({ model, enabled }, position) => ({ model, enabled, position })) })
}

function catalogFor(model: string): ImageCanvasCatalogEntry | undefined {
  return catalogMap.value.get(model)
}

function coverageFor(model: string) {
  return catalogFor(model)?.coverage || { account_count: 0, group_count: 0, api_key_count: 0 }
}

function mediaLabel(kind?: string): string {
  if (kind === 'video') return t('admin.imageCanvas.video')
  if (kind === 'audio') return t('admin.imageCanvas.audio')
  return t('admin.imageCanvas.image')
}

function reasonLabel(reason?: string): string {
  if (reason === 'group_model_filter') return t('admin.imageCanvas.reasonGroupFilter')
  if (reason === 'no_eligible_group') return t('admin.imageCanvas.reasonNoGroup')
  if (reason === 'unknown_model') return t('admin.imageCanvas.reasonUnknown')
  return t('admin.imageCanvas.reasonNoAccount')
}

function checkClass(status: 'ok' | 'warning' | 'fail' | 'loading'): string {
  if (status === 'ok') return 'text-emerald-600 dark:text-emerald-400'
  if (status === 'warning') return 'text-amber-600 dark:text-amber-300'
  return 'text-rose-600 dark:text-rose-400'
}

function checkLabel(status: 'ok' | 'warning' | 'fail' | 'loading'): string {
  if (status === 'ok') return t('admin.imageCanvas.checkPassed')
  if (status === 'warning') return t('admin.imageCanvas.checkPolicyBlocked')
  return t('admin.imageCanvas.checkFailed')
}

async function load() {
  loading.value = true
  try {
    policy.value = await getImageCanvasPolicy()
    draft.value = policy.value.models.map((item) => ({ ...item }))
    draftEnabled.value = policy.value.enabled
    try {
      audits.value = await listImageCanvasPolicyAudit()
    } catch {
      audits.value = []
    }
    checks.value = {}
    selectedToAdd.value = ''
    original.value = snapshot()
  } catch (error) {
    appStore.showError(error instanceof Error ? error.message : t('common.error'))
  } finally {
    loading.value = false
  }
}

function move(index: number, delta: number) {
  const next = index + delta
  if (next < 0 || next >= draft.value.length) return
  const [item] = draft.value.splice(index, 1)
  draft.value.splice(next, 0, item)
}

function addSelected() {
  const item = availableOnly.value.find((candidate) => candidate.model === selectedToAdd.value)
  if (!item) return
  draft.value.push({ ...item, enabled: true, position: draft.value.length })
  selectedToAdd.value = ''
}

function remove(index: number) {
  const [removed] = draft.value.splice(index, 1)
  if (removed) {
    const nextChecks = { ...checks.value }
    delete nextChecks[removed.model]
    checks.value = nextChecks
  }
}

async function checkModel(model: string) {
  checkingModel.value = model
  checks.value = { ...checks.value, [model]: 'loading' }
  try {
    const result = await checkImageCanvasModel(model)
    const status = result.eligible ? 'ok' : (result.schedulable ? 'warning' : 'fail')
    checks.value = { ...checks.value, [model]: status }
    if (!result.schedulable && result.schedulability_reason) appStore.showWarning(reasonLabel(result.schedulability_reason))
    else if (!result.eligible) appStore.showWarning(t('admin.imageCanvas.checkPolicyBlocked'))
  } catch (error) {
    checks.value = { ...checks.value, [model]: 'fail' }
    appStore.showError(error instanceof Error ? error.message : t('common.error'))
  } finally {
    checkingModel.value = ''
  }
}

async function save() {
  if (!policy.value || !hasEnabledModel.value || hasInvalidEnabledModel.value) return
  saving.value = true
  try {
    policy.value = await updateImageCanvasPolicy({
      version: policy.value.version,
      enabled: draftEnabled.value,
      models: draft.value.map((item, position) => ({ model: item.model, enabled: item.enabled, position }))
    })
    draft.value = policy.value.models.map((item) => ({ ...item }))
    draftEnabled.value = policy.value.enabled
    original.value = snapshot()
    try {
      audits.value = await listImageCanvasPolicyAudit()
    } catch {
      // Saving the policy succeeded; a stale audit table must not turn that
      // success into a false failure notification.
    }
    emit('saved')
    appStore.showSuccess(t('common.saved'))
  } catch (error) {
    const status = (error as { status?: number }).status
    if (status === 409) appStore.showWarning(t('admin.imageCanvas.conflict'))
    else appStore.showError(error instanceof Error ? error.message : t('common.error'))
    await load()
  } finally {
    saving.value = false
  }
}

function auditSummary(audit: ImageCanvasPolicyAudit): string {
  const before = policySnapshot(audit.before_value)
  const after = policySnapshot(audit.after_value)
  const changes: string[] = []
  if (before?.enabled !== after?.enabled && typeof after?.enabled === 'boolean') {
    changes.push(t('admin.imageCanvas.auditSwitchChanged', { state: after.enabled ? t('common.enabled') : t('common.disabled') }))
  }
  const beforeModels = before?.models || []
  const afterModels = after?.models || []
  if (JSON.stringify(beforeModels) !== JSON.stringify(afterModels)) {
    changes.push(t('admin.imageCanvas.auditModelsChanged', { before: beforeModels.length, after: afterModels.length }))
  }
  return changes.join(' · ') || t('admin.imageCanvas.auditVersionOnly')
}

function policySnapshot(value: unknown): { enabled?: boolean; models?: unknown[] } | null {
  if (!value || typeof value !== 'object') return null
  const snapshot = value as { enabled?: unknown; models?: unknown }
  return {
    enabled: typeof snapshot.enabled === 'boolean' ? snapshot.enabled : undefined,
    models: Array.isArray(snapshot.models) ? snapshot.models : []
  }
}

function formatDate(value: string) {
  return new Intl.DateTimeFormat(locale.value, { dateStyle: 'medium', timeStyle: 'short' }).format(new Date(value))
}

onMounted(load)

defineExpose({ load })
</script>
