import { StyleProvider } from '@ant-design/cssinjs'
import { ProConfigProvider } from '@ant-design/pro-components'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { App, ConfigProvider } from 'antd'
import enUS from 'antd/es/locale/en_US'
import zhCN from 'antd/es/locale/zh_CN'
import { lazy, Suspense, useEffect, useMemo, type ReactNode } from 'react'
import { createRoot } from 'react-dom/client'
import { useTranslation } from 'react-i18next'
import { Navigate, RouterProvider, createMemoryRouter } from 'react-router-dom'
import {
  CanvasHostProvider,
  createCanvasHostStore,
  useCanvasHost,
  type CanvasHandle,
  type CanvasHostContext
} from '@sub2api/host-context'
import { clearCanvasRuntimeHost, setCanvasRuntimeHost } from '@sub2api/runtime/host-runtime'
import { initializeCanvasProjectStore, resetCanvasProjectStore } from '@sub2api/adapters/use-canvas-store'
import { resetCanvasAssetRuntime } from '@sub2api/adapters/asset-runtime'
import { initializeCanvasConfigStore, resetCanvasConfigStore } from '@sub2api/adapters/use-config-store'
import CanvasProjectRuntime from '@sub2api/components/canvas-project-runtime'
import CanvasProjectsPage from '@/pages/canvas'
import upstreamI18n from '@/i18n'
import { getAntThemeConfig } from '@/lib/app-theme'
import { useThemeStore } from '@/stores/use-theme-store'
import 'antd/dist/reset.css'
import 'streamdown/styles.css'
import '@/styles/globals.css'

const FocusedImageEditor = lazy(() => import('@sub2api/editor/focused-image-editor'))

const queryClient = new QueryClient({
  defaultOptions: {
    queries: { staleTime: 30_000, retry: false, refetchOnWindowFocus: false }
  }
})

function CanvasProviders({ children, mountElement }: { children: ReactNode; mountElement: HTMLElement }) {
  const { i18n } = useTranslation()
  const theme = useThemeStore((state) => state.theme)
  const dark = theme === 'dark'
  const locale = i18n.resolvedLanguage?.toLowerCase().startsWith('zh') ? zhCN : enUS

  return (
    <ConfigProvider
      locale={locale}
      theme={getAntThemeConfig(dark)}
      getPopupContainer={() => mountElement}
    >
      <ProConfigProvider dark={dark}>
        <App>
          <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>
        </App>
      </ProConfigProvider>
    </ConfigProvider>
  )
}

function CanvasRuntime({ mountElement }: { mountElement: HTMLElement }) {
  const host = useCanvasHost()
  const rootNode = mountElement.getRootNode()
  const styleContainer = rootNode instanceof ShadowRoot ? rootNode : mountElement
  const router = useMemo(
    () => createMemoryRouter([
      { path: '/', element: <Navigate to="/canvas" replace /> },
      { path: '/canvas', element: <CanvasProjectsPage /> },
      { path: '/canvas/:id', element: <CanvasProjectRuntime /> },
      {
        path: '/editor/:projectId/:nodeId',
        element: (
          <Suspense fallback={<div className="flex h-full items-center justify-center">Loading...</div>}>
            <FocusedImageEditor />
          </Suspense>
        )
      },
      { path: '*', element: <Navigate to="/canvas" replace /> }
    ], { initialEntries: ['/canvas'] }),
    []
  )

  useEffect(() => {
    const locale = host.locale.toLowerCase().startsWith('zh') ? 'zh-CN' : 'en-US'
    void upstreamI18n.changeLanguage(locale)
    useThemeStore.getState().setTheme(host.theme)
    mountElement.classList.toggle('dark', host.theme === 'dark')
  }, [host.locale, host.theme, mountElement])

  return (
    <StyleProvider container={styleContainer}>
      <CanvasProviders mountElement={mountElement}>
        <RouterProvider router={router} />
      </CanvasProviders>
    </StyleProvider>
  )
}

export function mountCanvas(element: HTMLElement, context: CanvasHostContext): CanvasHandle {
  setCanvasRuntimeHost(context)
  resetCanvasProjectStore()
  resetCanvasAssetRuntime()
  resetCanvasConfigStore()
  void initializeCanvasProjectStore()
  void initializeCanvasConfigStore()
  const store = createCanvasHostStore(context)
  const root = createRoot(element)
  element.classList.add('sub2api-canvas-mount')
  root.render(
    <CanvasHostProvider store={store}>
      <CanvasRuntime mountElement={element} />
    </CanvasHostProvider>
  )

  let mounted = true
  return {
    updateContext(next) {
      if (mounted) {
        setCanvasRuntimeHost(next)
        store.update(next)
      }
    },
    unmount() {
      if (!mounted) return
      mounted = false
      root.unmount()
      resetCanvasProjectStore()
      resetCanvasAssetRuntime()
      resetCanvasConfigStore()
      clearCanvasRuntimeHost(store.getSnapshot())
      element.replaceChildren()
      element.classList.remove('sub2api-canvas-mount')
    }
  }
}

export type { CanvasHandle, CanvasHostContext }
