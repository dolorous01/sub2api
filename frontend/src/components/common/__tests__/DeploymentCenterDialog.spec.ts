import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import DeploymentCenterDialog from '@/components/common/DeploymentCenterDialog.vue'
import type { DeploymentOperation, DeploymentStatus } from '@/api/admin/deployments'

const mocks = vi.hoisted(() => ({
  getStatus: vi.fn(),
  getOperation: vi.fn(),
  startAction: vi.fn()
}))

vi.mock('@/api/admin/deployments', () => ({
  getDeploymentStatus: mocks.getStatus,
  getDeploymentOperation: mocks.getOperation,
  startDeploymentAction: mocks.startAction
}))

vi.mock('vue-i18n', () => ({
  useI18n: () => ({
    locale: { value: 'zh-CN' },
    t: (key: string, params?: Record<string, unknown>) =>
      params ? `${key}:${JSON.stringify(params)}` : key
  })
}))

const operation: DeploymentOperation = {
  id: `deploy-${'a'.repeat(32)}`,
  component: 'sub2api',
  action: 'update',
  state: 'queued',
  stage: 'queued',
  requested_at: '2026-09-12T08:00:00Z',
  deployment_succeeded: false,
  reconciliation: { required: true, state: 'pending' }
}

const status: DeploymentStatus = {
  components: {
    sub2api: {
      component: 'sub2api',
      current_version: '0.1.179',
      target_version: '0.1.180-operator.1',
      update_available: true,
      update_enabled: true,
      rollback_enabled: false,
      deployment_mode: 'blue_green',
      last_operation: null
    },
    canvas: {
      component: 'canvas',
      current_version: '0.1.0',
      target_version: '0.2.0',
      update_available: false,
      update_enabled: false,
      update_block_reason: 'release_not_approved',
      rollback_enabled: true,
      deployment_mode: 'stable',
      last_operation: null
    }
  },
  active_operation: null,
  latest_operation: null,
  reconciliation: {
    automatic_after_update: true,
    plaintext_key_accessed: false,
    mutation_performed: false
  }
}

function mountDialog() {
  return mount(DeploymentCenterDialog, {
    props: { show: true },
    global: {
      stubs: {
        BaseDialog: {
          props: ['show', 'title'],
          template: '<div v-if="show" data-testid="dialog"><slot /></div>'
        },
        OperationStatus: {
          props: ['operation'],
          template: '<div data-testid="operation-status">{{ operation.state }}</div>'
        },
        Icon: { template: '<i />' }
      }
    }
  })
}

describe('DeploymentCenterDialog', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    mocks.getStatus.mockResolvedValue(structuredClone(status))
    mocks.startAction.mockResolvedValue(structuredClone(operation))
  })

  it('renders Sub2API on the left and Canvas on the right at desktop breakpoints', async () => {
    const wrapper = mountDialog()
    await flushPromises()

    const grid = wrapper.find('.md\\:grid-cols-2')
    expect(grid.exists()).toBe(true)
    const cards = grid.findAll('section')
    expect(cards).toHaveLength(2)
    expect(cards[0].attributes('data-component')).toBe('sub2api')
    expect(cards[1].attributes('data-component')).toBe('canvas')
    expect(cards[0].text()).toContain('v0.1.179')
    expect(cards[1].text()).toContain('v0.1.0')
  })

  it('starts only the selected component action without accepting a target from the UI', async () => {
    const wrapper = mountDialog()
    await flushPromises()

    await wrapper.find('[data-component="sub2api"] [data-action="update"]').trigger('click')
    expect(wrapper.find('[data-testid="confirm-deployment"]').exists()).toBe(true)
    await wrapper.find('[data-testid="confirm-deployment"]').trigger('click')
    await flushPromises()

    expect(mocks.startAction).toHaveBeenCalledTimes(1)
    expect(mocks.startAction).toHaveBeenCalledWith('sub2api', 'update')
    expect(wrapper.find('[data-component="sub2api"] [data-testid="operation-status"]').text()).toBe('queued')
    wrapper.unmount()
  })

  it('disables a component whose release is not approved while leaving rollback independent', async () => {
    const wrapper = mountDialog()
    await flushPromises()

    const canvas = wrapper.find('[data-component="canvas"]')
    expect(canvas.find('[data-action="update"]').attributes('disabled')).toBeDefined()
    expect(canvas.find('[data-action="rollback"]').attributes('disabled')).toBeUndefined()
    expect(canvas.text()).toContain('deployment.blockReasons.release_not_approved')
  })
})
