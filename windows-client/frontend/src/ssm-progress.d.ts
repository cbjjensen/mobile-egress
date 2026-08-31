type WaitForSSMOnlineOptions = {
  checkOnline: () => Promise<boolean>
  onCheck: (online: boolean) => void
  onOnline?: () => Promise<void>
  maxAttempts?: number
  wait?: (milliseconds: number) => Promise<void>
}

export type SSMWaitProgress = {
  startedAt: number
  lastCheckedAt?: number
  checkCount: number
  setupSkipped?: boolean
}

export function formatSSMWaitDetails(progress: SSMWaitProgress, now?: number): string
export function shouldSkipSSMProfileSetup(instance: { ssmOnline?: boolean } | null | undefined): boolean
export function requiresSSMRoleConfirmation(reason: unknown): boolean
export function ssmWaitingStatusText(progress: SSMWaitProgress): string
export function ssmWaitingLiveText(progress: SSMWaitProgress, now?: number): string
export function ssmPollInterval(completedChecks: number): number

export function waitForSSMOnline(options: WaitForSSMOnlineOptions): Promise<{
  online: boolean
}>
