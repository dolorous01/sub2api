import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import RuntimeSettingsEditor from '../RuntimeSettingsEditor.vue'
import type {
  ImageCanvasCapability,
  ImageCanvasRuntimeResponse,
  ImageCanvasRuntimeSettings
} from '@/api/admin/imageCanvas'

const mocks = vi.hoisted(() => ({
  getRuntimeSettings: vi.fn(),
  updateRuntimeSettings: vi.fn(),
  showError: vi.fn(),
  showSuccess: vi.fn()
}))

vi.mock('@/api/admin/imageCanvas', () => ({
  getImageCanvasRuntimeSettings: mocks.getRuntimeSettings,
  updateImageCanvasRuntimeSettings: mocks.updateRuntimeSettings
}))

vi.mock('@/stores/app', () => ({
  useAppStore: () => ({
    showError: mocks.showError,
    showSuccess: mocks.showSuccess
  })
}))

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({
      t: (key: string) => key
    })
  }
})

const capability: ImageCanvasCapability = {
  provider: 'openai',
  dimension_mode: 'size',
  generation: true,
  edit: true,
  multi_image: true,
  mask: true,
  max_input_images: 16,
  max_outputs: 4,
  sizes: ['1024x1024'],
  output_formats: ['png', 'jpeg', 'webp'],
  backgrounds: ['transparent', 'opaque', 'auto'],
  output_compression: true,
  defaults: {
    size: '1024x1024',
    output_format: 'png'
  }
}

function settings(workerConcurrency: number): ImageCanvasRuntimeSettings {
  return {
    worker_concurrency: workerConcurrency,
    max_active_jobs_per_user: 8,
    task_timeout_seconds: 900,
    max_outputs_per_job: 4,
    max_input_images: 8,
    result_ttl_seconds: 86400,
    default_overrides: [],
    custom_presets: []
  }
}

function response(currentConcurrency = 4, defaultConcurrency = 2): ImageCanvasRuntimeResponse {
  return {
    settings: settings(currentConcurrency),
    defaults: settings(defaultConcurrency),
    storage: {
      enabled: true,
      driver: 'local',
      location: '/var/lib/sub2api/image-canvas',
      prefix: 'generated/',
      max_object_bytes: 20 * 1024 * 1024,
      generated_assets_permanent: true,
      runtime_changes_need_restart: false
    },
    worker: {
      started: true,
      target_workers: currentConcurrency,
      running_workers: currentConcurrency
    },
    available_models: [{
      model: 'gpt-image-1',
      enabled: true,
      position: 0,
      capability
    }]
  }
}

function buttonFor(wrapper: VueWrapper, text: string) {
  const button = wrapper.findAll('button').find((item) => item.text().includes(text))
  expect(button).toBeDefined()
  return button!
}

async function mountEditor() {
  const wrapper = mount(RuntimeSettingsEditor, {
    global: {
      stubs: {
        Icon: true,
        ModelParameterFields: true
      }
    }
  })
  await flushPromises()
  return wrapper
}

beforeEach(() => {
  vi.clearAllMocks()
  mocks.getRuntimeSettings.mockResolvedValue(response())
  mocks.updateRuntimeSettings.mockImplementation(async (input: ImageCanvasRuntimeSettings) => ({
    ...response(input.worker_concurrency),
    settings: JSON.parse(JSON.stringify(input)) as ImageCanvasRuntimeSettings
  }))
})

describe('RuntimeSettingsEditor', () => {
  it('loads the runtime settings and keeps save disabled before edits', async () => {
    const wrapper = await mountEditor()

    expect(mocks.getRuntimeSettings).toHaveBeenCalledTimes(1)
    const limits = wrapper.findAll('input[type="number"]')
    expect(limits.map((input) => (input.element as HTMLInputElement).value)).toEqual([
      '4',
      '8',
      '900',
      '4',
      '8',
      '86400'
    ])
    expect(buttonFor(wrapper, 'common.save').attributes('disabled')).toBeDefined()
  })

  it('submits an edited setting and adopts the saved response as the new baseline', async () => {
    const wrapper = await mountEditor()
    const concurrency = wrapper.findAll('input[type="number"]')[0]

    await concurrency.setValue('6')
    const save = buttonFor(wrapper, 'common.save')
    expect(save.attributes('disabled')).toBeUndefined()

    await save.trigger('click')
    await flushPromises()

    expect(mocks.updateRuntimeSettings).toHaveBeenCalledTimes(1)
    expect(mocks.updateRuntimeSettings).toHaveBeenCalledWith(expect.objectContaining({
      worker_concurrency: 6,
      default_overrides: [],
      custom_presets: []
    }))
    expect(mocks.showSuccess).toHaveBeenCalledWith('common.saved')
    expect(buttonFor(wrapper, 'common.save').attributes('disabled')).toBeDefined()
  })

  it('restores the server-provided startup defaults without saving immediately', async () => {
    const wrapper = await mountEditor()

    await buttonFor(wrapper, 'admin.imageCanvas.restoreDefaults').trigger('click')

    const concurrency = wrapper.findAll('input[type="number"]')[0]
    expect((concurrency.element as HTMLInputElement).value).toBe('2')
    expect(mocks.updateRuntimeSettings).not.toHaveBeenCalled()
    expect(buttonFor(wrapper, 'common.save').attributes('disabled')).toBeUndefined()

    await buttonFor(wrapper, 'common.save').trigger('click')
    await flushPromises()
    expect(mocks.updateRuntimeSettings).toHaveBeenCalledWith(expect.objectContaining({ worker_concurrency: 2 }))
  })
})
