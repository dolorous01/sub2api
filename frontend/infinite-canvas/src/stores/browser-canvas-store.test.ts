import { beforeEach, describe, expect, it, vi } from 'vitest'
import { createBrowserCanvasStore } from './browser-canvas-store'

describe('createBrowserCanvasStore', () => {
  beforeEach(() => {
    localStorage.clear()
    vi.useRealTimers()
  })

  it('persists project documents in a user-scoped browser key', async () => {
    vi.useFakeTimers()
    const store = createBrowserCanvasStore('user-42')
    await store.hydrate()
    const project = await store.createProject('Draft')
    store.updateDocument(project.id, {
      schema_version: 1,
      nodes: [{ id: 'node-1', type: 'text', position: { x: 10, y: 20 }, text: 'hello' }],
      edges: []
    })
    await vi.advanceTimersByTimeAsync(300)

    const restored = createBrowserCanvasStore('user-42')
    await restored.hydrate()

    expect(restored.getState().projectsByID[project.id].document.nodes[0]).toMatchObject({ id: 'node-1', text: 'hello' })
  })

  it('does not expose one user canvas to another user', async () => {
    const first = createBrowserCanvasStore('user-1')
    await first.hydrate()
    await first.createProject('Private draft')

    const second = createBrowserCanvasStore('user-2')
    await second.hydrate()

    expect(second.getState().order).toEqual([])
  })

  it('ignores malformed browser projects instead of hydrating a broken canvas', async () => {
    localStorage.setItem('sub2api:infinite-canvas:projects:user-42:v1', JSON.stringify({
      schema_version: 1,
      order: ['broken'],
      projects: [{
        id: 'broken', name: 'Broken', version: 1,
        created_at: '2026-08-21T00:00:00Z', updated_at: '2026-08-21T00:00:00Z',
        document: { schema_version: 1, nodes: [{ id: 'node-1', type: 'image' }], edges: [] }
      }]
    }))
    const store = createBrowserCanvasStore('user-42')

    await store.hydrate()

    expect(store.getState().order).toEqual([])
  })
})
