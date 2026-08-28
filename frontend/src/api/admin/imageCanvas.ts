import { apiClient } from '../client'

export interface ImageCanvasCapability {
  media_kind?: string
  provider?: string
  dimension_mode?: string
  generation: boolean
  edit: boolean
  multi_image: boolean
  mask: boolean
  max_input_images: number
  max_outputs: number
  sizes: string[]
  aspect_ratios?: string[]
  resolutions?: string[]
  qualities?: string[]
  output_formats?: string[]
  backgrounds?: string[]
  output_compression?: boolean
  partial_images?: boolean
  max_partial_images?: number
  custom_size?: ImageCanvasCustomSizeConstraints
  experimental_sizes?: string[]
  defaults?: ImageCanvasModelParameters
  presets?: ImageCanvasPreset[]
}

export interface ImageCanvasCustomSizeConstraints {
  min_pixels: number
  max_pixels: number
  max_edge: number
  multiple_of: number
  max_aspect_ratio: number
}

export interface ImageCanvasModelParameters {
  size?: string
  aspect_ratio?: string
  resolution?: string
  quality?: string
  output_format?: string
  background?: string
  output_compression?: number
}

export interface ImageCanvasPreset {
  id: string
  label: string
  parameters: ImageCanvasModelParameters
  experimental?: boolean
}

export interface ImageCanvasPolicyItem {
  model: string
  enabled: boolean
  position: number
  capability?: ImageCanvasCapability
}

export interface ImageCanvasPolicy {
  version: number
  enabled: boolean
  models: ImageCanvasPolicyItem[]
  available_models: ImageCanvasPolicyItem[]
  catalog?: ImageCanvasCatalogEntry[]
}

export interface ImageCanvasCoverage {
  account_count: number
  group_count: number
  api_key_count: number
}

export interface ImageCanvasCatalogEntry {
  model: string
  provider?: string
  media_kind?: string
  capability?: ImageCanvasCapability
  coverage: ImageCanvasCoverage
  schedulable: boolean
  schedulability_reason?: string
}

export interface ImageCanvasFallbackRules {
  selected_model_first: boolean
  same_provider_only: boolean
  retryable_status_codes: string[]
  requires_zero_final_output: boolean
  content_policy_falls_back: boolean
  account_failover_first: boolean
}

export interface ImageCanvasPolicyAudit {
  id: number
  operator_user_id: number
  old_version: number
  new_version: number
  before_value: unknown
  after_value: unknown
  created_at: string
}

export interface ImageCanvasDefaultOverride {
  model: string
  parameters: ImageCanvasModelParameters
}

export interface ImageCanvasPresetOverride extends ImageCanvasPreset {
  model: string
}

export interface ImageCanvasRuntimeSettings {
  worker_concurrency: number
  max_active_jobs_per_user: number
  task_timeout_seconds: number
  max_outputs_per_job: number
  max_input_images: number
  result_ttl_seconds: number
  default_overrides: ImageCanvasDefaultOverride[]
  custom_presets: ImageCanvasPresetOverride[]
}

export interface ImageCanvasStorageInfo {
  enabled: boolean
  driver: string
  location: string
  prefix: string
  max_object_bytes: number
  generated_assets_permanent: boolean
  runtime_changes_need_restart: boolean
}

export interface ImageCanvasWorkerInfo {
  started: boolean
  target_workers: number
  running_workers: number
}

export interface ImageCanvasRuntimeResponse {
  settings: ImageCanvasRuntimeSettings
  defaults: ImageCanvasRuntimeSettings
  storage: ImageCanvasStorageInfo
  worker: ImageCanvasWorkerInfo
  available_models: ImageCanvasPolicyItem[]
}

export interface ImageCanvasOverviewResponse {
  entry_enabled: boolean
  policy_enabled: boolean
  policy_version: number
  active_model_count: number
  catalog: ImageCanvasCatalogEntry[]
  worker: ImageCanvasWorkerInfo
  storage: ImageCanvasStorageInfo
  fallback_rules: ImageCanvasFallbackRules
}

export interface ImageCanvasModelCheckResponse extends ImageCanvasCatalogEntry {
  policy_enabled: boolean
  policy_listed: boolean
  policy_item_enabled: boolean
  eligible: boolean
}

export interface ImageCanvasJobAttempt {
  model: string
  position: number
  started_at: string
  latency_ms: number
  error_class?: string
  final_count: number
}

export interface ImageCanvasRecentJob {
  id: string
  user_id: number
  status: string
  operation: string
  mode?: string
  requested_model: string
  mapped_model?: string
  selected_model?: string
  successful_model?: string
  api_key_id: number
  group_id: number
  project_id?: number
  policy_version?: number
  execution_phase?: string
  requested_count: number
  completed_count: number
  attempt_plan: string[]
  attempt_log: ImageCanvasJobAttempt[]
  error?: { type: string; code: string; message: string; retryable: boolean }
  created_at: string
  started_at?: string
  finished_at?: string
  updated_at: string
  duration_ms?: number
}

export async function getImageCanvasPolicy(): Promise<ImageCanvasPolicy> {
  const { data } = await apiClient.get<ImageCanvasPolicy>('/admin/image-canvas/model-policy')
  return data
}

export async function updateImageCanvasPolicy(input: { version: number; enabled: boolean; models: ImageCanvasPolicyItem[] }): Promise<ImageCanvasPolicy> {
  const { data } = await apiClient.put<ImageCanvasPolicy>('/admin/image-canvas/model-policy', input)
  return data
}

export async function listImageCanvasPolicyAudit(limit = 20): Promise<ImageCanvasPolicyAudit[]> {
  const { data } = await apiClient.get<{ items: ImageCanvasPolicyAudit[] }>('/admin/image-canvas/model-policy/audit', { params: { limit } })
  return data.items
}

export async function getImageCanvasRuntimeSettings(): Promise<ImageCanvasRuntimeResponse> {
  const { data } = await apiClient.get<ImageCanvasRuntimeResponse>('/admin/image-canvas/runtime')
  return data
}

export async function updateImageCanvasRuntimeSettings(input: ImageCanvasRuntimeSettings): Promise<ImageCanvasRuntimeResponse> {
  const { data } = await apiClient.put<ImageCanvasRuntimeResponse>('/admin/image-canvas/runtime', input)
  return data
}

export async function getImageCanvasOverview(): Promise<ImageCanvasOverviewResponse> {
  const { data } = await apiClient.get<ImageCanvasOverviewResponse>('/admin/image-canvas/overview')
  return data
}

export async function checkImageCanvasModel(model: string): Promise<ImageCanvasModelCheckResponse> {
  const encodedModel = encodeURIComponent(model)
  const { data } = await apiClient.post<ImageCanvasModelCheckResponse>(`/admin/image-canvas/models/${encodedModel}/check`)
  return data
}

export async function listImageCanvasRecentJobs(limit = 30): Promise<ImageCanvasRecentJob[]> {
  const { data } = await apiClient.get<{ items: ImageCanvasRecentJob[] }>('/admin/image-canvas/jobs/recent', { params: { limit } })
  return (data.items || []).map((item) => ({
    ...item,
    attempt_plan: item.attempt_plan || [],
    attempt_log: item.attempt_log || []
  }))
}
