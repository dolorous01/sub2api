<template>
  <BaseDialog
    :show="show"
    :title="t('deployment.title')"
    width="extra-wide"
    :close-on-click-outside="false"
    @close="emit('close')"
  >
    <div class="space-y-5">
      <div class="flex flex-col gap-3 border-b border-gray-100 pb-4 dark:border-dark-700 sm:flex-row sm:items-center sm:justify-between">
        <div class="min-w-0">
          <p class="text-sm text-gray-600 dark:text-dark-300">{{ t('deployment.subtitle') }}</p>
          <p class="mt-1 flex items-center gap-1.5 text-xs text-emerald-700 dark:text-emerald-400">
            <Icon name="sync" size="sm" :stroke-width="2" />
            {{ t('deployment.automaticSync') }}
          </p>
        </div>
        <button
          type="button"
          class="btn btn-secondary btn-sm flex-shrink-0"
          :disabled="loading"
          :title="t('deployment.refresh')"
          @click="loadStatus(true)"
        >
          <Icon name="refresh" size="sm" :stroke-width="2" :class="{ 'animate-spin': loading }" />
          <span>{{ t('deployment.refresh') }}</span>
        </button>
      </div>

      <div
        v-if="pendingAction"
        class="flex flex-col gap-3 border border-amber-300 bg-amber-50 p-4 dark:border-amber-700 dark:bg-amber-900/20 sm:flex-row sm:items-center sm:justify-between"
      >
        <div class="min-w-0">
          <p class="text-sm font-semibold text-amber-900 dark:text-amber-200">
            {{ t('deployment.confirmTitle') }}
          </p>
          <p class="mt-1 text-xs leading-5 text-amber-800 dark:text-amber-300">
            {{ confirmationMessage }}
          </p>
        </div>
        <div class="flex flex-shrink-0 justify-end gap-2">
          <button type="button" class="btn btn-secondary btn-sm" :disabled="starting" @click="pendingAction = null">
            {{ t('common.cancel') }}
          </button>
          <button
            type="button"
            class="btn btn-primary btn-sm"
            data-testid="confirm-deployment"
            :disabled="starting"
            @click="confirmAction"
          >
            <Icon
              :name="pendingAction.action === 'update' ? 'download' : 'clock'"
              size="sm"
              :stroke-width="2"
              :class="{ 'animate-pulse': starting }"
            />
            {{ starting ? t('deployment.starting') : t('common.confirm') }}
          </button>
        </div>
      </div>

      <div
        v-if="loadError"
        class="flex items-start gap-3 border border-red-200 bg-red-50 p-4 dark:border-red-800 dark:bg-red-900/20"
      >
        <Icon name="exclamationCircle" size="md" :stroke-width="2" class="mt-0.5 flex-shrink-0 text-red-600 dark:text-red-400" />
        <div class="min-w-0">
          <p class="text-sm font-medium text-red-800 dark:text-red-200">{{ t('deployment.loadFailed') }}</p>
          <p class="mt-1 break-words text-xs text-red-700 dark:text-red-300">{{ loadError }}</p>
        </div>
      </div>

      <div v-if="loading && !status" class="flex min-h-56 items-center justify-center">
        <Icon name="refresh" size="lg" :stroke-width="2" class="animate-spin text-primary-500" />
      </div>

      <div v-else-if="status" class="grid grid-cols-1 gap-4 md:grid-cols-2">
        <section
          v-for="component in components"
          :key="component.component"
          :data-component="component.component"
          class="flex min-w-0 flex-col rounded-lg border border-gray-200 bg-white p-5 dark:border-dark-600 dark:bg-dark-800"
        >
          <div class="flex items-start justify-between gap-3">
            <div class="flex min-w-0 items-center gap-3">
              <span
                class="flex h-10 w-10 flex-shrink-0 items-center justify-center rounded-lg"
                :class="component.component === 'sub2api' ? 'bg-blue-50 text-blue-600 dark:bg-blue-900/30 dark:text-blue-400' : 'bg-emerald-50 text-emerald-600 dark:bg-emerald-900/30 dark:text-emerald-400'"
              >
                <Icon :name="component.component === 'sub2api' ? 'server' : 'grid'" size="lg" :stroke-width="1.8" />
              </span>
              <div class="min-w-0">
                <h3 class="truncate text-base font-semibold text-gray-900 dark:text-white">
                  {{ componentName(component.component) }}
                </h3>
                <p class="mt-0.5 text-xs text-gray-500 dark:text-dark-400">
                  {{ deploymentMode(component) }}
                </p>
              </div>
            </div>
            <span
              class="inline-flex flex-shrink-0 items-center gap-1.5 text-xs font-medium"
              :class="componentStatusClass(component)"
            >
              <span class="h-2 w-2 rounded-full bg-current"></span>
              {{ componentStatusLabel(component) }}
            </span>
          </div>

          <dl class="mt-5 grid grid-cols-2 border-y border-gray-100 py-4 dark:border-dark-700">
            <div class="min-w-0 border-r border-gray-100 pr-4 dark:border-dark-700">
              <dt class="text-xs text-gray-500 dark:text-dark-400">{{ t('deployment.currentVersion') }}</dt>
              <dd class="mt-1 truncate font-mono text-sm font-semibold text-gray-900 dark:text-white" :title="component.current_version || '--'">
                {{ versionLabel(component.current_version) }}
              </dd>
            </div>
            <div class="min-w-0 pl-4">
              <dt class="text-xs text-gray-500 dark:text-dark-400">{{ t('deployment.approvedVersion') }}</dt>
              <dd class="mt-1 truncate font-mono text-sm font-semibold text-gray-900 dark:text-white" :title="component.target_version || '--'">
                {{ versionLabel(component.target_version) }}
              </dd>
            </div>
          </dl>

          <div class="min-h-24 flex-1 py-4">
            <OperationStatus
              v-if="component.last_operation"
              :operation="component.last_operation"
              :now="now"
            />
            <p v-else class="text-sm text-gray-500 dark:text-dark-400">
              {{ t('deployment.noOperations') }}
            </p>
          </div>

          <p
            v-if="component.update_block_reason && component.update_block_reason !== 'already_up_to_date'"
            class="mb-3 flex items-start gap-2 text-xs leading-5 text-amber-700 dark:text-amber-400"
          >
            <Icon name="infoCircle" size="sm" :stroke-width="2" class="mt-0.5 flex-shrink-0" />
            {{ blockReason(component.update_block_reason) }}
          </p>

          <div class="flex flex-wrap items-center gap-2 border-t border-gray-100 pt-4 dark:border-dark-700">
            <button
              type="button"
              class="btn btn-primary btn-sm"
              data-action="update"
              :disabled="!component.update_enabled || hasRunningOperation"
              @click="askForAction(component.component, 'update')"
            >
              <Icon name="download" size="sm" :stroke-width="2" />
              {{ t('deployment.update') }}
            </button>
            <button
              type="button"
              class="btn btn-secondary btn-sm"
              data-action="rollback"
              :disabled="!component.rollback_enabled || hasRunningOperation"
              @click="askForAction(component.component, 'rollback')"
            >
              <Icon name="clock" size="sm" :stroke-width="2" />
              {{ t('deployment.rollback') }}
            </button>
            <a
              v-if="component.release_url"
              :href="component.release_url"
              target="_blank"
              rel="noopener noreferrer"
              class="ml-auto inline-flex items-center gap-1 text-xs text-gray-500 hover:text-gray-800 dark:text-dark-400 dark:hover:text-dark-200"
            >
              {{ t('deployment.releaseNotes') }}
              <Icon name="externalLink" size="xs" :stroke-width="2" />
            </a>
          </div>
        </section>
      </div>
    </div>
  </BaseDialog>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import {
  getDeploymentOperation,
  getDeploymentStatus,
  startDeploymentAction,
  type DeploymentAction,
  type DeploymentComponent,
  type DeploymentComponentStatus,
  type DeploymentStatus
} from '@/api/admin/deployments'
import BaseDialog from '@/components/common/BaseDialog.vue'
import OperationStatus from '@/components/common/DeploymentOperationStatus.vue'
import Icon from '@/components/icons/Icon.vue'

const props = defineProps<{ show: boolean }>()
const emit = defineEmits<{ (event: 'close'): void }>()
const { t } = useI18n()

const status = ref<DeploymentStatus | null>(null)
const loading = ref(false)
const starting = ref(false)
const loadError = ref('')
const pendingAction = ref<{ component: DeploymentComponent; action: DeploymentAction } | null>(null)
const now = ref(Date.now())
let pollTimer: ReturnType<typeof setTimeout> | null = null
let clockTimer: ReturnType<typeof setInterval> | null = null

const components = computed(() => {
  if (!status.value) return []
  return [status.value.components.sub2api, status.value.components.canvas]
})

const hasRunningOperation = computed(() => {
  const state = status.value?.active_operation?.state
  return state === 'queued' || state === 'running'
})

const confirmationMessage = computed(() => {
  if (!pendingAction.value) return ''
  const name = componentName(pendingAction.value.component)
  return pendingAction.value.action === 'update'
    ? t('deployment.confirmUpdate', { name })
    : t('deployment.confirmRollback', { name })
})

function versionLabel(value?: string | null): string {
  return value ? `v${value}` : '--'
}

function componentName(component: DeploymentComponent): string {
  return component === 'sub2api' ? t('deployment.sub2api') : t('deployment.canvas')
}

function deploymentMode(component: DeploymentComponentStatus): string {
  return component.deployment_mode === 'blue_green'
    ? t('deployment.blueGreenMode')
    : t('deployment.stableMode')
}

function componentStatusLabel(component: DeploymentComponentStatus): string {
  const operation = component.last_operation
  if (operation?.state === 'running' || operation?.state === 'queued') return t('deployment.status.running')
  if (operation?.state === 'failed' || operation?.state === 'interrupted') return t('deployment.status.failed')
  if (component.update_available) return t('deployment.status.available')
  if (component.update_block_reason === 'already_up_to_date') return t('deployment.status.current')
  return t('deployment.status.blocked')
}

function componentStatusClass(component: DeploymentComponentStatus): string {
  const operation = component.last_operation
  if (operation?.state === 'running' || operation?.state === 'queued') return 'text-blue-600 dark:text-blue-400'
  if (operation?.state === 'failed' || operation?.state === 'interrupted') return 'text-red-600 dark:text-red-400'
  if (component.update_available) return 'text-amber-600 dark:text-amber-400'
  if (component.update_block_reason === 'already_up_to_date') return 'text-emerald-600 dark:text-emerald-400'
  return 'text-gray-500 dark:text-dark-400'
}

function blockReason(reason: string): string {
  const knownReasons = new Set([
    'canvas_stable_required',
    'release_not_approved',
    'deployment_in_progress',
    'rollback_not_available'
  ])
  return knownReasons.has(reason)
    ? t(`deployment.blockReasons.${reason}`)
    : t('deployment.blockReasons.unknown')
}

function errorMessage(error: unknown): string {
  const value = error as {
    reason?: string
    message?: string
    response?: { data?: { reason?: string; message?: string } }
  }
  const reason = value.reason || value.response?.data?.reason
  const knownReasons = new Set([
    'deployment_operator_disabled',
    'admin_auth_unavailable',
    'admin_access_required',
    'canvas_stable_required',
    'release_not_approved',
    'deployment_in_progress',
    'rollback_not_available',
    'deployment_command_failed',
    'reconciliation_failed',
    'reconciliation_report_missing',
    'operator_restarted'
  ])
  if (reason && knownReasons.has(reason)) return t(`deployment.errors.${reason}`)
  return value.message || value.response?.data?.message || t('deployment.errors.unknown')
}

function clearPolling(): void {
  if (pollTimer) clearTimeout(pollTimer)
  pollTimer = null
}

function schedulePoll(operationId: string): void {
  clearPolling()
  pollTimer = setTimeout(() => pollOperation(operationId), 1500)
}

async function pollOperation(operationId: string): Promise<void> {
  if (!props.show) return
  try {
    const operation = await getDeploymentOperation(operationId)
    if (status.value) {
      status.value.active_operation = ['queued', 'running'].includes(operation.state) ? operation : null
      status.value.components[operation.component].last_operation = operation
    }
    if (operation.state === 'queued' || operation.state === 'running') {
      schedulePoll(operationId)
    } else {
      await loadStatus(false)
    }
  } catch (error) {
    loadError.value = errorMessage(error)
    schedulePoll(operationId)
  }
}

async function loadStatus(showLoading: boolean): Promise<void> {
  if (showLoading) loading.value = true
  loadError.value = ''
  try {
    status.value = await getDeploymentStatus()
    const active = status.value.active_operation
    if (active && (active.state === 'queued' || active.state === 'running')) schedulePoll(active.id)
  } catch (error) {
    loadError.value = errorMessage(error)
  } finally {
    loading.value = false
  }
}

function askForAction(component: DeploymentComponent, action: DeploymentAction): void {
  pendingAction.value = { component, action }
}

async function confirmAction(): Promise<void> {
  if (!pendingAction.value || starting.value) return
  starting.value = true
  loadError.value = ''
  const request = pendingAction.value
  try {
    const operation = await startDeploymentAction(request.component, request.action)
    pendingAction.value = null
    if (status.value) {
      status.value.active_operation = operation
      status.value.components[operation.component].last_operation = operation
    }
    schedulePoll(operation.id)
  } catch (error) {
    loadError.value = errorMessage(error)
  } finally {
    starting.value = false
  }
}

watch(
  () => props.show,
  (open) => {
    if (open) {
      now.value = Date.now()
      clockTimer = setInterval(() => {
        now.value = Date.now()
      }, 30_000)
      loadStatus(true)
    } else {
      pendingAction.value = null
      clearPolling()
      if (clockTimer) clearInterval(clockTimer)
      clockTimer = null
    }
  },
  { immediate: true }
)

onBeforeUnmount(() => {
  clearPolling()
  if (clockTimer) clearInterval(clockTimer)
})
</script>
