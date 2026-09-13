<template>
  <div class="space-y-2">
    <div class="flex items-center justify-between gap-3">
      <span class="flex min-w-0 items-center gap-2 text-sm font-medium text-gray-700 dark:text-dark-200">
        <Icon
          :name="operationIcon"
          size="sm"
          :stroke-width="2"
          :class="[operationClass, { 'animate-spin': isRunning }]"
        />
        <span class="truncate">{{ operationLabel }}</span>
      </span>
      <time v-if="operationTime" class="flex-shrink-0 text-xs text-gray-400 dark:text-dark-500">
        {{ operationTime }}
      </time>
    </div>

    <p v-if="operation.error" class="break-words text-xs leading-5 text-red-600 dark:text-red-400">
      {{ translatedError(operation.error.reason) }}
    </p>

    <div v-if="operation.action === 'update'" class="flex items-start gap-2 text-xs leading-5" :class="reconciliationClass">
      <Icon name="sync" size="sm" :stroke-width="2" class="mt-0.5 flex-shrink-0" />
      <span>{{ reconciliationLabel }}</span>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import type { DeploymentOperation } from '@/api/admin/deployments'
import Icon from '@/components/icons/Icon.vue'

const props = defineProps<{ operation: DeploymentOperation; now: number }>()
const { t, locale } = useI18n()

const isRunning = computed(() => props.operation.state === 'queued' || props.operation.state === 'running')
const operationIcon = computed(() => {
  if (isRunning.value) return 'refresh' as const
  if (props.operation.state === 'succeeded') return 'checkCircle' as const
  return 'exclamationCircle' as const
})
const operationClass = computed(() => {
  if (isRunning.value) return 'text-blue-500'
  if (props.operation.state === 'succeeded') return 'text-emerald-500'
  return 'text-red-500'
})
const operationLabel = computed(() => {
  const action = t(`deployment.actions.${props.operation.action}`)
  return isRunning.value
    ? t('deployment.operationRunning', { action })
    : t(`deployment.operationStates.${props.operation.state}`, { action })
})
const operationTime = computed(() => {
  const value = props.operation.finished_at || props.operation.started_at || props.operation.requested_at
  if (!value) return ''
  const parsed = new Date(value)
  if (Number.isNaN(parsed.getTime())) return ''
  void props.now
  return new Intl.DateTimeFormat(locale.value, {
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit'
  }).format(parsed)
})

const reconciliationClass = computed(() => {
  const state = props.operation.reconciliation?.state
  if (state === 'succeeded') return 'text-emerald-700 dark:text-emerald-400'
  if (state === 'failed' || state === 'not_run') return 'text-red-600 dark:text-red-400'
  return 'text-gray-500 dark:text-dark-400'
})

const reconciliationLabel = computed(() => {
  const reconciliation = props.operation.reconciliation
  if (!reconciliation || reconciliation.state === 'pending') return t('deployment.sync.pending')
  if (reconciliation.state === 'not_run') return t('deployment.sync.notRun')
  if (reconciliation.state === 'failed') return t('deployment.sync.failed')
  if (reconciliation.state === 'not_required') return t('deployment.sync.notRequired')
  if (reconciliation.status === 'attention_required') {
    const count =
      (reconciliation.active_but_unbound_count || 0) +
      (reconciliation.inactive_but_bound_count || 0) +
      (reconciliation.missing_from_official_count || 0)
    return t('deployment.sync.attention', { count })
  }
  return t('deployment.sync.ok')
})

function translatedError(reason: string): string {
  const known = new Set([
    'deployment_command_failed',
    'reconciliation_failed',
    'reconciliation_report_missing',
    'operator_restarted'
  ])
  return known.has(reason) ? t(`deployment.errors.${reason}`) : t('deployment.errors.unknown')
}
</script>
