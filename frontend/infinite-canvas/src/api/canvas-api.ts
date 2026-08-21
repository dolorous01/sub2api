import localforage from 'localforage'
import { nanoid } from 'nanoid'
import type { CanvasHostContext } from '@sub2api/host-context'

export interface CanvasAPIKey {
  id: number
  name: string
  group_id: number | null
  group_name: string
}

export interface CanvasConfig {
  api_keys: CanvasAPIKey[]
  selected_api_key_id?: number
}

export interface CanvasDocument {
  schema_version: 1
  nodes: CanvasNodeDocument[]
  edges: CanvasEdgeDocument[]
  viewport?: { x: number; y: number; k: number }
}

export interface CanvasNodeDocument {
  id: string
  type: 'image' | 'text' | 'config' | 'group'
  position: { x: number; y: number }
  size?: { width: number; height: number }
  prompt?: string
  text?: string
  asset_id?: string
  metadata?: Record<string, unknown>
}

export interface CanvasEdgeDocument {
  id: string
  source: string
  target: string
}

export interface CanvasProject {
  id: string
  name: string
  document: CanvasDocument
  version: number
  created_at: string
  updated_at: string
}

export interface CanvasImageRequest {
  api_key_id: number
  model: string
  prompt: string
  n: number
  size: string
}

export interface CanvasGeneratedAsset {
  asset_id: string
  revised_prompt?: string
}

export interface CanvasAssetSource {
  url: string
  revoke: boolean
}

export interface CanvasAPI {
  getConfig(): Promise<CanvasConfig>
  uploadAsset(file: File): Promise<{ id: string }>
  generate(input: CanvasImageRequest, signal?: AbortSignal): Promise<CanvasGeneratedAsset[]>
  edit(input: CanvasImageRequest & { asset_id: string }, signal?: AbortSignal): Promise<CanvasGeneratedAsset[]>
  getAssetSource(id: string, signal?: AbortSignal): Promise<CanvasAssetSource>
}

const canvasNodeTypes = new Set<CanvasNodeDocument['type']>(['image', 'text', 'config', 'group'])

export function isCanvasDocument(value: unknown): value is CanvasDocument {
  if (!isRecord(value) || value.schema_version !== 1 || !Array.isArray(value.nodes) || !Array.isArray(value.edges)) return false
  if (value.nodes.length > 5000 || value.edges.length > 10000) return false

  const nodeIDs = new Set<string>()
  for (const candidate of value.nodes) {
    if (!isCanvasNode(candidate) || nodeIDs.has(candidate.id)) return false
    nodeIDs.add(candidate.id)
  }

  const edgeIDs = new Set<string>()
  for (const candidate of value.edges) {
    if (!isCanvasEdge(candidate) || edgeIDs.has(candidate.id)) return false
    if (!nodeIDs.has(candidate.source) || !nodeIDs.has(candidate.target)) return false
    edgeIDs.add(candidate.id)
  }

  if (value.viewport !== undefined && !isViewport(value.viewport)) return false
  return true
}

interface StoredAsset {
  id: string
  kind: 'blob' | 'url'
  blob?: Blob
  url?: string
  mime_type: string
  name: string
  created_at: string
}

interface APIKeyRecord {
  id: number
  name: string
  group_id: number | null
  status: string
  group?: { name?: string } | null
}

interface ImagesResponse {
  data?: Array<{ b64_json?: string; url?: string; revised_prompt?: string }>
}

type APIEnvelope<T> = { code: number; message: string; data: T }

function unwrap<T>(value: T | APIEnvelope<T>): T {
  if (value && typeof value === 'object' && 'data' in value && 'code' in value) {
    return (value as APIEnvelope<T>).data
  }
  return value as T
}

export function createCanvasAPI(host: CanvasHostContext): CanvasAPI {
  const scope = host.storageScope.replace(/[^a-zA-Z0-9_-]/g, '_') || 'anonymous'
  const assets = localforage.createInstance({ name: `sub2api-infinite-canvas-${scope}`, storeName: 'assets' })
  const request = async <T>(method: string, path: string, body?: unknown, headers?: Record<string, string>, signal?: AbortSignal) =>
    unwrap(await host.request<T | APIEnvelope<T>>(method, path, body, headers, signal))

  const persistImages = async (response: ImagesResponse): Promise<CanvasGeneratedAsset[]> => {
    const output: CanvasGeneratedAsset[] = []
    for (const item of response.data || []) {
      const asset = await imageResponseAsset(item)
      if (!asset) continue
      await assets.setItem(asset.id, asset)
      output.push({ asset_id: asset.id, revised_prompt: item.revised_prompt })
    }
    if (!output.length) throw new Error('Image provider returned no usable image')
    return output
  }

  return {
    async getConfig() {
      const page = await request<{ items: APIKeyRecord[] }>('GET', '/keys?page=1&page_size=100&status=active')
      const apiKeys = (page.items || [])
        .filter((key) => key.status === 'active')
        .map((key) => ({
          id: key.id,
          name: key.name,
          group_id: key.group_id,
          group_name: key.group?.name || 'Ungrouped'
        }))
      return { api_keys: apiKeys, selected_api_key_id: apiKeys[0]?.id }
    },
    async uploadAsset(file) {
      const asset: StoredAsset = {
        id: `asset_${nanoid()}`,
        kind: 'blob',
        blob: file.slice(0, file.size, file.type || 'application/octet-stream'),
        mime_type: file.type || 'application/octet-stream',
        name: file.name,
        created_at: new Date().toISOString()
      }
      await assets.setItem(asset.id, asset)
      return { id: asset.id }
    },
    async generate(input, signal) {
      const response = await request<ImagesResponse>('POST', '/image-canvas/generations', imageRequestBody(input), apiKeyHeader(input.api_key_id), signal)
      return persistImages(response)
    },
    async edit(input, signal) {
      const source = await assets.getItem<StoredAsset>(input.asset_id)
      if (!source) throw new Error('The selected image is no longer available in this browser')
      let body: FormData | Record<string, unknown>
      if (source.kind === 'blob' && source.blob) {
        const form = new FormData()
        form.append('model', input.model)
        form.append('prompt', input.prompt)
        form.append('n', String(input.n))
        form.append('size', input.size)
        form.append('image', source.blob, source.name || 'canvas-image.png')
        body = form
      } else if (source.kind === 'url' && source.url) {
        body = { ...imageRequestBody(input), images: [{ image_url: source.url }] }
      } else {
        throw new Error('The selected image data is invalid')
      }
      const response = await request<ImagesResponse>('POST', '/image-canvas/edits', body, apiKeyHeader(input.api_key_id), signal)
      return persistImages(response)
    },
    async getAssetSource(id, signal) {
      if (signal?.aborted) throw new DOMException('Aborted', 'AbortError')
      const asset = await assets.getItem<StoredAsset>(id)
      if (!asset) throw new Error('Canvas image not found in this browser')
      if (asset.kind === 'url' && asset.url) return { url: asset.url, revoke: false }
      if (asset.kind === 'blob' && asset.blob) return { url: URL.createObjectURL(asset.blob), revoke: true }
      throw new Error('Canvas image data is invalid')
    }
  }
}

function imageRequestBody(input: CanvasImageRequest): Record<string, unknown> {
  return {
    model: input.model.trim(),
    prompt: input.prompt.trim(),
    n: input.n,
    size: input.size
  }
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value)
}

function isFiniteNumber(value: unknown): value is number {
  return typeof value === 'number' && Number.isFinite(value)
}

function isOptionalString(value: unknown): boolean {
  return value === undefined || typeof value === 'string'
}

function isPosition(value: unknown): value is { x: number; y: number } {
  return isRecord(value) && isFiniteNumber(value.x) && isFiniteNumber(value.y)
}

function isViewport(value: unknown): value is { x: number; y: number; k: number } {
  return isRecord(value) && isFiniteNumber(value.x) && isFiniteNumber(value.y) && isFiniteNumber(value.k) && value.k > 0
}

function isCanvasNode(value: unknown): value is CanvasNodeDocument {
  if (!isRecord(value) || typeof value.id !== 'string' || !value.id || !canvasNodeTypes.has(value.type as CanvasNodeDocument['type'])) return false
  if (!isPosition(value.position)) return false
  if (value.size !== undefined) {
    if (!isRecord(value.size) || !isFiniteNumber(value.size.width) || !isFiniteNumber(value.size.height)) return false
    if (value.size.width <= 0 || value.size.height <= 0) return false
  }
  if (!isOptionalString(value.prompt) || !isOptionalString(value.text) || !isOptionalString(value.asset_id)) return false
  return value.metadata === undefined || isRecord(value.metadata)
}

function isCanvasEdge(value: unknown): value is CanvasEdgeDocument {
  return isRecord(value) && typeof value.id === 'string' && value.id.length > 0 &&
    typeof value.source === 'string' && value.source.length > 0 &&
    typeof value.target === 'string' && value.target.length > 0
}

function apiKeyHeader(id: number): Record<string, string> {
  return { 'X-Sub2API-Key-ID': String(id) }
}

async function imageResponseAsset(item: { b64_json?: string; url?: string }): Promise<StoredAsset | undefined> {
  const id = `asset_${nanoid()}`
  const now = new Date().toISOString()
  if (item.b64_json) {
    const blob = decodeImageBase64(item.b64_json)
    return { id, kind: 'blob', blob, mime_type: blob.type, name: imageFileName(id, blob.type), created_at: now }
  }
  const url = item.url?.trim()
  if (!url) return undefined
  if (url.startsWith('data:')) {
    const blob = decodeImageBase64(url)
    return { id, kind: 'blob', blob, mime_type: blob.type, name: imageFileName(id, blob.type), created_at: now }
  }
  return { id, kind: 'url', url, mime_type: 'image/*', name: `${id}.image`, created_at: now }
}

function decodeImageBase64(value: string): Blob {
  const match = value.match(/^data:([^;,]+);base64,(.*)$/s)
  const encoded = match ? match[2] : value
  const binary = atob(encoded.replace(/\s/g, ''))
  const bytes = new Uint8Array(binary.length)
  for (let index = 0; index < binary.length; index += 1) bytes[index] = binary.charCodeAt(index)
  const mime = match?.[1] || detectImageMIME(bytes)
  return new Blob([bytes], { type: mime })
}

function detectImageMIME(bytes: Uint8Array): string {
  if (bytes[0] === 0xff && bytes[1] === 0xd8) return 'image/jpeg'
  if (bytes[0] === 0x52 && bytes[1] === 0x49 && bytes[2] === 0x46 && bytes[3] === 0x46) return 'image/webp'
  return 'image/png'
}

function imageFileName(id: string, mime: string): string {
  if (mime === 'image/jpeg') return `${id}.jpg`
  if (mime === 'image/webp') return `${id}.webp`
  return `${id}.png`
}
