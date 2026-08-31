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
  afterReboot?: boolean
  timeoutSeconds?: number
}

export function formatSSMWaitDetails(progress: SSMWaitProgress, now?: number): string
export function shouldSkipSSMProfileSetup(instance: { ssmOnline?: boolean } | null | undefined): boolean
export function requiresSSMRoleConfirmation(reason: unknown): boolean
export function ssmWaitingStatusText(progress: SSMWaitProgress): string
export function ssmWaitingLiveText(progress: SSMWaitProgress, now?: number): string
export function ssmPollInterval(completedChecks: number): number
export function ssmStatusState(status: { registered: boolean; online: boolean; lastPingAt?: string }, freshAfter?: string): 'unregistered' | 'offline' | 'stale' | 'ready'
export function formatSSMCheckActivity(status: { registered: boolean; online: boolean; pingStatus: string; agentVersion?: string; lastPingAt?: string }, checkCount: number, freshAfter?: string): string

export function waitForSSMOnline(options: WaitForSSMOnlineOptions): Promise<{
  online: boolean
}>
export function waitForSSMCredentialRefresh(options: Omit<WaitForSSMOnlineOptions, 'maxAttempts'>): Promise<{
  online: boolean
}>
export function runConfirmedSSMRestart(options: {
  confirmed: boolean
  reboot: () => Promise<void>
  monitor: () => Promise<{ online: boolean }>
}): Promise<{ outcome: 'cancelled' | 'online' | 'timeout' }>
