import type { CanvasHostContextContract } from './canvasLoader'
import apiClient from '@/api/client'
import { buildApiUrl, getAPIBaseURL } from '@/api/url'
import { useAppStore } from '@/stores/app'
import { getLocale } from '@/i18n'

function authHeaders(): Record<string, string> {
  const token = localStorage.getItem('auth_token')
  return token ? { Authorization: `Bearer ${token}` } : {}
}

function toError(value: unknown, fallback = 'Canvas request failed'): Error & { status?: number; code?: string } {
  const source = value as { message?: string; name?: string; status?: number; code?: string } | null
  if (source?.code === 'ERR_CANCELED' || source?.name === 'CanceledError') {
    const error = new Error('Aborted') as Error & { status?: number; code?: string }
    error.name = 'AbortError'
    return error
  }
  const error = new Error(source?.message || fallback) as Error & { status?: number; code?: string }
  error.status = source?.status
  error.code = source?.code
  return error
}

async function request<T>(
  method: string,
  path: string,
  body?: unknown,
  headers: Record<string, string> = {},
  options?: { signal?: AbortSignal; timeoutMs?: number }
): Promise<T> {
  const requestHeaders = { ...headers }
  if (typeof FormData !== 'undefined' && body instanceof FormData) delete requestHeaders['Content-Type']
  try {
    const response = await apiClient.request<T>({
      method,
      url: path,
      data: body,
      headers: requestHeaders,
      ...(options?.signal ? { signal: options.signal } : {}),
      ...(options?.timeoutMs ? { timeout: options.timeoutMs } : {})
    })
    return response.data
  } catch (error) {
    throw toError(error)
  }
}

async function stream(path: string, init: RequestInit = {}): Promise<ReadableStream<Uint8Array>> {
  const headers = new Headers(init.headers)
  for (const [key, value] of Object.entries(authHeaders())) headers.set(key, value)
  headers.set('Accept', 'text/event-stream, application/octet-stream')
  let response = await fetch(buildApiUrl(path), { ...init, headers, credentials: 'same-origin' })
  // Let the existing Axios interceptor refresh an expiring session, then retry
  // the stream once with the refreshed access token.
  if (response.status === 401 && localStorage.getItem('refresh_token')) {
    try {
      await apiClient.get('/user/profile')
      for (const [key, value] of Object.entries(authHeaders())) headers.set(key, value)
      response = await fetch(buildApiUrl(path), { ...init, headers, credentials: 'same-origin' })
    } catch {
      // Preserve the original unauthorized response below.
    }
  }
  if (!response.ok || !response.body) {
    let message = `Canvas stream failed (${response.status})`
    try {
      const payload = await response.json() as { message?: string; code?: string }
      message = payload.message || message
    } catch {
      // The server may return an empty body for a stream failure.
    }
    const error = toError({ status: response.status, message })
    throw error
  }
  return response.body
}

export function createCanvasHostContext(routeMode: 'user' | 'admin', navigate: (path: string) => void, theme: 'light' | 'dark' = 'light'): CanvasHostContextContract {
  const appStore = useAppStore()
  return {
    apiBaseURL: getAPIBaseURL(),
    locale: getLocale(),
    theme,
    routeMode,
    request,
    stream,
    navigate,
    notify(level, message) {
      if (level === 'success') appStore.showSuccess(message)
      else if (level === 'warning') appStore.showWarning(message)
      else if (level === 'error') appStore.showError(message)
      else appStore.showInfo(message)
    }
  }
}
