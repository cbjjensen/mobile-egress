import type { EC2Instance } from './api'

type WaitForSSMOnlineOptions = {
  instanceId: string
  listInstances: () => Promise<EC2Instance[]>
  onInventory: (inventory: EC2Instance[]) => void
  intervalMs?: number
  maxAttempts?: number
  wait?: (milliseconds: number) => Promise<void>
}

export type SSMWaitProgress = {
  startedAt: number
  lastCheckedAt?: number
  checkCount: number
}

export function formatSSMWaitDetails(progress: SSMWaitProgress, now?: number): string

export function waitForSSMOnline(options: WaitForSSMOnlineOptions): Promise<{
  online: boolean
  inventory: EC2Instance[]
}>
