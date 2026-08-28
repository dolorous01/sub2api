import { defineComponent } from 'vue'
import { mount } from '@vue/test-utils'
import { describe, expect, it, vi } from 'vitest'

import ImageCanvasAdminView from '../ImageCanvasAdminView.vue'

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({ t: (key: string) => key })
  }
})

const RouterLinkStub = defineComponent({
  props: { to: { type: String, required: true } },
  template: '<a :href="to"><slot /></a>'
})

describe('admin ImageCanvasAdminView', () => {
  it('keeps creation out of the admin route and links to Studio explicitly', async () => {
    const wrapper = mount(ImageCanvasAdminView, {
      global: {
        stubs: {
          AppLayout: { template: '<main><slot /></main>' },
          Icon: true,
          ModelPolicyEditor: { template: '<div data-test="policy-editor" />' },
          RuntimeSettingsEditor: { template: '<div data-test="runtime-editor" />' },
          RecentImageJobsPanel: { template: '<div data-test="recent-jobs" />' },
          BackendLogicPanel: { template: '<div data-test="backend-logic" />' },
          RouterLink: RouterLinkStub
        }
      }
    })

    expect(wrapper.findAll('[role="tab"]')).toHaveLength(2)
    expect(wrapper.find('#image-canvas-preview-tab').exists()).toBe(false)
    expect(wrapper.get('a[href="/studio"]').text()).toContain('admin.imageCanvas.openStudio')
    expect(wrapper.find('[data-test="policy-editor"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="backend-logic"]').exists()).toBe(true)

    await wrapper.get('#image-canvas-runtime-tab').trigger('click')
    expect(wrapper.find('[data-test="runtime-editor"]').exists()).toBe(true)
  })
})
