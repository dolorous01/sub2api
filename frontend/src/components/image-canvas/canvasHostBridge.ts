import type { CanvasHostContextContract } from './canvasLoader'
import type { RawAxiosRequestHeaders } from 'axios'
import apiClient from '@/api/client'
import { getAPIBaseURL } from '@/api/url'
import { useAppStore } from '@/stores/app'
import { useAuthStore } from '@/stores/auth'
import { getLocale } from '@/i18n'

function toError(value: unknown, fallback = 'Canvas request failed'): Error & { status?: number; code?: string } {
  const source = value as { message?: string; status?: number; code?: string } | null
  const error = new Error(source?.message || fallback) as Error & { status?: number; code?: string }
  error.status = source?.status
  error.code = source?.code
  return error
}

async function request<T>(method: string, path: string, body?: unknown, headers: Record<string, string> = {}, signal?: AbortSignal): Promise<T> {
  const requestHeaders: RawAxiosRequestHeaders = { ...headers }
  if (typeof FormData !== 'undefined' && body instanceof FormData) requestHeaders['Content-Type'] = null
  try {
    const response = await apiClient.request<T>({ method, url: path, data: body, headers: requestHeaders, signal })
    return response.data
  } catch (error) {
    throw toError(error)
  }
}

export function createCanvasHostContext(routeMode: 'user' | 'admin', navigate: (path: string) => void, theme: 'light' | 'dark' = 'light'): CanvasHostContextContract {
  const appStore = useAppStore()
  const authStore = useAuthStore()
  return {
    apiBaseURL: getAPIBaseURL(),
    storageScope: String(authStore.user?.id || 'anonymous'),
    locale: getLocale(),
    theme,
    routeMode,
    request,
    navigate,
    notify(level, message) {
      if (level === 'success') appStore.showSuccess(message)
      else if (level === 'warning') appStore.showWarning(message)
      else if (level === 'error') appStore.showError(message)
      else appStore.showInfo(message)
    }
  }
}
