<template>
  <section class="image-canvas-host" :aria-busy="loading">
    <div ref="mountElement" class="image-canvas-mount" />
    <div v-if="loading" class="image-canvas-loading" role="status">
      <span class="h-5 w-5 animate-spin rounded-full border-2 border-primary-500 border-t-transparent" />
      <span>{{ t('common.loading') }}</span>
    </div>
    <div v-else-if="errorMessage" class="image-canvas-error">
      <p>{{ errorMessage }}</p>
      <button type="button" class="btn btn-secondary btn-sm" @click="reload">{{ t('common.refresh') }}</button>
    </div>
  </section>
</template>

<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute, useRouter } from 'vue-router'
import { getLocale } from '@/i18n'
import { loadCanvasModule, type CanvasHostModule } from './canvasLoader'
import { createCanvasHostContext } from './canvasHostBridge'

const props = withDefaults(defineProps<{ routeMode?: 'user' | 'admin' }>(), { routeMode: 'user' })
const { t } = useI18n()
const router = useRouter()
const route = useRoute()
const mountElement = ref<HTMLElement | null>(null)
const loading = ref(true)
const errorMessage = ref('')
const canvasModule = ref<CanvasHostModule | null>(null)
let handle: ReturnType<CanvasHostModule['mountCanvas']> | undefined
let observer: MutationObserver | undefined
let shadowRoot: ShadowRoot | undefined
let canvasElement: HTMLElement | undefined

function currentTheme(): 'light' | 'dark' {
  return document.documentElement.classList.contains('dark') ? 'dark' : 'light'
}

function context() {
  const theme = currentTheme()
  mountElement.value?.classList.toggle('dark', theme === 'dark')
  return createCanvasHostContext(props.routeMode, (path) => void router.push(path), theme)
}

function ensureCanvasElement(): HTMLElement | undefined {
  const host = mountElement.value
  if (!host) return undefined
  shadowRoot ||= host.shadowRoot || host.attachShadow({ mode: 'open' })
  if (!canvasElement?.isConnected) {
    canvasElement = document.createElement('div')
    canvasElement.style.width = '100%'
    canvasElement.style.height = '100%'
    shadowRoot.appendChild(canvasElement)
  }
  return canvasElement
}

async function reload() {
  handle?.unmount()
  handle = undefined
  loading.value = true
  errorMessage.value = ''
  try {
    const target = ensureCanvasElement()
    if (!target || !shadowRoot) return
    canvasModule.value ||= await loadCanvasModule(shadowRoot)
    handle = canvasModule.value.mountCanvas(target, context())
  } catch (error) {
    errorMessage.value = error instanceof Error ? error.message : t('common.error')
  } finally {
    loading.value = false
  }
}

onMounted(() => {
  observer = new MutationObserver(() => handle?.updateContext(context()))
  observer.observe(document.documentElement, { attributes: true, attributeFilter: ['class', 'lang'] })
  void reload()
})

watch(() => [getLocale(), route.fullPath], () => handle?.updateContext(context()))

onBeforeUnmount(() => {
  observer?.disconnect()
  handle?.unmount()
  handle = undefined
  canvasElement = undefined
  shadowRoot = undefined
})
</script>

<style scoped>
.image-canvas-host { position: relative; min-height: 480px; height: 100dvh; overflow: hidden; background: rgb(248 250 252); }
.image-canvas-mount { width: 100%; height: 100%; }
.image-canvas-loading, .image-canvas-error { position: absolute; inset: 0; display: flex; align-items: center; justify-content: center; gap: .75rem; flex-direction: column; background: rgb(248 250 252 / .94); color: rgb(71 85 105); }
:global(.dark) .image-canvas-host { background: rgb(15 23 42); }
:global(.dark) .image-canvas-loading, :global(.dark) .image-canvas-error { background: rgb(15 23 42 / .94); color: rgb(203 213 225); }
</style>
