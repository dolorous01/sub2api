<template>
  <AppLayout>
    <div class="w-full space-y-5">
      <header class="flex flex-wrap items-end justify-between gap-4">
        <div>
          <div class="flex items-center gap-2 text-xs font-medium uppercase text-primary-600 dark:text-primary-300">
            <Icon name="cube" size="sm" />
            {{ t('admin.imageCanvas.consoleLabel') }}
          </div>
          <h1 class="mt-2 text-2xl font-semibold text-gray-900 dark:text-white">{{ t('admin.imageCanvas.title') }}</h1>
          <p class="mt-1 max-w-3xl text-sm text-gray-500 dark:text-gray-400">{{ t('admin.imageCanvas.description') }}</p>
        </div>
        <div class="flex max-w-full flex-wrap items-center justify-end gap-2">
          <button type="button" class="icon-button" :disabled="refreshing" :title="t('common.refresh')" :aria-label="t('common.refresh')" @click="refreshAll">
            <Icon name="refresh" size="sm" :class="refreshing ? 'animate-spin' : ''" />
          </button>
          <RouterLink to="/admin/settings?tab=features" class="btn btn-secondary btn-sm">
            <Icon name="cog" size="sm" />
            {{ t('admin.imageCanvas.entrySettings') }}
          </RouterLink>
          <RouterLink to="/studio" class="btn btn-primary btn-sm">
            <Icon name="externalLink" size="sm" />
            {{ t('admin.imageCanvas.openStudio') }}
          </RouterLink>
        </div>
      </header>

      <section class="grid overflow-hidden border border-gray-200 bg-white shadow-sm dark:border-dark-700 dark:bg-dark-900 sm:grid-cols-2 xl:grid-cols-4">
        <div class="flex items-start gap-3 border-b border-gray-200 px-4 py-4 dark:border-dark-700 sm:border-r xl:border-b-0">
          <span class="flex h-9 w-9 shrink-0 items-center justify-center rounded-lg bg-amber-50 text-amber-600 dark:bg-amber-950/40 dark:text-amber-300"><Icon name="globe" size="sm" /></span>
          <div class="min-w-0">
            <div class="text-xs font-medium uppercase text-gray-500 dark:text-gray-400">{{ t('admin.imageCanvas.entrySwitch') }}</div>
            <div class="mt-1 font-semibold text-gray-900 dark:text-white">{{ entryState }}</div>
            <div class="mt-1 text-xs text-gray-500 dark:text-gray-400">{{ t('admin.imageCanvas.entrySwitchHint') }}</div>
          </div>
        </div>
        <div class="flex items-start gap-3 border-b border-gray-200 px-4 py-4 dark:border-dark-700 xl:border-b-0 xl:border-r">
          <span class="flex h-9 w-9 shrink-0 items-center justify-center rounded-lg bg-primary-50 text-primary-600 dark:bg-primary-950/40 dark:text-primary-300"><Icon name="shield" size="sm" /></span>
          <div class="min-w-0">
            <div class="text-xs font-medium uppercase text-gray-500 dark:text-gray-400">{{ t('admin.imageCanvas.modelPolicySwitch') }}</div>
            <div class="mt-1 font-semibold text-gray-900 dark:text-white">{{ policyState }}</div>
            <div class="mt-1 text-xs text-gray-500 dark:text-gray-400">{{ t('admin.imageCanvas.modelPolicyVersion', { version: overview?.policy_version || '-' }) }}</div>
          </div>
        </div>
        <div class="flex items-start gap-3 border-b border-gray-200 px-4 py-4 dark:border-dark-700 sm:border-r sm:border-b-0">
          <span class="flex h-9 w-9 shrink-0 items-center justify-center rounded-lg bg-sky-50 text-sky-600 dark:bg-sky-950/40 dark:text-sky-300"><Icon name="server" size="sm" /></span>
          <div class="min-w-0">
            <div class="text-xs font-medium uppercase text-gray-500 dark:text-gray-400">{{ t('admin.imageCanvas.workerState') }}</div>
            <div class="mt-1 font-semibold text-gray-900 dark:text-white">{{ workerState }}</div>
            <div class="mt-1 text-xs text-gray-500 dark:text-gray-400">{{ storageState }}</div>
          </div>
        </div>
        <div class="flex items-start gap-3 px-4 py-4">
          <span class="flex h-9 w-9 shrink-0 items-center justify-center rounded-lg bg-emerald-50 text-emerald-600 dark:bg-emerald-950/40 dark:text-emerald-300"><Icon name="chart" size="sm" /></span>
          <div class="min-w-0">
            <div class="text-xs font-medium uppercase text-gray-500 dark:text-gray-400">{{ t('admin.imageCanvas.activeModels') }}</div>
            <div class="mt-1 font-semibold text-gray-900 dark:text-white">{{ overview ? overview.active_model_count : '-' }}</div>
            <div class="mt-1 text-xs text-gray-500 dark:text-gray-400">{{ t('admin.imageCanvas.fallbackStatus') }}</div>
          </div>
        </div>
      </section>

      <div class="flex max-w-full overflow-x-auto border-b border-gray-200 dark:border-dark-700" role="tablist">
        <button
          id="image-canvas-policy-tab"
          type="button"
          role="tab"
          class="inline-flex shrink-0 items-center gap-2 border-b-2 px-4 py-3 text-sm font-medium transition-colors"
          :class="activeTab === 'policy' ? 'border-primary-500 text-primary-600 dark:text-primary-300' : 'border-transparent text-gray-500 hover:text-gray-800 dark:text-gray-400 dark:hover:text-gray-200'"
          :aria-selected="activeTab === 'policy'"
          aria-controls="image-canvas-policy-panel"
          @click="activeTab = 'policy'"
        >
          <Icon name="sort" size="sm" />
          {{ t('admin.imageCanvas.policyTab') }}
        </button>
        <button
          id="image-canvas-runtime-tab"
          type="button"
          role="tab"
          class="inline-flex shrink-0 items-center gap-2 border-b-2 px-4 py-3 text-sm font-medium transition-colors"
          :class="activeTab === 'runtime' ? 'border-primary-500 text-primary-600 dark:text-primary-300' : 'border-transparent text-gray-500 hover:text-gray-800 dark:text-gray-400 dark:hover:text-gray-200'"
          :aria-selected="activeTab === 'runtime'"
          aria-controls="image-canvas-runtime-panel"
          @click="activeTab = 'runtime'"
        >
          <Icon name="cog" size="sm" />
          {{ t('admin.imageCanvas.runtimeTab') }}
        </button>
      </div>

      <div v-if="activeTab === 'policy'" id="image-canvas-policy-panel" role="tabpanel" aria-labelledby="image-canvas-policy-tab">
        <ModelPolicyEditor ref="policyEditor" @saved="loadOverview" />
        <RecentImageJobsPanel ref="recentJobsPanel" class="mt-5" />
        <BackendLogicPanel class="mt-5" />
      </div>
      <div v-else id="image-canvas-runtime-panel" role="tabpanel" aria-labelledby="image-canvas-runtime-tab">
        <RuntimeSettingsEditor ref="runtimeEditor" />
      </div>
    </div>
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import AppLayout from '@/components/layout/AppLayout.vue'
import Icon from '@/components/icons/Icon.vue'
import ModelPolicyEditor from '@/components/admin/image-canvas/ModelPolicyEditor.vue'
import RuntimeSettingsEditor from '@/components/admin/image-canvas/RuntimeSettingsEditor.vue'
import RecentImageJobsPanel from '@/components/admin/image-canvas/RecentImageJobsPanel.vue'
import BackendLogicPanel from '@/components/admin/image-canvas/BackendLogicPanel.vue'
import { getImageCanvasOverview, type ImageCanvasOverviewResponse } from '@/api/admin/imageCanvas'

const { t } = useI18n()
const activeTab = ref<'policy' | 'runtime'>('policy')
const overview = ref<ImageCanvasOverviewResponse | null>(null)
const refreshing = ref(false)
const policyEditor = ref<{ load: () => Promise<void> } | null>(null)
const recentJobsPanel = ref<{ load: () => Promise<void> } | null>(null)
const runtimeEditor = ref<{ load: () => Promise<void> } | null>(null)

const entryState = computed(() => overview.value ? (overview.value.entry_enabled ? t('common.enabled') : t('common.disabled')) : '-')
const policyState = computed(() => overview.value ? (overview.value.policy_enabled ? t('common.enabled') : t('common.disabled')) : '-')
const workerState = computed(() => {
  if (!overview.value) return '-'
  if (!overview.value.worker.started) return t('admin.imageCanvas.workerStopped')
  return t('admin.imageCanvas.workerCount', { running: overview.value.worker.running_workers, target: overview.value.worker.target_workers })
})
const storageState = computed(() => {
  if (!overview.value) return '-'
  return overview.value.storage.enabled ? `${overview.value.storage.driver || t('common.enabled')} · ${overview.value.storage.location || '-'}` : t('common.disabled')
})

async function refreshAll() {
  if (refreshing.value) return
  refreshing.value = true
  try {
    if (activeTab.value === 'policy') {
      await Promise.all([
        loadOverview(),
        policyEditor.value?.load(),
        recentJobsPanel.value?.load()
      ])
    } else {
      await Promise.all([loadOverview(), runtimeEditor.value?.load()])
    }
  } finally {
    refreshing.value = false
  }
}

async function loadOverview() {
  try {
    overview.value = await getImageCanvasOverview()
  } catch {
    overview.value = null
  }
}

onMounted(loadOverview)
</script>
