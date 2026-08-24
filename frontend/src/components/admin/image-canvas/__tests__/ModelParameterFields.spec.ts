import { mount, type VueWrapper } from '@vue/test-utils'
import { describe, expect, it, vi } from 'vitest'

import ModelParameterFields from '../ModelParameterFields.vue'
import type { ImageCanvasCapability, ImageCanvasModelParameters } from '@/api/admin/imageCanvas'

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
  qualities: ['auto', 'high'],
  output_formats: ['png', 'jpeg', 'webp'],
  backgrounds: ['transparent', 'opaque', 'auto'],
  output_compression: true
}

function mountFields(modelValue: ImageCanvasModelParameters) {
  return mount(ModelParameterFields, {
    props: {
      model: 'gpt-image-1',
      modelValue,
      capability
    }
  })
}

function selectFor(wrapper: VueWrapper, labelKey: string) {
  const label = wrapper.findAll('label').find((item) => item.text().includes(labelKey))
  expect(label).toBeDefined()
  return label!.find('select')
}

function lastUpdate(wrapper: VueWrapper): ImageCanvasModelParameters {
  const updates = wrapper.emitted<ImageCanvasModelParameters[]>('update:modelValue')
  expect(updates).toBeDefined()
  return updates![updates!.length - 1][0]
}

describe('ModelParameterFields', () => {
  it('replaces a transparent background when the output format changes to JPEG', async () => {
    const wrapper = mountFields({
      size: '1024x1024',
      output_format: 'png',
      background: 'transparent'
    })

    await selectFor(wrapper, 'admin.imageCanvas.format').setValue('jpeg')

    const update = lastUpdate(wrapper)
    expect(update).toEqual({
      size: '1024x1024',
      output_format: 'jpeg',
      background: 'opaque'
    })

    await wrapper.setProps({ modelValue: update })
    const backgroundOptions = selectFor(wrapper, 'admin.imageCanvas.background')
      .findAll('option')
      .map((option) => option.attributes('value'))
    expect(backgroundOptions).not.toContain('transparent')
  })

  it('keeps compression numeric for JPEG and clears it for PNG', async () => {
    const wrapper = mountFields({
      output_format: 'jpeg',
      background: 'opaque',
      output_compression: 80
    })

    const compression = wrapper.find('input[type="number"]')
    expect(compression.exists()).toBe(true)
    expect((compression.element as HTMLInputElement).value).toBe('80')

    await compression.setValue('55')
    expect(lastUpdate(wrapper).output_compression).toBe(55)

    await selectFor(wrapper, 'admin.imageCanvas.format').setValue('png')
    const update = lastUpdate(wrapper)
    expect(update.output_format).toBe('png')
    expect(update).not.toHaveProperty('output_compression')

    await wrapper.setProps({ modelValue: update })
    expect(wrapper.find('input[type="number"]').exists()).toBe(false)
  })
})
