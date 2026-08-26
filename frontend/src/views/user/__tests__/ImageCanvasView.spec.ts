import { mount } from '@vue/test-utils'
import { describe, expect, it } from 'vitest'

import ImageCanvasView from '../ImageCanvasView.vue'

describe('ImageCanvasView', () => {
  it('uses a dedicated full-viewport Studio layout', () => {
    const wrapper = mount(ImageCanvasView, {
      global: { stubs: { ImageCanvasHost: { template: '<div data-testid="canvas-host" />' } } }
    })

    expect(wrapper.find('main').classes()).toContain('h-dvh')
    expect(wrapper.find('[data-testid="canvas-host"]').exists()).toBe(true)
    expect(wrapper.findComponent({ name: 'AppLayout' }).exists()).toBe(false)
  })
})
