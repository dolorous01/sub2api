import { mount } from '@vue/test-utils'
import { describe, expect, it, vi } from 'vitest'

import BackendLogicPanel from '../BackendLogicPanel.vue'

vi.mock('vue-i18n', () => ({
  useI18n: () => ({ t: (key: string) => key })
}))

describe('BackendLogicPanel', () => {
  it('shows the execution chain, data objects, and parameterized SQL', async () => {
    const wrapper = mount(BackendLogicPanel, {
      global: { stubs: { Icon: true } }
    })

    expect(wrapper.text()).toContain('ImageCanvasEnabled middleware')
    expect(wrapper.text()).toContain('ImageJobWorker')

    const controls = wrapper.findAll('button[aria-pressed]')
    await controls[1].trigger('click')
    expect(wrapper.text()).toContain('image_model_policy_audits')
    expect(wrapper.text()).toContain('image_jobs')

    await controls[2].trigger('click')
    expect(wrapper.get('pre').text()).toContain('FROM accounts')
    expect(wrapper.get('pre').text()).toContain('account_groups')
  })
})
