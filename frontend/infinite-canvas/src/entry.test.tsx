import { act } from 'react'
import { describe, expect, it, vi } from 'vitest'
import type { CanvasHostContext } from './host-context'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT: boolean }).IS_REACT_ACT_ENVIRONMENT = true

vi.mock('@/pages/canvas', () => ({
  default: () => <div data-canvas-test-root />
}))

vi.mock('@/pages/canvas/project', () => ({
  default: () => <div data-canvas-project-test-root />
}))

vi.mock('@sub2api/adapters/use-canvas-store', () => ({
  initializeCanvasProjectStore: vi.fn().mockResolvedValue(undefined),
  resetCanvasProjectStore: vi.fn()
}))

vi.mock('@sub2api/adapters/use-config-store', () => ({
  initializeCanvasConfigStore: vi.fn().mockResolvedValue(undefined),
  resetCanvasConfigStore: vi.fn()
}))

vi.mock('@sub2api/adapters/asset-runtime', () => ({
  resetCanvasAssetRuntime: vi.fn()
}))

import { mountCanvas } from './entry'

function hostContext(): CanvasHostContext {
  return {
    apiBaseURL: '/api/v1',
    locale: 'en',
    theme: 'light',
    routeMode: 'user',
    request: async () => undefined as never,
    stream: async () => new ReadableStream<Uint8Array>(),
    navigate: vi.fn(),
    notify: vi.fn()
  }
}

describe('mountCanvas', () => {
  it('updates context and unmounts without leaving a React root', async () => {
    const element = document.createElement('div')
    let handle: ReturnType<typeof mountCanvas>

    await act(async () => {
      handle = mountCanvas(element, hostContext())
    })
    expect(element.querySelector('[data-canvas-test-root]')).not.toBeNull()

    await act(async () => {
      handle.updateContext({ ...hostContext(), locale: 'zh-CN', theme: 'dark' })
      handle.unmount()
    })
    expect(element.childElementCount).toBe(0)
    expect(() => handle.unmount()).not.toThrow()
  })
})
