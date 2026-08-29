import type { AxiosAdapter, AxiosResponse, InternalAxiosRequestConfig } from 'axios'
import { createPinia, setActivePinia } from 'pinia'
import { afterEach, beforeEach, describe, expect, it } from 'vitest'

import apiClient from '@/api/client'
import { createCanvasHostContext } from './canvasHostBridge'

describe('canvasHostBridge request serialization', () => {
  const originalAdapter = apiClient.defaults.adapter

  beforeEach(() => {
    setActivePinia(createPinia())
    localStorage.clear()
  })

  afterEach(() => {
    apiClient.defaults.adapter = originalAdapter
  })

  it('keeps FormData intact and does not force application/json', async () => {
    let captured: InternalAxiosRequestConfig | undefined
    apiClient.defaults.adapter = (async (config) => {
      captured = config
      return {
        config,
        data: { id: 'asset-1' },
        headers: {},
        status: 200,
        statusText: 'OK'
      } satisfies AxiosResponse
    }) as AxiosAdapter

    const form = new FormData()
    form.append('file', new File(['image'], 'sample.png', { type: 'image/png' }))

    const host = createCanvasHostContext('user', () => undefined)
    await host.request('POST', '/image-canvas/assets', form)

    expect(captured?.data).toBe(form)
    expect(captured?.headers.get('Content-Type')).not.toBe('application/json')
  })

  it('still serializes ordinary objects as JSON', async () => {
    let captured: InternalAxiosRequestConfig | undefined
    apiClient.defaults.adapter = (async (config) => {
      captured = config
      return {
        config,
        data: { id: 'project-1' },
        headers: {},
        status: 200,
        statusText: 'OK'
      } satisfies AxiosResponse
    }) as AxiosAdapter

    const host = createCanvasHostContext('user', () => undefined)
    await host.request('POST', '/image-canvas/projects', { name: 'Draft' })

    expect(captured?.data).toBe(JSON.stringify({ name: 'Draft' }))
    expect(captured?.headers.get('Content-Type')).toContain('application/json')
  })
})
