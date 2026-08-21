export interface CanvasHostContextContract {
  apiBaseURL: string
  storageScope: string
  locale: string
  theme: 'light' | 'dark'
  routeMode: 'user' | 'admin'
  request<T>(method: string, path: string, body?: unknown, headers?: Record<string, string>, signal?: AbortSignal): Promise<T>
  navigate(path: string): void
  notify(level: 'success' | 'warning' | 'error', message: string): void
}

export interface CanvasHostModule {
  mountCanvas(element: HTMLElement, context: CanvasHostContextContract): { updateContext(context: CanvasHostContextContract): void; unmount(): void }
}

interface CanvasManifestEntry {
  file?: string
  css?: string[]
  isEntry?: boolean
}

const MANIFEST_PATH = '/infinite-canvas/manifest.json'
let manifestEntryPromise: Promise<CanvasManifestEntry> | undefined
let modulePromise: Promise<CanvasHostModule> | undefined
let loadedStyles = new WeakMap<Document | ShadowRoot, Set<string>>()

function sameOriginAsset(path: string): string {
  const url = new URL(path, window.location.origin)
  if (url.origin !== window.location.origin || !url.pathname.startsWith('/infinite-canvas/')) {
    throw new Error('Canvas asset must be served by the current site')
  }
  return url.href
}

export function resolveCanvasManifestEntry(manifest: Record<string, CanvasManifestEntry>): CanvasManifestEntry {
  const entry = Object.values(manifest).find((item) => item?.isEntry) ||
    Object.entries(manifest).find(([key]) => key.endsWith('entry.tsx') || key.endsWith('entry.js'))?.[1]
  if (!entry?.file) throw new Error('Canvas entry is missing from the build manifest')

  const css = new Set(entry.css || [])
  for (const item of Object.values(manifest)) {
    if (item?.file?.toLowerCase().endsWith('.css')) css.add(item.file)
  }
  return { ...entry, css: [...css] }
}

async function loadManifestEntry(): Promise<CanvasManifestEntry> {
  const response = await fetch(MANIFEST_PATH, {
    credentials: 'same-origin',
    cache: 'no-store',
    headers: { Accept: 'application/json' }
  })
  if (!response.ok) throw new Error(`Canvas manifest request failed (${response.status})`)
  const manifest = await response.json() as Record<string, CanvasManifestEntry>
  return resolveCanvasManifestEntry(manifest)
}

function loadStyles(entry: CanvasManifestEntry, root: Document | ShadowRoot): void {
  let rootStyles = loadedStyles.get(root)
  if (!rootStyles) {
    rootStyles = new Set<string>()
    loadedStyles.set(root, rootStyles)
  }
  for (const css of entry.css || []) {
    const href = sameOriginAsset(`/infinite-canvas/${css.replace(/^\/+/, '')}`)
    if (rootStyles.has(href)) continue
    const link = document.createElement('link')
    link.rel = 'stylesheet'
    link.href = href
    link.dataset.sub2apiCanvasStyle = 'true'
    if (root instanceof Document) root.head.appendChild(link)
    else root.appendChild(link)
    rootStyles.add(href)
  }
}

async function importModule(entry: CanvasManifestEntry): Promise<CanvasHostModule> {
  const entryFile = entry.file
  if (!entryFile) throw new Error('Canvas entry is missing from the build manifest')
  const imported = await import(/* @vite-ignore */ sameOriginAsset(`/infinite-canvas/${entryFile.replace(/^\/+/, '')}`)) as Partial<CanvasHostModule>
  if (typeof imported.mountCanvas !== 'function') throw new Error('Canvas bundle does not expose mountCanvas')
  return imported as CanvasHostModule
}

export async function loadCanvasModule(styleRoot: Document | ShadowRoot = document): Promise<CanvasHostModule> {
  manifestEntryPromise ||= loadManifestEntry().catch((error) => {
    manifestEntryPromise = undefined
    throw error
  })
  const entry = await manifestEntryPromise
  loadStyles(entry, styleRoot)
  modulePromise ||= importModule(entry).catch((error) => {
    modulePromise = undefined
    throw error
  })
  return modulePromise
}

export function resetCanvasModuleForTests(): void {
  manifestEntryPromise = undefined
  modulePromise = undefined
  loadedStyles = new WeakMap<Document | ShadowRoot, Set<string>>()
}
