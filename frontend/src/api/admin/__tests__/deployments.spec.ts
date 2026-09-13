import { beforeEach, describe, expect, it, vi } from 'vitest'

const client = vi.hoisted(() => ({
  get: vi.fn(),
  post: vi.fn()
}))

vi.mock('@/api/client', () => ({ apiClient: client }))

import {
  getDeploymentOperation,
  getDeploymentStatus,
  startDeploymentAction
} from '@/api/admin/deployments'

describe('deployment admin API', () => {
  beforeEach(() => {
    vi.clearAllMocks()
  })

  it('loads status and a persisted operation', async () => {
    client.get.mockResolvedValueOnce({ data: { components: {} } })
    client.get.mockResolvedValueOnce({ data: { id: 'deploy-one' } })

    await expect(getDeploymentStatus()).resolves.toEqual({ components: {} })
    await expect(getDeploymentOperation('deploy-one')).resolves.toEqual({ id: 'deploy-one' })
    expect(client.get).toHaveBeenNthCalledWith(1, '/admin/deployments')
    expect(client.get).toHaveBeenNthCalledWith(2, '/admin/deployments/operations/deploy-one')
  })

  it('sends only an empty body and an exact confirmation header', async () => {
    client.post.mockResolvedValue({ data: { id: 'deploy-one', state: 'queued' } })

    await startDeploymentAction('canvas', 'update')

    expect(client.post).toHaveBeenCalledWith(
      '/admin/deployments/canvas/update',
      {},
      { headers: { 'X-Deployment-Confirmation': 'canvas:update' } }
    )
  })
})
