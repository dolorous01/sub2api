<template>
  <AppLayout>
    <div class="mx-auto max-w-7xl space-y-4">
      <div class="flex flex-wrap items-center justify-between gap-3">
        <div>
          <h1 class="text-2xl font-semibold text-gray-900 dark:text-white">{{ t('admin.imageCanvas.title') }}</h1>
          <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">{{ t('admin.imageCanvas.description') }}</p>
        </div>
        <div class="flex max-w-full flex-wrap items-center justify-end gap-2">
          <div class="flex max-w-full gap-2 overflow-x-auto" role="tablist">
            <button
              id="image-canvas-policy-tab"
              type="button"
              role="tab"
              class="btn btn-secondary btn-sm shrink-0"
              :class="activeTab === 'policy' ? 'ring-2 ring-primary-500' : ''"
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
              class="btn btn-secondary btn-sm shrink-0"
              :class="activeTab === 'runtime' ? 'ring-2 ring-primary-500' : ''"
              :aria-selected="activeTab === 'runtime'"
              aria-controls="image-canvas-runtime-panel"
              @click="activeTab = 'runtime'"
            >
              <Icon name="cog" size="sm" />
              {{ t('admin.imageCanvas.runtimeTab') }}
            </button>
          </div>
          <RouterLink to="/studio" class="btn btn-primary btn-sm shrink-0">
            <Icon name="externalLink" size="sm" />
            {{ t('admin.imageCanvas.openStudio') }}
          </RouterLink>
        </div>
      </div>
      <div
        v-if="activeTab === 'policy'"
        id="image-canvas-policy-panel"
        role="tabpanel"
        aria-labelledby="image-canvas-policy-tab"
      >
        <ModelPolicyEditor />
      </div>
      <div
        v-else-if="activeTab === 'runtime'"
        id="image-canvas-runtime-panel"
        role="tabpanel"
        aria-labelledby="image-canvas-runtime-tab"
      >
        <RuntimeSettingsEditor />
      </div>
    </div>
  </AppLayout>
</template>

<script setup lang="ts">
import { ref } from 'vue'
import { useI18n } from 'vue-i18n'
import AppLayout from '@/components/layout/AppLayout.vue'
import Icon from '@/components/icons/Icon.vue'
import ModelPolicyEditor from '@/components/admin/image-canvas/ModelPolicyEditor.vue'
import RuntimeSettingsEditor from '@/components/admin/image-canvas/RuntimeSettingsEditor.vue'

const { t } = useI18n()
const activeTab = ref<'policy' | 'runtime'>('policy')
</script>
