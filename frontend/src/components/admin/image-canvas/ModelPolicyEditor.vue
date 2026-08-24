<template>
  <div class="space-y-6">
    <div class="flex flex-wrap items-center justify-between gap-3">
      <div>
        <h2 class="text-lg font-semibold text-gray-900 dark:text-white">{{ t('admin.imageCanvas.policyTitle') }}</h2>
        <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">{{ t('admin.imageCanvas.policyDescription') }}</p>
      </div>
      <div class="flex items-center gap-2">
        <button type="button" class="btn btn-secondary btn-sm" :disabled="loading" :title="t('common.refresh')" @click="load">
          <Icon name="refresh" size="sm" :class="loading ? 'animate-spin' : ''" />
        </button>
        <button type="button" class="btn btn-primary btn-sm" :disabled="loading || saving || !dirty" @click="save">
          <Icon name="check" size="sm" />
          {{ saving ? t('common.saving') : t('common.save') }}
        </button>
      </div>
    </div>

    <div v-if="loading && !policy" class="flex justify-center py-12"><span class="h-7 w-7 animate-spin rounded-full border-2 border-primary-500 border-t-transparent" /></div>
    <template v-else-if="policy">
      <div class="card space-y-4 p-6">
        <label class="flex items-center justify-between gap-4">
          <span>
            <span class="block font-medium text-gray-900 dark:text-white">{{ t('admin.imageCanvas.enabled') }}</span>
            <span class="text-sm text-gray-500 dark:text-gray-400">{{ t('admin.imageCanvas.enabledDescription') }}</span>
          </span>
          <input v-model="draftEnabled" type="checkbox" class="h-4 w-4 accent-primary-600" />
        </label>
        <div v-if="draft.length === 0" class="rounded-lg border border-dashed border-gray-300 p-6 text-center text-sm text-gray-500 dark:border-dark-600">{{ t('admin.imageCanvas.noModels') }}</div>
        <ol v-else class="divide-y divide-gray-100 rounded-lg border border-gray-200 dark:divide-dark-700 dark:border-dark-700">
          <li v-for="(item, index) in draft" :key="item.model" class="flex items-center gap-3 px-4 py-3">
            <span class="w-7 text-center font-mono text-sm text-gray-400">{{ index + 1 }}</span>
            <div class="min-w-0 flex-1">
              <div class="truncate font-medium text-gray-900 dark:text-white">{{ item.model }}</div>
              <div class="mt-1 flex flex-wrap gap-2 text-xs text-gray-500 dark:text-gray-400">
                <span>{{ item.capability?.generation ? t('admin.imageCanvas.generation') : '' }}</span>
                <span>{{ item.capability?.edit ? t('admin.imageCanvas.edit') : '' }}</span>
                <span v-if="item.capability?.sizes?.length">{{ item.capability.sizes.join(', ') }}</span>
              </div>
            </div>
            <label class="flex items-center gap-2 text-sm text-gray-600 dark:text-gray-300">
              <input v-model="item.enabled" type="checkbox" class="h-4 w-4 accent-primary-600" />
              {{ t('admin.imageCanvas.modelEnabled') }}
            </label>
            <button type="button" class="icon-button" :disabled="index === 0" :title="t('admin.imageCanvas.moveUp')" @click="move(index, -1)"><Icon name="arrowUp" size="sm" /></button>
            <button type="button" class="icon-button" :disabled="index === draft.length - 1" :title="t('admin.imageCanvas.moveDown')" @click="move(index, 1)"><Icon name="arrowDown" size="sm" /></button>
          </li>
        </ol>
        <p v-if="!hasEnabledModel" class="text-sm text-rose-600">{{ t('admin.imageCanvas.atLeastOne') }}</p>
      </div>

      <div v-if="availableOnly.length" class="card p-6">
        <h3 class="font-medium text-gray-900 dark:text-white">{{ t('admin.imageCanvas.availableModels') }}</h3>
        <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">{{ t('admin.imageCanvas.availableModelsDescription') }}</p>
        <div class="mt-4 flex flex-wrap gap-2">
          <button v-for="item in availableOnly" :key="item.model" type="button" class="btn btn-secondary btn-sm" @click="add(item)"><Icon name="plus" size="sm" />{{ item.model }}</button>
        </div>
      </div>
    </template>

    <div v-if="audits.length" class="card p-6">
      <h3 class="font-medium text-gray-900 dark:text-white">{{ t('admin.imageCanvas.auditTitle') }}</h3>
      <ul class="mt-3 divide-y divide-gray-100 text-sm dark:divide-dark-700">
        <li v-for="audit in audits" :key="audit.id" class="flex flex-wrap justify-between gap-2 py-2 text-gray-600 dark:text-gray-300"><span>v{{ audit.old_version }} → v{{ audit.new_version }}</span><time :datetime="audit.created_at">{{ formatDate(audit.created_at) }}</time></li>
      </ul>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import Icon from '@/components/icons/Icon.vue'
import { useAppStore } from '@/stores/app'
import { getImageCanvasPolicy, listImageCanvasPolicyAudit, updateImageCanvasPolicy, type ImageCanvasPolicy, type ImageCanvasPolicyAudit, type ImageCanvasPolicyItem } from '@/api/admin/imageCanvas'

const { t, locale } = useI18n()
const appStore = useAppStore()
const policy = ref<ImageCanvasPolicy | null>(null)
const draft = ref<ImageCanvasPolicyItem[]>([])
const draftEnabled = ref(false)
const audits = ref<ImageCanvasPolicyAudit[]>([])
const loading = ref(false)
const saving = ref(false)
const original = ref('')
const dirty = computed(() => JSON.stringify({ enabled: draftEnabled.value, models: draft.value.map(({ model, enabled }, position) => ({ model, enabled, position })) }) !== original.value)
const hasEnabledModel = computed(() => draft.value.some((item) => item.enabled))
const availableOnly = computed(() => (policy.value?.available_models || []).filter((item) => !draft.value.some((draftItem) => draftItem.model === item.model)))

function snapshot() {
  return JSON.stringify({ enabled: draftEnabled.value, models: draft.value.map(({ model, enabled }, position) => ({ model, enabled, position })) })
}

async function load() {
  loading.value = true
  try {
    policy.value = await getImageCanvasPolicy()
    draft.value = policy.value.models.map((item) => ({ ...item }))
    draftEnabled.value = policy.value.enabled
    audits.value = await listImageCanvasPolicyAudit()
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

function add(item: ImageCanvasPolicyItem) {
  draft.value.push({ ...item, enabled: true })
}

async function save() {
  if (!policy.value || !hasEnabledModel.value) return
  saving.value = true
  try {
    policy.value = await updateImageCanvasPolicy({ version: policy.value.version, enabled: draftEnabled.value, models: draft.value.map((item, position) => ({ model: item.model, enabled: item.enabled, position })) })
    draft.value = policy.value.models.map((item) => ({ ...item }))
    draftEnabled.value = policy.value.enabled
    original.value = snapshot()
    audits.value = await listImageCanvasPolicyAudit()
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

function formatDate(value: string) { return new Intl.DateTimeFormat(locale.value, { dateStyle: 'medium', timeStyle: 'short' }).format(new Date(value)) }
onMounted(load)
</script>
