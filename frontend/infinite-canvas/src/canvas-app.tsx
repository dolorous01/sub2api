import {
  useCallback, useEffect, useMemo, useRef, useState, useSyncExternalStore,
  type PointerEvent as ReactPointerEvent, type ReactElement
} from 'react'
import {
  Box, Braces, ChevronLeft, ChevronRight, CircleHelp, Download, FileImage, Focus,
  Group, ImageOff, ImagePlus, Link2, LoaderCircle, Plus, Redo2, Save, Trash2, Type, Undo2,
  Upload, X, ZoomIn, ZoomOut
} from 'lucide-react'
import { nanoid } from 'nanoid'
import { InfiniteCanvas } from '@/components/canvas/infinite-canvas'
import { useThemeStore } from '@/stores/use-theme-store'
import type { CanvasDocument, CanvasEdgeDocument, CanvasNodeDocument } from '@sub2api/api/canvas-api'
import { createCanvasAPI, isCanvasDocument } from '@sub2api/api/canvas-api'
import { APIKeyModelSelector } from '@sub2api/components/api-key-model-selector'
import { SourceNoticeDialog } from '@sub2api/components/source-notice-dialog'
import { useCanvasHost } from '@sub2api/host-context'
import { useCanvasI18n } from '@sub2api/i18n'
import { useCanvasSessionStore } from '@sub2api/stores/canvas-session-store'
import { createBrowserCanvasStore } from '@sub2api/stores/browser-canvas-store'

type Viewport = { x: number; y: number; k: number }
const maxImageUploadBytes = 20 * 1024 * 1024
const supportedImageTypes = new Set(['image/png', 'image/jpeg', 'image/webp'])

type DragState = {
  ids: string[]
  startX: number
  startY: number
  scale: number
  original: Map<string, { x: number; y: number }>
  before: CanvasDocument
}

export function CanvasApp() {
  const host = useCanvasHost()
  const hostRef = useRef(host)
  hostRef.current = host
  const t = useCanvasI18n()
  const api = useMemo(() => createCanvasAPI(host), [host.apiBaseURL, host.request, host.storageScope])
  const projects = useMemo(() => createBrowserCanvasStore(host.storageScope), [host.storageScope])
  const state = useSyncExternalStore(projects.subscribe, projects.getState, projects.getState)
  const { apiKeyID, model, setSelection } = useCanvasSessionStore()
  const [config, setConfig] = useState<Awaited<ReturnType<typeof api.getConfig>>>()
  const [selected, setSelected] = useState<string[]>([])
  const [prompt, setPrompt] = useState('')
  const [operation, setOperation] = useState<'generation' | 'edit'>('generation')
  const [count, setCount] = useState(1)
  const [size, setSize] = useState('1024x1024')
  const [sidebarOpen, setSidebarOpen] = useState(true)
  const [sourceOpen, setSourceOpen] = useState(false)
  const [busy, setBusy] = useState(false)
  const [nameDraft, setNameDraft] = useState('')
  const [drag, setDrag] = useState<DragState>()
  const [historyVersion, setHistoryVersion] = useState(0)
  const history = useRef<{ past: CanvasDocument[]; future: CanvasDocument[] }>({ past: [], future: [] })
  const canvasRef = useRef<HTMLDivElement>(null)
  const fileInputRef = useRef<HTMLInputElement>(null)
  const importInputRef = useRef<HTMLInputElement>(null)
  const aborters = useRef(new Map<string, AbortController>())

  const activeID = state.activeProjectID || state.order[0]
  const active = activeID ? state.projectsByID[activeID] : undefined
  const document = active?.localDraft || active?.document
  const viewport = document?.viewport || { x: 0, y: 0, k: 1 }

  useEffect(() => {
    useThemeStore.getState().setTheme(host.theme)
  }, [host.theme])

  useEffect(() => {
    void Promise.all([projects.hydrate(), api.getConfig().then((next) => {
      setConfig(next)
      const session = useCanvasSessionStore.getState()
      const selectedKey = next.api_keys.some((key) => key.id === session.apiKeyID)
        ? session.apiKeyID
        : next.selected_api_key_id
      setSelection(selectedKey, session.model || 'gpt-image-1')
    })]).catch((error) => {
      hostRef.current.notify('error', readableError(error))
    })
    return () => {
      projects.destroy()
      for (const aborter of aborters.current.values()) aborter.abort()
      aborters.current.clear()
    }
  }, [api, projects, setSelection])

  useEffect(() => {
    if (!state.hydrated || state.activeProjectID || state.order.length === 0) return
    void projects.openProject(state.order[0]).catch((error) => hostRef.current.notify('error', readableError(error)))
  }, [projects, state.activeProjectID, state.hydrated, state.order])

  useEffect(() => {
    setNameDraft(active?.name || '')
    history.current = { past: [], future: [] }
    setHistoryVersion((value) => value + 1)
    setSelected([])
  }, [activeID, active?.name])

  const commit = useCallback((update: CanvasDocument | ((current: CanvasDocument) => CanvasDocument), record = true) => {
    if (!activeID) return
    projects.mutateDocument(activeID, (current) => {
      const next = typeof update === 'function' ? update(current) : update
      if (record && next !== current) {
        history.current.past.push(structuredClone(current))
        if (history.current.past.length > 80) history.current.past.shift()
        history.current.future = []
        setHistoryVersion((value) => value + 1)
      }
      return next
    })
  }, [activeID, projects])

  useEffect(() => {
    if (!drag) return
    const move = (event: PointerEvent) => {
      const dx = (event.clientX - drag.startX) / drag.scale
      const dy = (event.clientY - drag.startY) / drag.scale
      commit((current) => ({
        ...current,
        nodes: current.nodes.map((node) => {
          const original = drag.original.get(node.id)
          return original ? { ...node, position: { x: original.x + dx, y: original.y + dy } } : node
        })
      }), false)
    }
    const up = () => {
      setDrag(undefined)
      const current = activeID
        ? projects.getState().projectsByID[activeID]?.localDraft || projects.getState().projectsByID[activeID]?.document
        : undefined
      const moved = current?.nodes.some((node) => {
        const original = drag.original.get(node.id)
        return original && (node.position.x !== original.x || node.position.y !== original.y)
      })
      if (moved) {
        history.current.past.push(drag.before)
        if (history.current.past.length > 80) history.current.past.shift()
        history.current.future = []
        setHistoryVersion((value) => value + 1)
      }
    }
    window.addEventListener('pointermove', move)
    window.addEventListener('pointerup', up, { once: true })
    return () => {
      window.removeEventListener('pointermove', move)
      window.removeEventListener('pointerup', up)
    }
  }, [activeID, commit, drag, projects])

  const addNode = (type: CanvasNodeDocument['type'], patch: Partial<CanvasNodeDocument> = {}) => {
    if (!activeID) return
    const nodeID = patch.id || `node_${nanoid()}`
    commit((current) => {
      const index = current.nodes.length
      const node: CanvasNodeDocument = {
        id: nodeID,
        type,
        position: { x: 160 + (index % 4) * 260, y: 130 + Math.floor(index / 4) * 220 },
        size: type === 'group' ? { width: 520, height: 340 } : { width: 220, height: type === 'image' ? 260 : 150 },
        ...patch
      }
      return { ...current, nodes: [...current.nodes, node] }
    })
    setSelected([nodeID])
  }

  const updateNode = (id: string, patch: Partial<CanvasNodeDocument>, record = true) => {
    commit((current) => ({
      ...current,
      nodes: current.nodes.map((node) => node.id === id ? { ...node, ...patch } : node)
    }), record)
  }

  const removeSelected = () => {
    if (!document || !selected.length) return
    const ids = new Set(selected)
    commit((current) => ({
      ...current,
      nodes: current.nodes.filter((node) => !ids.has(node.id)),
      edges: current.edges.filter((edge) => !ids.has(edge.source) && !ids.has(edge.target))
    }))
    setSelected([])
  }

  const connectSelected = () => {
    if (!document || selected.length !== 2) return
    const edge: CanvasEdgeDocument = { id: `edge_${nanoid()}`, source: selected[0], target: selected[1] }
    if (document.edges.some((item) => item.source === edge.source && item.target === edge.target)) return
    commit((current) => ({ ...current, edges: [...current.edges, edge] }))
  }

  const undo = () => {
    if (!document || !activeID) return
    const previous = history.current.past.pop()
    if (!previous) return
    history.current.future.push(structuredClone(document))
    projects.updateDocument(activeID, previous)
    setHistoryVersion((value) => value + 1)
  }

  const redo = () => {
    if (!document || !activeID) return
    const next = history.current.future.pop()
    if (!next) return
    history.current.past.push(structuredClone(document))
    projects.updateDocument(activeID, next)
    setHistoryVersion((value) => value + 1)
  }

  const setViewport = (next: Viewport) => {
    commit((current) => ({ ...current, viewport: next }), false)
  }

  const fitView = () => {
    if (!document || !canvasRef.current || document.nodes.length === 0) {
      setViewport({ x: 0, y: 0, k: 1 })
      return
    }
    const rect = canvasRef.current.getBoundingClientRect()
    const bounds = document.nodes.reduce((result, node) => ({
      minX: Math.min(result.minX, node.position.x), minY: Math.min(result.minY, node.position.y),
      maxX: Math.max(result.maxX, node.position.x + (node.size?.width || 220)),
      maxY: Math.max(result.maxY, node.position.y + (node.size?.height || 160))
    }), { minX: Infinity, minY: Infinity, maxX: -Infinity, maxY: -Infinity })
    const k = Math.min(1.2, Math.max(.15, Math.min((rect.width - 100) / (bounds.maxX - bounds.minX), (rect.height - 100) / (bounds.maxY - bounds.minY))))
    setViewport({ x: (rect.width - (bounds.maxX + bounds.minX) * k) / 2, y: (rect.height - (bounds.maxY + bounds.minY) * k) / 2, k })
  }

  const uploadFile = async (file: File) => {
    if (!activeID) return
    if (!supportedImageTypes.has(file.type)) {
      hostRef.current.notify('warning', t('unsupportedImage'))
      return
    }
    if (file.size > maxImageUploadBytes) {
      hostRef.current.notify('warning', t('imageTooLarge'))
      return
    }
    setBusy(true)
    try {
      const asset = await api.uploadAsset(file)
      addNode('image', { asset_id: asset.id, text: file.name, metadata: { source: 'upload' } })
    } catch (error) {
      hostRef.current.notify('error', readableError(error))
    } finally {
      setBusy(false)
    }
  }

  const runGeneration = async () => {
    if (!document || !activeID || !apiKeyID || !model || !prompt.trim()) return
    const inputNode = operation === 'edit'
      ? document.nodes.find((node) => selected.includes(node.id) && node.type === 'image' && node.asset_id)
      : undefined
    if (operation === 'edit' && !inputNode?.asset_id) {
      hostRef.current.notify('warning', t('selectImage'))
      return
    }
    const runningID = `node_${nanoid()}`
    addNode('image', {
      id: runningID,
      prompt: prompt.trim(),
      metadata: { generationStatus: 'queued', selectedModel: model }
    })
    const aborter = new AbortController()
    aborters.current.set(runningID, aborter)
    setBusy(true)
    try {
      updateNode(runningID, { metadata: { generationStatus: 'running', selectedModel: model } }, false)
      const request = { api_key_id: apiKeyID, model, prompt: prompt.trim(), size, n: count }
      const results = operation === 'edit' && inputNode?.asset_id
        ? await api.edit({ ...request, asset_id: inputNode.asset_id }, aborter.signal)
        : await api.generate(request, aborter.signal)
      const outputIDs = results.map((result, index) => index === 0 ? runningID : `node_${nanoid()}`)
      updateNode(runningID, {
        asset_id: results[0].asset_id,
        prompt: results[0].revised_prompt || prompt.trim(),
        metadata: { generationStatus: 'completed', selectedModel: model }
      })
      results.slice(1).forEach((result, index) => addNode('image', {
        id: outputIDs[index + 1],
        asset_id: result.asset_id,
        prompt: result.revised_prompt || prompt.trim(),
        metadata: { generationStatus: 'completed', selectedModel: model }
      }))
      if (inputNode) {
        const edges = outputIDs.map((target) => ({ id: `edge_${nanoid()}`, source: inputNode.id, target }))
        commit((current) => ({ ...current, edges: [...current.edges, ...edges] }))
      }
    } catch (error) {
      if (aborter.signal.aborted) {
        updateNode(runningID, { metadata: { generationStatus: 'canceled', selectedModel: model } })
      } else {
        hostRef.current.notify('error', readableError(error))
        updateNode(runningID, { metadata: { generationStatus: 'failed', errorCode: readableError(error), selectedModel: model } })
      }
    } finally {
      aborters.current.delete(runningID)
      setBusy(false)
    }
  }

  const exportProject = () => {
    if (!active) return
    const blob = new Blob([JSON.stringify({ name: active.name, document }, null, 2)], { type: 'application/json' })
    const url = URL.createObjectURL(blob)
    const link = window.document.createElement('a')
    link.href = url
    link.download = `${active.name || 'canvas'}.json`
    link.click()
    URL.revokeObjectURL(url)
    hostRef.current.notify('warning', t('exportAssetsWarning'))
  }

  const importProject = async (file: File) => {
    try {
      const parsed = JSON.parse(await file.text()) as unknown
      const record = isRecord(parsed) ? parsed : undefined
      const imported = record && 'document' in record ? record.document : parsed
      if (!isCanvasDocument(imported)) throw new Error(t('invalidDocument'))
      const name = record && typeof record.name === 'string' ? record.name : t('untitled')
      await projects.createProject(name || t('untitled'), imported)
    } catch (error) {
      hostRef.current.notify('error', readableError(error))
    }
  }

  const deleteProject = (id: string) => {
    if (!window.confirm(t('deleteProjectConfirm'))) return
    void projects.deleteProject(id).catch((error) => hostRef.current.notify('error', readableError(error)))
  }

  const sizes = ['1024x1024', '1536x1024', '1024x1536']
  const maxOutputs = 4
  const projectsLabel = sidebarOpen ? t('collapseProjects') : t('showProjects')

  return (
    <main className="sub2api-canvas-root" data-theme={host.theme}>
      <aside className={`canvas-projects ${sidebarOpen ? 'open' : 'closed'}`} data-canvas-no-zoom>
        <div className="project-heading">
          <strong>{t('projects')}</strong>
          <div className="project-heading-actions">
            <button className="icon-button" title={t('newProject')} aria-label={t('newProject')} onClick={() => void projects.createProject(t('untitled'))}><Plus size={17} /></button>
            <button className="icon-button mobile-project-close" title={t('collapseProjects')} aria-label={t('collapseProjects')} onClick={() => setSidebarOpen(false)}><ChevronLeft size={17} /></button>
          </div>
        </div>
        <div className="project-list">
          {state.order.map((id) => {
            const item = state.projectsByID[id]
            return <div key={id} className={`project-row ${id === activeID ? 'active' : ''}`}>
              <button className="project-open" onClick={() => void projects.openProject(id)}>
                <FileImage size={16} /><span>{item.name}</span><small>{saveLabel(item.saveState, t)}</small>
              </button>
              <button className="project-delete" title={t('deleteProject')} aria-label={t('deleteProject')} disabled={busy && id === activeID} onClick={() => deleteProject(id)}><Trash2 size={14} /></button>
            </div>
          })}
          {state.hydrated && state.order.length === 0 && <div className="project-empty"><span>{t('noProject')}</span><button className="canvas-command primary" onClick={() => void projects.createProject(t('untitled'))}>{t('newProject')}</button></div>}
        </div>
      </aside>

      <section className="canvas-workspace">
        <header className="canvas-toolbar" data-canvas-no-zoom>
          <button className="icon-button" title={projectsLabel} aria-label={projectsLabel} onClick={() => setSidebarOpen(!sidebarOpen)}>{sidebarOpen ? <ChevronLeft size={18} /> : <ChevronRight size={18} />}</button>
          {active && <input className="project-name" aria-label={t('projectName')} value={nameDraft}
            onChange={(event) => setNameDraft(event.target.value)}
            onBlur={() => void projects.renameProject(active.id, nameDraft)} />}
          <span className={`save-state ${active?.saveState || 'idle'}`}><Save size={14} />{active ? saveLabel(active.saveState, t) : ''}</span>
          <div className="toolbar-divider" />
          <IconButton icon={<Undo2 />} label={t('undo')} onClick={undo} disabled={!history.current.past.length} />
          <IconButton icon={<Redo2 />} label={t('redo')} onClick={redo} disabled={!history.current.future.length} dataVersion={historyVersion} />
          <IconButton icon={<ZoomOut />} label={t('zoomOut')} onClick={() => setViewport({ ...viewport, k: Math.max(.1, viewport.k / 1.2) })} />
          <span className="zoom-value">{Math.round(viewport.k * 100)}%</span>
          <IconButton icon={<ZoomIn />} label={t('zoomIn')} onClick={() => setViewport({ ...viewport, k: Math.min(5, viewport.k * 1.2) })} />
          <IconButton icon={<Focus />} label={t('fit')} onClick={fitView} />
          <div className="toolbar-divider" />
          <IconButton icon={<Type />} label={t('addText')} onClick={() => addNode('text', { text: '' })} />
          <IconButton icon={<ImagePlus />} label={t('addImage')} onClick={() => fileInputRef.current?.click()} />
          <IconButton icon={<Braces />} label={t('addConfig')} onClick={() => addNode('config', { text: '{}' })} />
          <IconButton icon={<Group />} label={t('addGroup')} onClick={() => addNode('group', { text: t('groupDefault') })} />
          <IconButton icon={<Link2 />} label={t('connect')} onClick={connectSelected} disabled={selected.length !== 2} />
          <IconButton icon={<Trash2 />} label={t('delete')} onClick={removeSelected} disabled={!selected.length} />
          <div className="toolbar-spacer" />
          <IconButton icon={<Upload />} label={t('import')} onClick={() => importInputRef.current?.click()} />
          <IconButton icon={<Download />} label={t('export')} onClick={exportProject} disabled={!active} />
          <IconButton icon={<CircleHelp />} label={t('source')} onClick={() => setSourceOpen(true)} />
        </header>

        <div className="canvas-stage">
          {document ? <InfiniteCanvas containerRef={canvasRef} viewport={viewport} onViewportChange={setViewport} onCanvasDeselect={() => setSelected([])} onCanvasDoubleClick={(event) => {
            const rect = canvasRef.current?.getBoundingClientRect()
            if (!rect) return
            addNode('text', { position: { x: (event.clientX - rect.left - viewport.x) / viewport.k, y: (event.clientY - rect.top - viewport.y) / viewport.k }, text: '' })
          }}>
            <CanvasEdges document={document} />
            {document.nodes.slice().sort((a, b) => Number(a.type !== 'group') - Number(b.type !== 'group')).map((node) =>
              <CanvasNode key={node.id} node={node} selected={selected.includes(node.id)} api={api} onSelect={(event) => {
                event.stopPropagation()
                setSelected((current) => event.shiftKey ? (current.includes(node.id) ? current.filter((id) => id !== node.id) : [...current, node.id]) : [node.id])
              }} onDragStart={(event) => {
                event.preventDefault()
                event.stopPropagation()
                const ids = selected.includes(node.id) ? selected : [node.id]
                if (!selected.includes(node.id)) setSelected(ids)
                const original = new Map(document.nodes.filter((item) => ids.includes(item.id)).map((item) => [item.id, { ...item.position }]))
                setDrag({
                  ids,
                  startX: event.clientX,
                  startY: event.clientY,
                  scale: viewport.k,
                  original,
                  before: structuredClone(document)
                })
              }} onChange={(patch) => updateNode(node.id, patch)} onCancel={() => aborters.current.get(node.id)?.abort()} />
            )}
          </InfiniteCanvas> : <div className="canvas-empty"><Box size={36} /><button className="canvas-command primary" onClick={() => void projects.createProject(t('untitled'))}>{t('newProject')}</button></div>}
        </div>

        <footer className="generation-panel" data-canvas-no-zoom>
          <div className="generation-mode" role="group" aria-label={t('operation')}>
            <button className={operation === 'generation' ? 'active' : ''} onClick={() => setOperation('generation')}>{t('generation')}</button>
            <button className={operation === 'edit' ? 'active' : ''} onClick={() => setOperation('edit')}>{t('imageEdit')}</button>
          </div>
          <APIKeyModelSelector config={config} operation={operation} apiKeyID={apiKeyID} model={model} onAPIKeyChange={(id) => setSelection(id)} onModelChange={(value) => setSelection(apiKeyID, value)} onCreateKey={() => host.navigate('/keys')} />
          <textarea aria-label={t('prompt')} placeholder={t('prompt')} value={prompt} onChange={(event) => setPrompt(event.target.value)} />
          <label className="compact-field"><span>{t('size')}</span><select value={size} onChange={(event) => setSize(event.target.value)}>{sizes.map((item) => <option key={item}>{item}</option>)}</select></label>
          <label className="compact-field"><span>{t('outputCount')}</span><input type="number" min={1} max={maxOutputs} value={count} onChange={(event) => setCount(Math.max(1, Math.min(maxOutputs, Number(event.target.value))))} /></label>
          <button className="generate-button" disabled={busy || !active || !apiKeyID || !model || !prompt.trim()} onClick={() => void runGeneration()}>{busy ? <LoaderCircle className="spin" size={18} /> : <ImagePlus size={18} />}<span>{busy ? t('generating') : operation === 'edit' ? t('edit') : t('generate')}</span></button>
        </footer>
      </section>

      <input ref={fileInputRef} hidden type="file" accept="image/png,image/jpeg,image/webp" onChange={(event) => { const file = event.target.files?.[0]; if (file) void uploadFile(file); event.target.value = '' }} />
      <input ref={importInputRef} hidden type="file" accept="application/json" onChange={(event) => { const file = event.target.files?.[0]; if (file) void importProject(file); event.target.value = '' }} />
      <SourceNoticeDialog open={sourceOpen} onClose={() => setSourceOpen(false)} />
    </main>
  )
}

function IconButton({ icon, label, onClick, disabled, dataVersion: _ }: { icon: ReactElement; label: string; onClick(): void; disabled?: boolean; dataVersion?: number }) {
  return <button className="icon-button" title={label} aria-label={label} onClick={onClick} disabled={disabled}>{icon}</button>
}

function CanvasEdges({ document }: { document: CanvasDocument }) {
  const nodes = new Map(document.nodes.map((node) => [node.id, node]))
  return <svg className="canvas-edges" aria-hidden="true">
    {document.edges.map((edge) => {
      const from = nodes.get(edge.source); const to = nodes.get(edge.target)
      if (!from || !to) return null
      const x1 = from.position.x + (from.size?.width || 220); const y1 = from.position.y + (from.size?.height || 160) / 2
      const x2 = to.position.x; const y2 = to.position.y + (to.size?.height || 160) / 2
      const bend = Math.max(70, Math.abs(x2 - x1) * .45)
      return <path key={edge.id} d={`M ${x1} ${y1} C ${x1 + bend} ${y1}, ${x2 - bend} ${y2}, ${x2} ${y2}`} />
    })}
  </svg>
}

function CanvasNode({ node, selected, api, onSelect, onDragStart, onChange, onCancel }: {
  node: CanvasNodeDocument; selected: boolean; api: ReturnType<typeof createCanvasAPI>
  onSelect(event: ReactPointerEvent): void; onDragStart(event: ReactPointerEvent): void
  onChange(patch: Partial<CanvasNodeDocument>): void; onCancel(): void
}) {
  const t = useCanvasI18n()
  const status = String(node.metadata?.generationStatus || '')
  const loading = ['queued', 'running', 'falling_back', 'saving'].includes(status)
  return <article
    className={`canvas-node type-${node.type} ${selected ? 'selected' : ''}`}
    data-node-id={node.id}
    style={{ left: node.position.x, top: node.position.y, width: node.size?.width || 220, height: node.size?.height || 160 }}
    onPointerDown={onSelect}
  >
    <header onPointerDown={onDragStart}><span className="node-type">{nodeTypeLabel(node.type, t)}</span><span className={`node-status ${status}`}>{status.replace('_', ' ')}</span>{loading && <button className="node-cancel" onPointerDown={(event) => event.stopPropagation()} onClick={onCancel} aria-label={t('nodeCancel')} title={t('nodeCancel')}><X size={14} /></button>}</header>
    {node.type === 'image' && <div className="node-image">{node.asset_id ? <AssetImage assetID={node.asset_id} api={api} /> : loading ? <LoaderCircle className="spin" size={26} /> : <FileImage size={28} />}</div>}
    {node.type === 'text' && <textarea value={node.text || ''} placeholder={t('textPlaceholder')} onPointerDown={(event) => event.stopPropagation()} onChange={(event) => onChange({ text: event.target.value })} />}
    {node.type === 'config' && <textarea className="config-editor" value={node.text || '{}'} onPointerDown={(event) => event.stopPropagation()} onChange={(event) => onChange({ text: event.target.value })} />}
    {node.type === 'group' && <input value={node.text || t('groupDefault')} onPointerDown={(event) => event.stopPropagation()} onChange={(event) => onChange({ text: event.target.value })} />}
    {(node.prompt || node.asset_id) && node.type === 'image' && <div className="node-caption">{node.prompt || node.text}</div>}
  </article>
}

function nodeTypeLabel(type: CanvasNodeDocument['type'], t: ReturnType<typeof useCanvasI18n>): string {
  if (type === 'image') return t('nodeImage')
  if (type === 'text') return t('nodeText')
  if (type === 'config') return t('nodeConfig')
  return t('nodeGroup')
}

function AssetImage({ assetID, api }: { assetID: string; api: ReturnType<typeof createCanvasAPI> }) {
  const t = useCanvasI18n()
  const [url, setURL] = useState<string>()
  const [failed, setFailed] = useState(false)
  useEffect(() => {
    let current = ''
    let revoke = false
    const aborter = new AbortController()
    setURL(undefined)
    setFailed(false)
    void api.getAssetSource(assetID, aborter.signal).then((source) => {
      if (aborter.signal.aborted) {
        if (source.revoke) URL.revokeObjectURL(source.url)
        return
      }
      current = source.url
      revoke = source.revoke
      setURL(source.url)
    }).catch(() => {
      if (!aborter.signal.aborted) setFailed(true)
    })
    return () => { aborter.abort(); if (current && revoke) URL.revokeObjectURL(current) }
  }, [api, assetID])
  if (url) return <img src={url} alt="" draggable={false} />
  if (failed) return <span className="asset-unavailable" role="img" aria-label={t('assetUnavailable')} title={t('assetUnavailable')}><ImageOff size={26} /></span>
  return <LoaderCircle className="spin" size={24} />
}

function saveLabel(state: string, t: ReturnType<typeof useCanvasI18n>): string {
  if (state === 'saving') return t('saving')
  if (state === 'conflict') return t('conflict')
  if (state === 'error') return t('failed')
  return t('saved')
}

function readableError(error: unknown): string {
  if (error instanceof Error) return error.message
  return String(error || 'Unknown error')
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null
}
