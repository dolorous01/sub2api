import { beforeEach, describe, expect, it, vi } from 'vitest'
import type { CanvasHostContext } from '@sub2api/host-context'
import { createCanvasAPI, isCanvasDocument } from './canvas-api'

const assetSet = vi.hoisted(() => vi.fn())

vi.mock('localforage', () => ({
  default: {
    createInstance: () => ({ setItem: assetSet, getItem: vi.fn() })
  }
}))

function hostContext(request: CanvasHostContext['request']): CanvasHostContext {
  return {
    apiBaseURL: '/api/v1',
    storageScope: '42',
    locale: 'en',
    theme: 'light',
    routeMode: 'user',
    request,
    navigate: vi.fn(),
    notify: vi.fn()
  }
}

describe('createCanvasAPI', () => {
  beforeEach(() => assetSet.mockReset().mockResolvedValue(undefined))

  it('lists active API keys without exposing their secrets', async () => {
    const request = vi.fn().mockResolvedValue({
      items: [
        { id: 7, name: 'Images', group_id: 3, status: 'active', group: { name: 'OpenAI' } },
        { id: 8, name: 'Disabled', group_id: 3, status: 'disabled' }
      ]
    })

    const config = await createCanvasAPI(hostContext(request as unknown as CanvasHostContext['request'])).getConfig()

    expect(config).toEqual({
      api_keys: [{ id: 7, name: 'Images', group_id: 3, group_name: 'OpenAI' }],
      selected_api_key_id: 7
    })
    expect(request).toHaveBeenCalledWith('GET', '/keys?page=1&page_size=100&status=active', undefined, undefined, undefined)
  })

  it('sends a synchronous image request through the selected API key', async () => {
    const request = vi.fn().mockResolvedValue({ data: [{ b64_json: 'iVBORw0KGgo=' }] })
    const api = createCanvasAPI(hostContext(request as unknown as CanvasHostContext['request']))

    const result = await api.generate({ api_key_id: 7, model: 'gpt-image-1', prompt: 'A quiet desk', n: 1, size: '1024x1024' })

    expect(result).toHaveLength(1)
    expect(request).toHaveBeenCalledWith(
      'POST',
      '/image-canvas/generations',
      { model: 'gpt-image-1', prompt: 'A quiet desk', n: 1, size: '1024x1024' },
      { 'X-Sub2API-Key-ID': '7' },
      undefined
    )
    expect(assetSet).toHaveBeenCalledOnce()
  })

  it('rejects malformed canvas documents before import', () => {
    expect(isCanvasDocument({
      schema_version: 1,
      nodes: [{ id: 'image-1', type: 'image', position: { x: 0, y: 0 } }],
      edges: []
    })).toBe(true)
    expect(isCanvasDocument({
      schema_version: 1,
      nodes: [{ id: 'image-1', type: 'image' }],
      edges: []
    })).toBe(false)
    expect(isCanvasDocument({
      schema_version: 1,
      nodes: [{ id: 'image-1', type: 'image', position: { x: 0, y: 0 } }],
      edges: [{ id: 'edge-1', source: 'image-1', target: 'missing' }]
    })).toBe(false)
  })
})
