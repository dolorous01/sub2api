<template>
  <div v-if="capability" class="grid gap-3 sm:grid-cols-2 lg:grid-cols-4">
    <label v-if="capability.dimension_mode === 'size'">
      <span class="input-label">{{ t('admin.imageCanvas.size') }}</span>
      <input
        v-if="capability.custom_size"
        :value="modelValue.size || ''"
        :list="sizeListId"
        class="input"
        inputmode="text"
        placeholder="1024x1024"
        @change="updateString('size', $event)"
      />
      <select v-else :value="modelValue.size || ''" class="input" @change="updateString('size', $event)">
        <option value="">{{ t('admin.imageCanvas.noDefault') }}</option>
        <option v-for="option in capability.sizes" :key="option" :value="option">{{ option }}</option>
      </select>
      <datalist v-if="capability.custom_size" :id="sizeListId">
        <option v-for="option in capability.sizes" :key="option" :value="option" />
      </datalist>
      <span v-if="capability.custom_size" class="mt-1 block text-xs text-gray-500 dark:text-gray-400">
        {{ customSizeHint }}
      </span>
      <span v-if="experimentalSize" class="mt-1 block text-xs text-amber-700 dark:text-amber-300">
        {{ t('admin.imageCanvas.experimentalSize', { model }) }}
      </span>
    </label>

    <label v-if="capability.dimension_mode === 'aspect_ratio_resolution'">
      <span class="input-label">{{ t('admin.imageCanvas.aspectRatio') }}</span>
      <select :value="modelValue.aspect_ratio || ''" class="input" @change="updateString('aspect_ratio', $event)">
        <option value="">{{ t('admin.imageCanvas.noDefault') }}</option>
        <option v-for="option in capability.aspect_ratios || []" :key="option" :value="option">{{ option }}</option>
      </select>
    </label>

    <label v-if="capability.dimension_mode === 'aspect_ratio_resolution'">
      <span class="input-label">{{ t('admin.imageCanvas.resolution') }}</span>
      <select :value="modelValue.resolution || ''" class="input" @change="updateString('resolution', $event)">
        <option value="">{{ t('admin.imageCanvas.noDefault') }}</option>
        <option v-for="option in capability.resolutions || []" :key="option" :value="option">{{ option }}</option>
      </select>
    </label>

    <label v-if="capability.qualities?.length">
      <span class="input-label">{{ t('admin.imageCanvas.quality') }}</span>
      <select :value="modelValue.quality || ''" class="input" @change="updateString('quality', $event)">
        <option value="">{{ t('admin.imageCanvas.noDefault') }}</option>
        <option v-for="option in capability.qualities" :key="option" :value="option">{{ option }}</option>
      </select>
    </label>

    <label v-if="capability.output_formats?.length">
      <span class="input-label">{{ t('admin.imageCanvas.format') }}</span>
      <select :value="modelValue.output_format || ''" class="input" @change="updateString('output_format', $event)">
        <option value="">{{ t('admin.imageCanvas.noDefault') }}</option>
        <option v-for="option in capability.output_formats" :key="option" :value="option">{{ option }}</option>
      </select>
    </label>

    <label v-if="capability.backgrounds?.length">
      <span class="input-label">{{ t('admin.imageCanvas.background') }}</span>
      <select :value="modelValue.background || ''" class="input" @change="updateString('background', $event)">
        <option value="">{{ t('admin.imageCanvas.noDefault') }}</option>
        <option v-for="option in backgroundOptions" :key="option" :value="option">{{ option }}</option>
      </select>
    </label>

    <label v-if="compressionAvailable">
      <span class="input-label">{{ t('admin.imageCanvas.compression') }}</span>
      <input
        :value="modelValue.output_compression ?? 90"
        class="input"
        type="number"
        min="0"
        max="100"
        step="1"
        @input="updateCompression"
      />
    </label>
  </div>
</template>

<script setup lang="ts">
import { computed, getCurrentInstance } from 'vue'
import { useI18n } from 'vue-i18n'
import type { ImageCanvasCapability, ImageCanvasModelParameters } from '@/api/admin/imageCanvas'

const props = defineProps<{
  model: string
  modelValue: ImageCanvasModelParameters
  capability?: ImageCanvasCapability
}>()

const emit = defineEmits<{
  'update:modelValue': [value: ImageCanvasModelParameters]
}>()

const { t } = useI18n()
const sizeListId = `image-canvas-sizes-${getCurrentInstance()?.uid ?? 0}`

type StringParameterKey = 'size' | 'aspect_ratio' | 'resolution' | 'quality' | 'output_format' | 'background'

const compressionAvailable = computed(() => {
  const format = props.modelValue.output_format?.toLowerCase()
  return Boolean(props.capability?.output_compression && (format === 'jpeg' || format === 'webp'))
})

const backgroundOptions = computed(() => {
  const options = props.capability?.backgrounds || []
  return props.modelValue.output_format?.toLowerCase() === 'jpeg'
    ? options.filter((option) => option !== 'transparent')
    : options
})

const experimentalSize = computed(() => {
  const size = props.modelValue.size
  return Boolean(size && props.capability?.experimental_sizes?.includes(size))
})

const customSizeHint = computed(() => {
  const constraints = props.capability?.custom_size
  if (!constraints) return ''
  return t('admin.imageCanvas.customSizeHint', {
    maxEdge: constraints.max_edge,
    multiple: constraints.multiple_of,
    maxRatio: constraints.max_aspect_ratio
  })
})

function updateString(key: StringParameterKey, event: Event) {
  const value = (event.target as HTMLInputElement | HTMLSelectElement).value.trim()
  const next: ImageCanvasModelParameters = { ...props.modelValue }
  if (value) next[key] = value
  else delete next[key]

  if (key === 'output_format') {
    const format = value.toLowerCase()
    if (format !== 'jpeg' && format !== 'webp') delete next.output_compression
    if (format === 'jpeg' && next.background === 'transparent') {
      const fallback = props.capability?.backgrounds?.find((option) => option === 'opaque' || option === 'auto')
      if (fallback) next.background = fallback
      else delete next.background
    }
  }

  if (key === 'background' && value === 'transparent' && next.output_format === 'jpeg') {
    const fallback = props.capability?.output_formats?.find((option) => option === 'png' || option === 'webp')
    if (fallback) next.output_format = fallback
    else delete next.output_format
  }

  emit('update:modelValue', next)
}

function updateCompression(event: Event) {
  const raw = (event.target as HTMLInputElement).value
  const next: ImageCanvasModelParameters = { ...props.modelValue }
  if (raw === '') delete next.output_compression
  else next.output_compression = Number(raw)
  emit('update:modelValue', next)
}
</script>
