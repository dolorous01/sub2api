import { describe, expect, it, vi } from 'vitest'
import type { CanvasHostContext } from '@sub2api/host-context'
import { createCanvasAPI, parseCanvasSSE } from './canvas-api'

function chunkedStream(...chunks: string[]): ReadableStream<Uint8Array> {
  const encoder = new TextEncoder()
  return new ReadableStream({
    start(controller) {
      for (const chunk of chunks) controller.enqueue(encoder.encode(chunk))
      controller.close()
    }
  })
}

describe('parseCanvasSSE', () => {
  it('parses comments, multiline data, and CRLF split across chunks', async () => {
    const stream = chunkedStream(
      ': keepalive\r',
      '\nevent: progress\r\ndata: {"status":"running",\r',
      '\ndata: "completed_count":1}\r\n\r',
      '\nevent: completed\ndata: {"status":"completed"}\n\n'
    )

    const events = []
    for await (const event of parseCanvasSSE(stream)) events.push(event)

    expect(events).toEqual([
      { event: 'progress', data: '{"status":"running",\n"completed_count":1}' },
      { event: 'completed', data: '{"status":"completed"}' }
    ])
  })
})

describe('createCanvasAPI', () => {
  it('creates a job through the host bridge with the idempotency header', async () => {
    const request = vi.fn().mockResolvedValue({ id: 'job-1', status: 'queued' })
    const host = { request } as unknown as CanvasHostContext
    const api = createCanvasAPI(host)

    await api.createJob({
      project_id: 'project-1',
      client_node_id: 'node-1',
      operation: 'generation',
      api_key_id: 42,
      selected_model: 'gpt-image-1',
      prompt: 'Product image',
      input_asset_ids: [],
      parameters: { n: 1, size: '1024x1024' }
    }, 'project-1:node-1:nonce-1')

    expect(request).toHaveBeenCalledWith(
      'POST',
      '/image-canvas/jobs',
      expect.objectContaining({ api_key_id: 42, selected_model: 'gpt-image-1' }),
      { 'Idempotency-Key': 'project-1:node-1:nonce-1' }
    )
  })

  it('uses the authenticated media task routes and idempotency headers', async () => {
    const request = vi.fn().mockResolvedValue({ id: 'media-1', status: 'queued', results: [] })
    const host = { request } as unknown as CanvasHostContext
    const api = createCanvasAPI(host)
    const controller = new AbortController()

    await api.createVideoTask({
      project_id: 'project-1',
      client_node_id: 'node-video',
      api_key_id: 42,
      selected_model: 'grok-imagine-video',
      prompt: 'Ocean at sunrise',
      reference_asset_ids: ['asset-1'],
      parameters: {
        seconds: 6,
        size: '16:9',
        resolution: '720p',
        generate_audio: true,
        watermark: false
      }
    }, 'video-key', controller.signal)
    await api.generateAudio({
      project_id: 'project-1',
      client_node_id: 'node-audio',
      api_key_id: 42,
      selected_model: 'gpt-4o-mini-tts',
      prompt: 'Read this',
      parameters: { voice: 'alloy', format: 'mp3', speed: 1, instructions: '' }
    }, 'audio-key', controller.signal)
    await api.getMediaTask('task/id', controller.signal)
    await api.cancelMediaTask('task/id')

    expect(request).toHaveBeenNthCalledWith(
      1,
      'POST',
      '/image-canvas/media/video/tasks',
      expect.objectContaining({ selected_model: 'grok-imagine-video' }),
      { 'Idempotency-Key': 'video-key' },
      { signal: controller.signal }
    )
    expect(request).toHaveBeenNthCalledWith(
      2,
      'POST',
      '/image-canvas/media/audio',
      expect.objectContaining({ selected_model: 'gpt-4o-mini-tts' }),
      { 'Idempotency-Key': 'audio-key' },
      { signal: controller.signal, timeoutMs: 10 * 60_000 }
    )
    expect(request).toHaveBeenNthCalledWith(
      3,
      'GET',
      '/image-canvas/media/tasks/task%2Fid',
      undefined,
      undefined,
      { signal: controller.signal }
    )
    expect(request).toHaveBeenNthCalledWith(4, 'DELETE', '/image-canvas/media/tasks/task%2Fid', undefined, undefined)
  })
})
