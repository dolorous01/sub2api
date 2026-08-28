<template>
  <section class="border border-gray-200 bg-white dark:border-dark-700 dark:bg-dark-900">
    <div class="flex flex-wrap items-end justify-between gap-3 border-b border-gray-200 px-4 py-4 dark:border-dark-700 sm:px-5">
      <div>
        <h2 class="text-lg font-semibold text-gray-900 dark:text-white">{{ t('admin.imageCanvas.recentJobsTitle') }}</h2>
        <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">{{ t('admin.imageCanvas.recentJobsDescription') }}</p>
      </div>
      <button type="button" class="icon-button" :disabled="loading" :title="t('common.refresh')" :aria-label="t('common.refresh')" @click="load">
        <Icon name="refresh" size="sm" :class="loading ? 'animate-spin' : ''" />
      </button>
    </div>

    <div v-if="loading && !jobs.length" class="flex justify-center py-12">
      <span class="h-7 w-7 animate-spin rounded-full border-2 border-primary-500 border-t-transparent" />
    </div>
    <div v-else-if="!jobs.length" class="px-4 py-14 text-center text-sm text-gray-500 dark:text-gray-400">
      <Icon name="inbox" size="lg" class="mx-auto mb-3 text-gray-300 dark:text-dark-600" />
      {{ t('admin.imageCanvas.noRecentJobs') }}
    </div>
    <template v-else>
      <div class="hidden overflow-x-auto md:block">
        <table class="w-full min-w-[980px] text-left text-sm">
          <thead class="bg-gray-50 text-xs uppercase text-gray-500 dark:bg-dark-950 dark:text-gray-400">
            <tr>
              <th class="w-10 px-4 py-3" />
              <th class="px-3 py-3">{{ t('admin.imageCanvas.job') }}</th>
              <th class="px-3 py-3">{{ t('admin.imageCanvas.jobStatus') }}</th>
              <th class="px-3 py-3">{{ t('admin.imageCanvas.jobModel') }}</th>
              <th class="px-3 py-3">{{ t('admin.imageCanvas.jobAttempts') }}</th>
              <th class="px-3 py-3">{{ t('admin.imageCanvas.jobOutput') }}</th>
              <th class="px-4 py-3 text-right">{{ t('admin.imageCanvas.jobCreated') }}</th>
            </tr>
          </thead>
          <tbody class="divide-y divide-gray-100 dark:divide-dark-800">
            <template v-for="job in jobs" :key="job.id">
              <tr class="hover:bg-gray-50/80 dark:hover:bg-dark-800/50">
                <td class="px-4 py-3">
                  <button type="button" class="icon-button" :title="isExpanded(job.id) ? t('admin.imageCanvas.collapseAttempts') : t('admin.imageCanvas.expandAttempts')" :aria-label="isExpanded(job.id) ? t('admin.imageCanvas.collapseAttempts') : t('admin.imageCanvas.expandAttempts')" @click="toggle(job.id)">
                    <Icon :name="isExpanded(job.id) ? 'chevronUp' : 'chevronDown'" size="sm" />
                  </button>
                </td>
                <td class="px-3 py-3">
                  <div class="font-mono text-xs text-gray-700 dark:text-gray-200">{{ shortID(job.id) }}</div>
                  <div class="mt-1 whitespace-nowrap text-xs text-gray-500 dark:text-gray-400">{{ job.operation }}<span v-if="job.mode"> / {{ job.mode }}</span> · U#{{ job.user_id }} · K#{{ job.api_key_id }} · G#{{ job.group_id }}</div>
                </td>
                <td class="px-3 py-3"><StatusBadge :status="job.status" /></td>
                <td class="max-w-[210px] px-3 py-3">
                  <div class="truncate font-medium text-gray-800 dark:text-gray-100" :title="job.requested_model">{{ job.requested_model }}</div>
                  <div v-if="job.successful_model && job.successful_model !== job.requested_model" class="mt-1 inline-flex items-center gap-1 text-xs text-emerald-600 dark:text-emerald-400"><Icon name="swap" size="xs" />{{ job.successful_model }}</div>
                </td>
                <td class="px-3 py-3 text-xs text-gray-600 dark:text-gray-300">{{ job.attempt_log.length || job.attempt_plan.length }} / {{ job.attempt_plan.length || '-' }}</td>
                <td class="px-3 py-3 text-xs text-gray-600 dark:text-gray-300">{{ job.completed_count }} / {{ job.requested_count }}<span v-if="job.duration_ms" class="ml-2 text-gray-400">{{ formatDuration(job.duration_ms) }}</span></td>
                <td class="whitespace-nowrap px-4 py-3 text-right text-xs text-gray-500 dark:text-gray-400">{{ formatDate(job.created_at) }}</td>
              </tr>
              <tr v-if="isExpanded(job.id)" class="bg-gray-50/70 dark:bg-dark-950/50">
                <td colspan="7" class="px-4 py-4 sm:px-12">
                  <div class="grid gap-4 lg:grid-cols-[minmax(0,1fr)_minmax(260px,0.8fr)]">
                    <div>
                      <div class="mb-2 text-xs font-semibold uppercase text-gray-500 dark:text-gray-400">{{ t('admin.imageCanvas.attemptPlan') }}</div>
                      <div class="flex flex-wrap items-center gap-2">
                        <template v-for="(model, index) in job.attempt_plan" :key="`${job.id}-${model}-${index}`">
                          <span class="inline-flex items-center gap-1 rounded bg-white px-2.5 py-1.5 font-mono text-xs text-gray-700 shadow-sm ring-1 ring-gray-200 dark:bg-dark-900 dark:text-gray-200 dark:ring-dark-700"><span class="text-gray-400">{{ index + 1 }}</span>{{ model }}</span>
                          <Icon v-if="index < job.attempt_plan.length - 1" name="arrowRight" size="xs" class="text-gray-300 dark:text-dark-600" />
                        </template>
                        <span v-if="!job.attempt_plan.length" class="text-xs text-gray-500">-</span>
                      </div>
                    </div>
                    <div>
                      <div class="mb-2 text-xs font-semibold uppercase text-gray-500 dark:text-gray-400">{{ t('admin.imageCanvas.attemptLog') }}</div>
                      <ol v-if="job.attempt_log.length" class="space-y-2">
                        <li v-for="attempt in job.attempt_log" :key="`${job.id}-${attempt.position}-${attempt.started_at}`" class="flex items-start justify-between gap-3 text-xs">
                          <span class="min-w-0 truncate font-mono text-gray-700 dark:text-gray-200">{{ attempt.position + 1 }}. {{ attempt.model }}</span>
                          <span class="shrink-0 text-gray-500 dark:text-gray-400">{{ formatDuration(attempt.latency_ms) }} · {{ attempt.final_count }}</span>
                          <span v-if="attempt.error_class" class="shrink-0 text-rose-600 dark:text-rose-400">{{ attempt.error_class }}</span>
                        </li>
                      </ol>
                      <span v-else class="text-xs text-gray-500 dark:text-gray-400">{{ t('admin.imageCanvas.noAttempts') }}</span>
                    </div>
                  </div>
                  <div v-if="job.error" class="mt-4 flex items-start gap-2 border-l-2 border-rose-400 pl-3 text-xs text-rose-700 dark:text-rose-300"><Icon name="exclamationTriangle" size="xs" class="mt-0.5 shrink-0" /><span>{{ job.error.code }}: {{ job.error.message }}</span></div>
                  <div class="mt-4 flex flex-wrap gap-x-5 gap-y-1 border-t border-gray-200 pt-3 font-mono text-[11px] text-gray-500 dark:border-dark-700 dark:text-gray-400">
                    <span>user={{ job.user_id }}</span>
                    <span>api_key={{ job.api_key_id }}</span>
                    <span>group={{ job.group_id }}</span>
                    <span v-if="job.project_id">project={{ job.project_id }}</span>
                    <span v-if="job.policy_version">policy=v{{ job.policy_version }}</span>
                    <span v-if="job.execution_phase">phase={{ job.execution_phase }}</span>
                  </div>
                </td>
              </tr>
            </template>
          </tbody>
        </table>
      </div>

      <div class="divide-y divide-gray-100 md:hidden dark:divide-dark-800">
        <article v-for="job in jobs" :key="job.id" class="px-4 py-4">
          <div class="flex items-start justify-between gap-3">
            <div class="min-w-0">
              <div class="truncate font-mono text-xs text-gray-700 dark:text-gray-200">{{ shortID(job.id) }}</div>
              <div class="mt-1 truncate font-medium text-gray-900 dark:text-white">{{ job.requested_model }}</div>
              <div class="mt-1 text-xs text-gray-500 dark:text-gray-400">{{ formatDate(job.created_at) }} · U#{{ job.user_id }} · K#{{ job.api_key_id }} · G#{{ job.group_id }}</div>
            </div>
            <StatusBadge :status="job.status" />
          </div>
          <div class="mt-3 flex items-center justify-between text-xs text-gray-600 dark:text-gray-300">
            <span>{{ job.completed_count }} / {{ job.requested_count }} {{ t('admin.imageCanvas.outputsShort') }}</span>
            <button type="button" class="inline-flex items-center gap-1 text-primary-600 dark:text-primary-300" @click="toggle(job.id)">
              <Icon :name="isExpanded(job.id) ? 'chevronUp' : 'chevronDown'" size="xs" /> {{ isExpanded(job.id) ? t('admin.imageCanvas.hideAttempts') : t('admin.imageCanvas.viewAttempts') }}
            </button>
          </div>
          <div v-if="isExpanded(job.id)" class="mt-3 space-y-3 border-t border-gray-100 pt-3 dark:border-dark-800">
            <div class="text-xs text-gray-600 dark:text-gray-300"><span class="font-medium">{{ t('admin.imageCanvas.attemptPlan') }}:</span> {{ job.attempt_plan.join(' -> ') || '-' }}</div>
            <ol v-if="job.attempt_log.length" class="space-y-1.5 text-xs text-gray-600 dark:text-gray-300">
              <li v-for="attempt in job.attempt_log" :key="`${job.id}-${attempt.position}-${attempt.started_at}`">{{ attempt.position + 1 }}. {{ attempt.model }} · {{ formatDuration(attempt.latency_ms) }}<span v-if="attempt.error_class" class="text-rose-600"> · {{ attempt.error_class }}</span></li>
            </ol>
          </div>
        </article>
      </div>
    </template>
  </section>
</template>

<script setup lang="ts">
import { defineComponent, h, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import Icon from '@/components/icons/Icon.vue'
import { listImageCanvasRecentJobs, type ImageCanvasRecentJob } from '@/api/admin/imageCanvas'
import { useAppStore } from '@/stores/app'

const { t, locale } = useI18n()
const appStore = useAppStore()
const jobs = ref<ImageCanvasRecentJob[]>([])
const expanded = ref<Set<string>>(new Set())
const loading = ref(false)

const StatusBadge = defineComponent({
  props: { status: { type: String, required: true } },
  setup(props) {
    return () => h('span', {
      class: ['inline-flex items-center rounded px-2 py-1 text-xs font-medium', statusClass(props.status)]
    }, statusLabel(props.status))
  }
})

function statusClass(status: string): string {
  if (status === 'completed') return 'bg-emerald-50 text-emerald-700 dark:bg-emerald-950/40 dark:text-emerald-300'
  if (status === 'partial') return 'bg-amber-50 text-amber-700 dark:bg-amber-950/40 dark:text-amber-300'
  if (status === 'failed' || status === 'indeterminate') return 'bg-rose-50 text-rose-700 dark:bg-rose-950/40 dark:text-rose-300'
  if (status === 'running') return 'bg-sky-50 text-sky-700 dark:bg-sky-950/40 dark:text-sky-300'
  return 'bg-gray-100 text-gray-600 dark:bg-dark-800 dark:text-gray-300'
}

function statusLabel(status: string): string {
  const key = `admin.imageCanvas.jobStatus_${status}`
  const translated = t(key)
  return translated === key ? status : translated
}

function isExpanded(id: string): boolean {
  return expanded.value.has(id)
}

function toggle(id: string) {
  const next = new Set(expanded.value)
  if (next.has(id)) next.delete(id)
  else next.add(id)
  expanded.value = next
}

async function load() {
  loading.value = true
  try {
    jobs.value = await listImageCanvasRecentJobs()
  } catch (error) {
    appStore.showError(error instanceof Error ? error.message : t('common.error'))
  } finally {
    loading.value = false
  }
}

function shortID(id: string): string {
  return id.length > 22 ? `${id.slice(0, 14)}...${id.slice(-6)}` : id
}

function formatDate(value: string): string {
  return new Intl.DateTimeFormat(locale.value, { dateStyle: 'short', timeStyle: 'short' }).format(new Date(value))
}

function formatDuration(milliseconds: number): string {
  if (!Number.isFinite(milliseconds) || milliseconds < 0) return '-'
  if (milliseconds < 1000) return `${Math.round(milliseconds)}ms`
  return `${(milliseconds / 1000).toFixed(1)}s`
}

onMounted(load)

defineExpose({ load })
</script>
