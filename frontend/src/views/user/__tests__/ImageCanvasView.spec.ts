import { mount } from '@vue/test-utils'
import { describe, expect, it } from 'vitest'

import ImageCanvasView from '../ImageCanvasView.vue'

describe('ImageCanvasView', () => {
  it('fills the area below the global navigation without a card container', () => {
    const wrapper = mount(ImageCanvasView, {
      global: {
        stubs: {
          AppLayout: { template: '<div data-testid="app-layout"><slot /></div>' },
          ImageCanvasHost: { template: '<div data-testid="canvas-host" />' }
        }
      }
    })

    const canvasArea = wrapper.find('[data-testid="app-layout"] > div')
    expect(canvasArea.classes()).toContain('h-[calc(100dvh-4rem)]')
    expect(canvasArea.classes()).toContain('-m-4')
    expect(wrapper.find('[data-testid="canvas-host"]').exists()).toBe(true)
    expect(wrapper.find('[data-testid="app-layout"]').exists()).toBe(true)
  })
})
