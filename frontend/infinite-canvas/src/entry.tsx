import { createRoot } from 'react-dom/client'
import { CanvasApp } from '@sub2api/canvas-app'
import {
  CanvasHostProvider,
  createCanvasHostStore,
  useCanvasHost,
  type CanvasHandle,
  type CanvasHostContext
} from '@sub2api/host-context'
import { CanvasI18nProvider } from '@sub2api/i18n'
import '@sub2api/styles/sub2api-canvas.css'

function CanvasRuntime() {
  const host = useCanvasHost()
  return (
    <CanvasI18nProvider locale={host.locale}>
      <CanvasApp />
    </CanvasI18nProvider>
  )
}

export function mountCanvas(element: HTMLElement, context: CanvasHostContext): CanvasHandle {
  const store = createCanvasHostStore(context)
  const root = createRoot(element)
  element.classList.add('sub2api-canvas-mount')
  root.render(
    <CanvasHostProvider store={store}>
      <CanvasRuntime />
    </CanvasHostProvider>
  )

  let mounted = true
  return {
    updateContext(next) {
      if (mounted) store.update(next)
    },
    unmount() {
      if (!mounted) return
      mounted = false
      root.unmount()
      element.replaceChildren()
      element.classList.remove('sub2api-canvas-mount')
    }
  }
}

export type { CanvasHandle, CanvasHostContext }
