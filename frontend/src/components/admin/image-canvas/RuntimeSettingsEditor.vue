<template>
  <div class="space-y-5">
    <div class="flex flex-wrap items-center justify-between gap-3">
      <div>
        <h2 class="text-lg font-semibold text-gray-900 dark:text-white">{{ t('admin.imageCanvas.runtimeTitle') }}</h2>
        <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">{{ t('admin.imageCanvas.runtimeDescription') }}</p>
      </div>
      <div class="flex flex-wrap items-center gap-2">
        <button
          v-if="response && draft"
          type="button"
          class="btn btn-secondary btn-sm"
          :disabled="loading || saving"
          @click="restoreDefaults"
        >
          <Icon name="sync" size="sm" />
          {{ t('admin.imageCanvas.restoreDefaults') }}
        </button>
        <button type="button" class="icon-button" :disabled="loading" :title="t('common.refresh')" @click="load">
          <Icon name="refresh" size="sm" :class="loading ? 'animate-spin' : ''" />
        </button>
        <button
          type="button"
          class="btn btn-primary btn-sm"
          :disabled="loading || saving || !dirty || Boolean(validationError)"
          @click="save"
        >
          <Icon name="check" size="sm" />
          {{ saving ? t('common.saving') : t('common.save') }}
        </button>
      </div>
    </div>

    <div v-if="loading && !draft" class="flex justify-center py-12">
      <span class="h-7 w-7 animate-spin rounded-full border-2 border-primary-500 border-t-transparent" />
    </div>

    <template v-else-if="draft && response">
      <section class="border border-gray-200 bg-white p-5 dark:border-dark-700 dark:bg-dark-900">
        <div class="grid gap-5 sm:grid-cols-2 lg:grid-cols-5">
          <div>
            <div class="text-xs font-medium text-gray-500 dark:text-gray-400">{{ t('admin.imageCanvas.workerState') }}</div>
            <div class="mt-1 flex items-center gap-2 font-semibold text-gray-900 dark:text-white">
              <Icon name="cpu" size="sm" />
              <span>{{ workerSummary }}</span>
            </div>
          </div>
          <div>
            <div class="text-xs font-medium text-gray-500 dark:text-gray-400">{{ t('admin.imageCanvas.storageState') }}</div>
            <div class="mt-1 font-semibold text-gray-900 dark:text-white">{{ storageSummary }}</div>
          </div>
          <div class="min-w-0">
            <div class="text-xs font-medium text-gray-500 dark:text-gray-400">{{ t('admin.imageCanvas.storageLocation') }}</div>
            <div class="mt-1 truncate font-semibold text-gray-900 dark:text-white" :title="response.storage.location">
              {{ response.storage.location || '-' }}
            </div>
          </div>
          <div>
            <div class="text-xs font-medium text-gray-500 dark:text-gray-400">{{ t('admin.imageCanvas.objectLimit') }}</div>
            <div class="mt-1 font-semibold text-gray-900 dark:text-white">{{ formatBytes(response.storage.max_object_bytes) }}</div>
          </div>
          <div>
            <div class="text-xs font-medium text-gray-500 dark:text-gray-400">{{ t('admin.imageCanvas.applyMode') }}</div>
            <div class="mt-1 font-semibold text-gray-900 dark:text-white">{{ applyMode }}</div>
          </div>
        </div>
        <p class="mt-4 border-t border-gray-100 pt-3 text-sm text-gray-500 dark:border-dark-700 dark:text-gray-400">
          {{ response.storage.generated_assets_permanent
            ? t('admin.imageCanvas.permanentAssetNotice')
            : t('admin.imageCanvas.temporaryAssetNotice') }}
        </p>
      </section>

      <section class="border border-gray-200 bg-white p-5 dark:border-dark-700 dark:bg-dark-900">
        <h3 class="font-medium text-gray-900 dark:text-white">{{ t('admin.imageCanvas.limitsTitle') }}</h3>
        <div class="mt-4 grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
          <label>
            <span class="input-label">{{ t('admin.imageCanvas.workerConcurrency') }}</span>
            <input v-model.number="draft.worker_concurrency" class="input" type="number" min="1" max="32" step="1" />
            <span class="mt-1 block text-xs text-gray-500 dark:text-gray-400">{{ t('admin.imageCanvas.workerConcurrencyHint') }}</span>
          </label>
          <label>
            <span class="input-label">{{ t('admin.imageCanvas.maxActiveJobs') }}</span>
            <input v-model.number="draft.max_active_jobs_per_user" class="input" type="number" min="1" max="200" step="1" />
            <span class="mt-1 block text-xs text-gray-500 dark:text-gray-400">{{ t('admin.imageCanvas.maxActiveJobsHint') }}</span>
          </label>
          <label>
            <span class="input-label">{{ t('admin.imageCanvas.taskTimeout') }}</span>
            <input v-model.number="draft.task_timeout_seconds" class="input" type="number" min="60" max="7200" step="60" />
            <span class="mt-1 block text-xs text-gray-500 dark:text-gray-400">
              {{ t('admin.imageCanvas.currentDuration', { duration: formatDuration(draft.task_timeout_seconds) }) }}
            </span>
          </label>
          <label>
            <span class="input-label">{{ t('admin.imageCanvas.maxOutputs') }}</span>
            <input v-model.number="draft.max_outputs_per_job" class="input" type="number" min="1" max="4" step="1" />
          </label>
          <label>
            <span class="input-label">{{ t('admin.imageCanvas.maxInputs') }}</span>
            <input v-model.number="draft.max_input_images" class="input" type="number" min="1" max="16" step="1" />
          </label>
          <label>
            <span class="input-label">{{ t('admin.imageCanvas.resultRetention') }}</span>
            <input v-model.number="draft.result_ttl_seconds" class="input" type="number" min="3600" max="31536000" step="3600" />
            <span class="mt-1 block text-xs text-gray-500 dark:text-gray-400">
              {{ t('admin.imageCanvas.currentDuration', { duration: formatDuration(draft.result_ttl_seconds) }) }}
            </span>
          </label>
        </div>
        <div class="mt-4 rounded border border-amber-200 bg-amber-50 px-4 py-3 text-sm text-amber-800 dark:border-amber-900/60 dark:bg-amber-950/30 dark:text-amber-200">
          {{ t('admin.imageCanvas.concurrencyNotice') }}
        </div>
        <p v-if="validationError" class="mt-3 text-sm text-rose-600" role="alert">{{ validationError }}</p>
      </section>

      <section class="border border-gray-200 bg-white p-5 dark:border-dark-700 dark:bg-dark-900">
        <div class="flex flex-wrap items-center justify-between gap-3">
          <div>
            <h3 class="font-medium text-gray-900 dark:text-white">{{ t('admin.imageCanvas.defaultsTitle') }}</h3>
            <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">{{ t('admin.imageCanvas.defaultsDescription') }}</p>
          </div>
          <button type="button" class="btn btn-secondary btn-sm" :disabled="!canAddDefault" @click="addDefaultOverride">
            <Icon name="plus" size="sm" />
            {{ t('admin.imageCanvas.addDefault') }}
          </button>
        </div>

        <div v-if="draft.default_overrides.length" class="mt-4 divide-y divide-gray-100 dark:divide-dark-700">
          <div v-for="(item, index) in draft.default_overrides" :key="index" class="py-4 first:pt-0 last:pb-0">
            <div class="flex items-center gap-3">
              <select v-model="item.model" class="input min-w-0 flex-1" @change="resetParameters(item)">
                <option v-for="modelItem in defaultModelOptions(index)" :key="modelItem.model" :value="modelItem.model">
                  {{ modelItem.model }}
                </option>
              </select>
              <button
                type="button"
                class="icon-button"
                :title="t('common.delete')"
                :aria-label="t('common.delete')"
                @click="draft.default_overrides.splice(index, 1)"
              >
                <Icon name="trash" size="sm" />
              </button>
            </div>
            <ModelParameterFields
              v-model="item.parameters"
              class="mt-3"
              :model="item.model"
              :capability="capabilityFor(item.model)"
            />
          </div>
        </div>
        <p v-else class="mt-4 text-sm text-gray-500 dark:text-gray-400">{{ t('admin.imageCanvas.noDefaultOverrides') }}</p>
      </section>

      <section class="border border-gray-200 bg-white p-5 dark:border-dark-700 dark:bg-dark-900">
        <div class="flex flex-wrap items-center justify-between gap-3">
          <div>
            <h3 class="font-medium text-gray-900 dark:text-white">{{ t('admin.imageCanvas.presetsTitle') }}</h3>
            <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">{{ t('admin.imageCanvas.presetsDescription') }}</p>
          </div>
          <button type="button" class="btn btn-secondary btn-sm" :disabled="!availableModels.length" @click="addPreset">
            <Icon name="plus" size="sm" />
            {{ t('admin.imageCanvas.addPreset') }}
          </button>
        </div>

        <div v-if="draft.custom_presets.length" class="mt-4 divide-y divide-gray-100 dark:divide-dark-700">
          <div v-for="(preset, index) in draft.custom_presets" :key="index" class="py-5 first:pt-0 last:pb-0">
            <div class="grid gap-3 sm:grid-cols-2 lg:grid-cols-[minmax(0,1.2fr)_minmax(0,0.8fr)_minmax(0,1fr)_auto]">
              <label>
                <span class="input-label">{{ t('admin.imageCanvas.model') }}</span>
                <select v-model="preset.model" class="input" @change="resetParameters(preset)">
                  <option v-for="modelItem in availableModels" :key="modelItem.model" :value="modelItem.model">
                    {{ modelItem.model }}
                  </option>
                </select>
              </label>
              <label>
                <span class="input-label">{{ t('admin.imageCanvas.presetId') }}</span>
                <input v-model.trim="preset.id" class="input" maxlength="64" />
              </label>
              <label>
                <span class="input-label">{{ t('admin.imageCanvas.presetLabel') }}</span>
                <input v-model.trim="preset.label" class="input" maxlength="80" />
              </label>
              <button
                type="button"
                class="icon-button self-end"
                :title="t('common.delete')"
                :aria-label="t('common.delete')"
                @click="draft.custom_presets.splice(index, 1)"
              >
                <Icon name="trash" size="sm" />
              </button>
            </div>
            <ModelParameterFields
              v-model="preset.parameters"
              class="mt-3"
              :model="preset.model"
              :capability="capabilityFor(preset.model)"
            />
            <label class="mt-3 inline-flex items-center gap-2 text-sm text-gray-600 dark:text-gray-300">
              <input v-model="preset.experimental" type="checkbox" class="h-4 w-4 accent-primary-600" />
              {{ t('admin.imageCanvas.experimentalPreset') }}
            </label>
          </div>
        </div>
        <p v-else class="mt-4 text-sm text-gray-500 dark:text-gray-400">{{ t('admin.imageCanvas.noCustomPresets') }}</p>
      </section>
    </template>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import Icon from '@/components/icons/Icon.vue'
import ModelParameterFields from './ModelParameterFields.vue'
import { useAppStore } from '@/stores/app'
import {
  getImageCanvasRuntimeSettings,
  updateImageCanvasRuntimeSettings,
  type ImageCanvasCapability,
  type ImageCanvasDefaultOverride,
  type ImageCanvasModelParameters,
  type ImageCanvasPolicyItem,
  type ImageCanvasPresetOverride,
  type ImageCanvasRuntimeResponse,
  type ImageCanvasRuntimeSettings
} from '@/api/admin/imageCanvas'

const { t } = useI18n()
const appStore = useAppStore()
const response = ref<ImageCanvasRuntimeResponse | null>(null)
const draft = ref<ImageCanvasRuntimeSettings | null>(null)
const original = ref('')
const loading = ref(false)
const saving = ref(false)

const availableModels = computed(() => response.value?.available_models || [])
const dirty = computed(() => Boolean(draft.value) && JSON.stringify(draft.value) !== original.value)
const canAddDefault = computed(() => {
  if (!draft.value) return false
  const used = new Set(draft.value.default_overrides.map((item) => item.model))
  return availableModels.value.some((item) => !used.has(item.model))
})
const workerSummary = computed(() => {
  if (!response.value?.worker.started) return t('admin.imageCanvas.workerStopped')
  return t('admin.imageCanvas.workerCount', {
    running: response.value.worker.running_workers,
    target: response.value.worker.target_workers
  })
})
const storageSummary = computed(() => {
  if (!response.value?.storage.enabled) return t('common.disabled')
  return response.value.storage.driver || t('common.enabled')
})
const applyMode = computed(() => response.value?.storage.runtime_changes_need_restart
  ? t('admin.imageCanvas.restartRequired')
  : t('admin.imageCanvas.appliesImmediately'))

const validationError = computed(() => validateSettings(draft.value))

function cloneSettings(value: ImageCanvasRuntimeSettings): ImageCanvasRuntimeSettings {
  const cloned = JSON.parse(JSON.stringify(value)) as ImageCanvasRuntimeSettings
  cloned.default_overrides ||= []
  cloned.custom_presets ||= []
  return cloned
}

function capabilityFor(model: string): ImageCanvasCapability | undefined {
  return availableModels.value.find((item) => item.model === model)?.capability
}

function parametersFor(model: string): ImageCanvasModelParameters {
  return { ...(capabilityFor(model)?.defaults || {}) }
}

function resetParameters(item: ImageCanvasDefaultOverride | ImageCanvasPresetOverride) {
  item.parameters = parametersFor(item.model)
}

function defaultModelOptions(index: number): ImageCanvasPolicyItem[] {
  if (!draft.value) return []
  const used = new Set(
    draft.value.default_overrides
      .filter((_, itemIndex) => itemIndex !== index)
      .map((item) => item.model)
  )
  return availableModels.value.filter((item) => !used.has(item.model))
}

async function load() {
  loading.value = true
  try {
    response.value = await getImageCanvasRuntimeSettings()
    draft.value = cloneSettings(response.value.settings)
    original.value = JSON.stringify(draft.value)
  } catch (error) {
    appStore.showError(errorMessage(error))
  } finally {
    loading.value = false
  }
}

async function save() {
  if (!draft.value || validationError.value) return
  saving.value = true
  try {
    const input = cloneSettings(draft.value)
    input.default_overrides = input.default_overrides.map((item) => ({ ...item, model: item.model.trim() }))
    input.custom_presets = input.custom_presets.map((item) => ({
      ...item,
      model: item.model.trim(),
      id: item.id.trim(),
      label: item.label.trim()
    }))
    response.value = await updateImageCanvasRuntimeSettings(input)
    draft.value = cloneSettings(response.value.settings)
    original.value = JSON.stringify(draft.value)
    appStore.showSuccess(t('common.saved'))
  } catch (error) {
    appStore.showError(errorMessage(error))
  } finally {
    saving.value = false
  }
}

function restoreDefaults() {
  if (!response.value) return
  draft.value = cloneSettings(response.value.defaults)
}

function addDefaultOverride() {
  if (!draft.value) return
  const used = new Set(draft.value.default_overrides.map((item) => item.model))
  const model = availableModels.value.find((item) => !used.has(item.model))?.model
  if (!model) return
  draft.value.default_overrides.push({ model, parameters: parametersFor(model) })
}

function addPreset() {
  if (!draft.value || !availableModels.value.length) return
  const model = availableModels.value[0].model
  let sequence = draft.value.custom_presets.length + 1
  let id = `custom-${sequence}`
  const used = new Set(draft.value.custom_presets.filter((item) => item.model === model).map((item) => item.id))
  while (used.has(id)) id = `custom-${++sequence}`
  draft.value.custom_presets.push({
    model,
    id,
    label: t('admin.imageCanvas.presetDefaultLabel', { number: sequence }),
    parameters: parametersFor(model),
    experimental: false
  })
}

function validateSettings(value: ImageCanvasRuntimeSettings | null): string {
  if (!value) return ''
  const limits: Array<[number, number, number]> = [
    [value.worker_concurrency, 1, 32],
    [value.max_active_jobs_per_user, 1, 200],
    [value.task_timeout_seconds, 60, 7200],
    [value.max_outputs_per_job, 1, 4],
    [value.max_input_images, 1, 16],
    [value.result_ttl_seconds, 3600, 31536000]
  ]
  if (limits.some(([current, min, max]) => !Number.isInteger(current) || current < min || current > max)) {
    return t('admin.imageCanvas.invalidLimits')
  }
  if (value.default_overrides.length > 64 || value.custom_presets.length > 128) {
    return t('admin.imageCanvas.tooManyOverrides')
  }

  const models = new Set(availableModels.value.map((item) => item.model))
  const defaultModels = new Set<string>()
  for (const item of value.default_overrides) {
    if (!models.has(item.model)) return t('admin.imageCanvas.unknownModel', { model: item.model })
    if (defaultModels.has(item.model)) return t('admin.imageCanvas.duplicateDefault', { model: item.model })
    defaultModels.add(item.model)
    if (!validParameters(item.parameters, capabilityFor(item.model))) {
      return t('admin.imageCanvas.invalidParameters', { model: item.model })
    }
  }

  const presetKeys = new Set<string>()
  for (const preset of value.custom_presets) {
    if (!models.has(preset.model)) return t('admin.imageCanvas.unknownModel', { model: preset.model })
    if (!/^[A-Za-z0-9._-]{1,64}$/.test(preset.id) || !preset.label.trim() || preset.label.length > 80) {
      return t('admin.imageCanvas.invalidPreset')
    }
    const key = `${preset.model}\u0000${preset.id}`
    if (presetKeys.has(key)) return t('admin.imageCanvas.duplicatePreset', { id: preset.id, model: preset.model })
    presetKeys.add(key)
    if (!validParameters(preset.parameters, capabilityFor(preset.model))) {
      return t('admin.imageCanvas.invalidParameters', { model: preset.model })
    }
  }
  return ''
}

function validParameters(parameters: ImageCanvasModelParameters, capability?: ImageCanvasCapability): boolean {
  if (!capability) return false
  const size = parameters.size?.trim() || ''
  const aspectRatio = parameters.aspect_ratio?.trim() || ''
  const resolution = parameters.resolution?.trim() || ''
  if (capability.dimension_mode === 'size') {
    if (aspectRatio || resolution) return false
    if (size && !capability.sizes.includes(size) && !validCustomSize(size, capability)) return false
  }
  if (capability.dimension_mode === 'aspect_ratio_resolution') {
    if (size) return false
    if (aspectRatio && !capability.aspect_ratios?.includes(aspectRatio)) return false
    if (resolution && !capability.resolutions?.includes(resolution)) return false
  }
  if (parameters.quality && !capability.qualities?.includes(parameters.quality)) return false
  if (parameters.output_format && !capability.output_formats?.includes(parameters.output_format)) return false
  if (parameters.background && !capability.backgrounds?.includes(parameters.background)) return false
  if (parameters.background === 'transparent' && parameters.output_format === 'jpeg') return false
  if (parameters.output_compression !== undefined) {
    if (!Number.isInteger(parameters.output_compression) || parameters.output_compression < 0 || parameters.output_compression > 100) return false
    if (!capability.output_compression || !['jpeg', 'webp'].includes(parameters.output_format || '')) return false
  }
  return true
}

function validCustomSize(size: string, capability: ImageCanvasCapability): boolean {
  const constraints = capability.custom_size
  const match = /^(\d+)x(\d+)$/i.exec(size)
  if (!constraints || !match) return false
  const width = Number(match[1])
  const height = Number(match[2])
  const pixels = width * height
  const ratio = Math.max(width, height) / Math.min(width, height)
  return width > 0 && height > 0
    && width <= constraints.max_edge && height <= constraints.max_edge
    && width % constraints.multiple_of === 0 && height % constraints.multiple_of === 0
    && pixels >= constraints.min_pixels && pixels <= constraints.max_pixels
    && ratio <= constraints.max_aspect_ratio
}

function formatBytes(value: number): string {
  if (!Number.isFinite(value) || value <= 0) return '-'
  return `${Math.round(value / (1024 * 1024))} MiB`
}

function formatDuration(seconds: number): string {
  if (!Number.isFinite(seconds) || seconds <= 0) return '-'
  if (seconds % 86400 === 0) return t('admin.imageCanvas.durationDays', { value: seconds / 86400 })
  if (seconds % 3600 === 0) return t('admin.imageCanvas.durationHours', { value: seconds / 3600 })
  if (seconds % 60 === 0) return t('admin.imageCanvas.durationMinutes', { value: seconds / 60 })
  return t('admin.imageCanvas.durationSeconds', { value: seconds })
}

function errorMessage(error: unknown): string {
  return error instanceof Error ? error.message : t('common.error')
}

onMounted(load)

defineExpose({ load })
</script>
