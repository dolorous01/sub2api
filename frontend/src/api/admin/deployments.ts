import { apiClient } from '../client'

export type DeploymentComponent = 'sub2api' | 'canvas'
export type DeploymentAction = 'update' | 'rollback'
export type DeploymentOperationState = 'queued' | 'running' | 'succeeded' | 'failed' | 'interrupted'

export interface DeploymentReconciliation {
  required: boolean
  state: 'pending' | 'not_required' | 'not_run' | 'succeeded' | 'failed'
  status?: 'ok' | 'attention_required' | 'failed'
  metadata_sha256?: string | null
  active_but_unbound_count?: number | null
  inactive_but_bound_count?: number | null
  missing_from_official_count?: number | null
  plaintext_key_accessed?: boolean
  mutation_performed?: boolean
}

export interface DeploymentOperation {
  id: string
  component: DeploymentComponent
  action: DeploymentAction
  state: DeploymentOperationState
  stage: string
  requested_at: string
  started_at?: string | null
  finished_at?: string | null
  from_version?: string | null
  target_version?: string | null
  deployment_succeeded: boolean
  reconciliation: DeploymentReconciliation
  error?: {
    reason: string
    message: string
  } | null
}

export interface DeploymentComponentStatus {
  component: DeploymentComponent
  current_version?: string | null
  target_version?: string | null
  release_url?: string | null
  update_available: boolean
  update_enabled: boolean
  update_block_reason?: string | null
  rollback_enabled: boolean
  deployment_mode: 'blue_green' | 'stable'
  last_operation?: DeploymentOperation | null
}

export interface DeploymentStatus {
  components: Record<DeploymentComponent, DeploymentComponentStatus>
  active_operation?: DeploymentOperation | null
  latest_operation?: DeploymentOperation | null
  reconciliation: {
    automatic_after_update: boolean
    plaintext_key_accessed: boolean
    mutation_performed: boolean
  }
}

export async function getDeploymentStatus(): Promise<DeploymentStatus> {
  const { data } = await apiClient.get<DeploymentStatus>('/admin/deployments')
  return data
}

export async function startDeploymentAction(
  component: DeploymentComponent,
  action: DeploymentAction
): Promise<DeploymentOperation> {
  const { data } = await apiClient.post<DeploymentOperation>(
    `/admin/deployments/${component}/${action}`,
    {},
    {
      headers: {
        'X-Deployment-Confirmation': `${component}:${action}`
      }
    }
  )
  return data
}

export async function getDeploymentOperation(operationId: string): Promise<DeploymentOperation> {
  const { data } = await apiClient.get<DeploymentOperation>(
    `/admin/deployments/operations/${encodeURIComponent(operationId)}`
  )
  return data
}

export default {
  getStatus: getDeploymentStatus,
  startAction: startDeploymentAction,
  getOperation: getDeploymentOperation
}
