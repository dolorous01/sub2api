import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import VersionBadge from '@/components/common/VersionBadge.vue'

const mocks = vi.hoisted(() => ({
  appStore: {
    versionLoading: false,
    currentVersion: '0.2.4',
    latestVersion: '0.2.5',
    hasUpdate: true,
    releaseInfo: null as null | { html_url: string },
    buildType: 'release',
    fetchVersion: vi.fn(),
    clearVersionCache: vi.fn()
  },
  getRollbackVersions: vi.fn(),
  copyToClipboard: vi.fn()
}))

vi.mock('@/stores', () => ({
  useAuthStore: () => ({ isAdmin: true }),
  useAppStore: () => mocks.appStore
}))

vi.mock('@/api/admin/system', () => ({
  getRollbackVersions: mocks.getRollbackVersions
}))

vi.mock('@/composables/useClipboard', () => ({
  useClipboard: () => ({ copied: { value: false }, copyToClipboard: mocks.copyToClipboard })
}))

vi.mock('vue-i18n', async (importOriginal) => {
  const actual = await importOriginal<typeof import('vue-i18n')>()
  return {
    ...actual,
    useI18n: () => ({ t: (key: string) => key })
  }
})

function mountBadge() {
  return mount(VersionBadge, {
    global: {
      stubs: {
        DeploymentCenterDialog: {
          props: ['show'],
          emits: ['close'],
          template: '<div v-if="show" data-testid="deployment-center" />'
        },
        Icon: { template: '<i />' }
      }
    }
  })
}

async function openVersionMenu(wrapper: ReturnType<typeof mountBadge>) {
  await wrapper.find('button').trigger('click')
  await flushPromises()
}

function buttonWithText(wrapper: ReturnType<typeof mountBadge>, label: string) {
  const button = wrapper.findAll('button').find((candidate) => candidate.text().includes(label))
  if (!button) throw new Error('button not found: ' + label)
  return button
}

describe('VersionBadge controlled deployment entry', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    mocks.appStore.hasUpdate = true
    mocks.appStore.buildType = 'release'
    mocks.getRollbackVersions.mockResolvedValue({ versions: [] })
  })

  it('opens the two-component deployment center instead of the in-place update flow', async () => {
    const wrapper = mountBadge()
    await openVersionMenu(wrapper)

    await buttonWithText(wrapper, 'version.updateNow').trigger('click')

    expect(wrapper.find('[data-testid="deployment-center"]').exists()).toBe(true)
    expect(mocks.getRollbackVersions).not.toHaveBeenCalled()
    wrapper.unmount()
  })

  it('routes the legacy rollback entry to the same controlled deployment center', async () => {
    mocks.appStore.hasUpdate = false
    const wrapper = mountBadge()
    await openVersionMenu(wrapper)

    await buttonWithText(wrapper, 'version.rollback').trigger('click')

    expect(wrapper.find('[data-testid="deployment-center"]').exists()).toBe(true)
    expect(mocks.getRollbackVersions).not.toHaveBeenCalled()
    wrapper.unmount()
  })
})
