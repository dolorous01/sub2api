import { nanoid } from 'nanoid'
import type { CanvasDocument, CanvasProject } from '@sub2api/api/canvas-api'
import { isCanvasDocument } from '@sub2api/api/canvas-api'

export type CanvasSaveState = 'idle' | 'saving' | 'saved' | 'error'

export interface BrowserCanvasProject extends CanvasProject {
  saveState: CanvasSaveState
  localDraft?: CanvasDocument
}

export interface BrowserCanvasState {
  projectsByID: Record<string, BrowserCanvasProject>
  order: string[]
  hydrated: boolean
  activeProjectID?: string
}

export interface BrowserCanvasStore {
  getState(): BrowserCanvasState
  subscribe(listener: () => void): () => void
  hydrate(): Promise<void>
  createProject(name?: string, document?: CanvasDocument): Promise<BrowserCanvasProject>
  openProject(id: string): Promise<BrowserCanvasProject>
  updateDocument(id: string, document: CanvasDocument): void
  mutateDocument(id: string, update: (document: CanvasDocument) => CanvasDocument): CanvasDocument | undefined
  renameProject(id: string, name: string): Promise<void>
  deleteProject(id: string): Promise<void>
  saveAsNew(id: string): Promise<BrowserCanvasProject>
  destroy(): void
}

interface StoredCanvasProjects {
  schema_version: 1
  order: string[]
  projects: CanvasProject[]
}

const emptyDocument = (): CanvasDocument => ({
  schema_version: 1,
  nodes: [],
  edges: [],
  viewport: { x: 0, y: 0, k: 1 }
})

export function createBrowserCanvasStore(storageScope: string): BrowserCanvasStore {
  const safeScope = storageScope.replace(/[^a-zA-Z0-9_-]/g, '_') || 'anonymous'
  const storageKey = `sub2api:infinite-canvas:projects:${safeScope}:v1`
  let state: BrowserCanvasState = { projectsByID: {}, order: [], hydrated: false }
  const listeners = new Set<() => void>()
  let timer: ReturnType<typeof setTimeout> | undefined

  const emit = () => listeners.forEach((listener) => listener())
  const setState = (next: BrowserCanvasState) => {
    state = next
    emit()
  }
  const put = (project: BrowserCanvasProject) => {
    setState({
      ...state,
      projectsByID: { ...state.projectsByID, [project.id]: project },
      order: state.order.includes(project.id) ? state.order : [project.id, ...state.order]
    })
  }
  const persist = () => {
    if (timer) {
      clearTimeout(timer)
      timer = undefined
    }
    const projects = state.order.flatMap((id) => {
      const project = state.projectsByID[id]
      if (!project) return []
      const document = project.localDraft || project.document
      return [{
        id: project.id,
        name: project.name,
        document,
        version: project.version,
        created_at: project.created_at,
        updated_at: project.updated_at
      } satisfies CanvasProject]
    })
    try {
      localStorage.setItem(storageKey, JSON.stringify({ schema_version: 1, order: state.order, projects } satisfies StoredCanvasProjects))
      const projectsByID = { ...state.projectsByID }
      for (const project of projects) projectsByID[project.id] = { ...project, saveState: 'saved' }
      setState({ ...state, projectsByID })
    } catch {
      const projectsByID = { ...state.projectsByID }
      for (const id of state.order) {
        const project = projectsByID[id]
        if (project?.localDraft) projectsByID[id] = { ...project, saveState: 'error' }
      }
      setState({ ...state, projectsByID })
    }
  }
  const schedulePersist = () => {
    if (timer) clearTimeout(timer)
    timer = setTimeout(persist, 250)
  }
  const handleBeforeUnload = () => persist()
  window.addEventListener('beforeunload', handleBeforeUnload)

  return {
    getState: () => state,
    subscribe(listener) {
      listeners.add(listener)
      return () => listeners.delete(listener)
    },
    async hydrate() {
      const saved = readStoredProjects(localStorage.getItem(storageKey))
      const projectsByID: Record<string, BrowserCanvasProject> = {}
      for (const project of saved.projects) projectsByID[project.id] = { ...project, saveState: 'saved' }
      setState({ projectsByID, order: saved.order.filter((id) => Boolean(projectsByID[id])), hydrated: true })
    },
    async createProject(name = 'Untitled canvas', document = emptyDocument()) {
      const now = new Date().toISOString()
      const project: BrowserCanvasProject = {
        id: `project_${nanoid()}`,
        name,
        document,
        version: 1,
        created_at: now,
        updated_at: now,
        saveState: 'saving'
      }
      put(project)
      state = { ...state, activeProjectID: project.id }
      emit()
      persist()
      return state.projectsByID[project.id]
    },
    async openProject(id) {
      const project = state.projectsByID[id]
      if (!project) throw new Error('Canvas project not found in this browser')
      state = { ...state, activeProjectID: id }
      emit()
      return project
    },
    updateDocument(id, document) {
      const project = state.projectsByID[id]
      if (!project) return
      put({ ...project, document, localDraft: document, updated_at: new Date().toISOString(), saveState: 'saving' })
      schedulePersist()
    },
    mutateDocument(id, update) {
      const project = state.projectsByID[id]
      if (!project) return undefined
      const next = update(project.localDraft || project.document)
      this.updateDocument(id, next)
      return next
    },
    async renameProject(id, name) {
      const project = state.projectsByID[id]
      if (!project) return
      const nextName = name.trim() || project.name
      if (nextName === project.name) return
      put({ ...project, name: nextName, updated_at: new Date().toISOString(), saveState: 'saving' })
      persist()
    },
    async deleteProject(id) {
      const projectsByID = { ...state.projectsByID }
      delete projectsByID[id]
      const order = state.order.filter((projectID) => projectID !== id)
      setState({
        ...state,
        projectsByID,
        order,
        activeProjectID: state.activeProjectID === id ? order[0] : state.activeProjectID
      })
      persist()
    },
    async saveAsNew(id) {
      const project = state.projectsByID[id]
      if (!project) throw new Error('Canvas project not found in this browser')
      return this.createProject(`${project.name} copy`, structuredClone(project.localDraft || project.document))
    },
    destroy() {
      window.removeEventListener('beforeunload', handleBeforeUnload)
      listeners.clear()
      persist()
    }
  }
}

function readStoredProjects(raw: string | null): StoredCanvasProjects {
  if (!raw) return { schema_version: 1, order: [], projects: [] }
  try {
    const value = JSON.parse(raw) as Partial<StoredCanvasProjects>
    if (value.schema_version !== 1 || !Array.isArray(value.projects) || !Array.isArray(value.order)) {
      throw new Error('invalid canvas storage')
    }
    const projectsByID = new Map<string, CanvasProject>()
    for (const project of value.projects) {
      if (isCanvasProject(project)) projectsByID.set(project.id, project)
    }
    const order: string[] = []
    const seen = new Set<string>()
    for (const id of value.order) {
      if (typeof id === 'string' && projectsByID.has(id) && !seen.has(id)) {
        order.push(id)
        seen.add(id)
      }
    }
    for (const id of projectsByID.keys()) {
      if (!seen.has(id)) order.push(id)
    }
    return { schema_version: 1, order, projects: [...projectsByID.values()] }
  } catch {
    return { schema_version: 1, order: [], projects: [] }
  }
}

function isCanvasProject(value: unknown): value is CanvasProject {
  if (!value || typeof value !== 'object') return false
  const project = value as Partial<CanvasProject>
  return typeof project.id === 'string' && project.id.length > 0 &&
    typeof project.name === 'string' && isCanvasDocument(project.document) &&
    typeof project.version === 'number' && Number.isFinite(project.version) && project.version >= 1 &&
    typeof project.created_at === 'string' && typeof project.updated_at === 'string'
}
