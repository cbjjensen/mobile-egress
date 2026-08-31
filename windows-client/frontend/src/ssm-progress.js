const delay = milliseconds => new Promise(resolve => window.setTimeout(resolve, milliseconds))

const duration = seconds => `${Math.floor(seconds / 60)}:${String(seconds % 60).padStart(2, '0')}`

export function formatSSMWaitDetails(progress, now = Date.now()) {
  const timeoutSeconds = progress.timeoutSeconds ?? 300
  const elapsedSeconds = Math.min(timeoutSeconds, Math.max(0, Math.floor((now - progress.startedAt) / 1000)))
  const checks = `${progress.checkCount} ${progress.checkCount === 1 ? 'check' : 'checks'}`
  const recency = progress.lastCheckedAt == null
    ? 'checking now'
    : `last checked ${Math.max(0, Math.floor((now - progress.lastCheckedAt) / 1000))}s ago`
  return `${checks} · ${recency} · waiting ${duration(elapsedSeconds)} / ${duration(timeoutSeconds)}`
}

export function shouldSkipSSMProfileSetup(instance) {
  return Boolean(instance?.ssmOnline)
}

export function requiresSSMRoleConfirmation(reason) {
  const message = reason instanceof Error ? reason.message : typeof reason === 'string' ? reason : ''
  return message.includes('Explicit confirmation is required before adding AmazonSSMManagedInstanceCore to the existing role.')
}

export function ssmWaitingStatusText(progress) {
  if (progress?.afterReboot) return 'EC2 restarted · checking SSM'
  return progress?.setupSkipped ? 'Profile already configured · checking SSM' : 'SSM setup complete · checking SSM'
}

export function ssmWaitingLiveText(progress, now = Date.now()) {
  if (progress?.afterReboot) return `EC2 restart requested · waiting for a fresh SSM Agent ping · ${formatSSMWaitDetails(progress, now)}`
  const preparation = progress?.setupSkipped ? 'SSM permissions already configured' : 'SSM setup complete'
  return `${preparation} · allowing 30 seconds for credential refresh · ${formatSSMWaitDetails(progress, now)}`
}

export function ssmStatusState(status, freshAfter) {
  if (!status?.registered) return 'unregistered'
  if (!status.online) return 'offline'
  if (freshAfter && (!status.lastPingAt || Date.parse(status.lastPingAt) <= Date.parse(freshAfter))) return 'stale'
  return 'ready'
}

export function formatSSMCheckActivity(status, checkCount, freshAfter) {
  const state = ssmStatusState(status, freshAfter)
  const agent = status?.agentVersion ? ` · agent ${status.agentVersion}` : ''
  const lastPing = status?.lastPingAt ? ` · last ping ${status.lastPingAt}` : ''
  if (state === 'unregistered') return `Check ${checkCount}: no SSM registration record yet.`
  if (state === 'offline') return `Check ${checkCount}: SSM reports ${status.pingStatus || 'offline'}${agent}${lastPing}.`
  if (state === 'stale') return `Check ${checkCount}: SSM is online, but its last ping predates this restart${agent}${lastPing}.`
  return `${freshAfter ? 'SSM Agent reported online after restart' : 'SSM Agent reported online'}${agent}.`
}

export function ssmPollInterval(completedChecks) {
  if (completedChecks <= 15) return 2_000
  if (completedChecks <= 27) return 5_000
  return 10_000
}

export async function waitForSSMOnline({
  checkOnline,
  onCheck,
  onOnline = async () => {},
  maxAttempts = 49,
  wait = delay,
}) {
  for (let attempt = 0; attempt < maxAttempts; attempt += 1) {
    if (attempt > 0) await wait(ssmPollInterval(attempt))
    const online = await checkOnline()
    onCheck(online)
    if (online) {
      await onOnline()
      return { online: true }
    }
  }
  return { online: false }
}

export function waitForSSMCredentialRefresh(options) {
  return waitForSSMOnline({ ...options, maxAttempts: 16 })
}

export async function runConfirmedSSMRestart({ confirmed, reboot, monitor }) {
  if (!confirmed) return { outcome: 'cancelled' }
  await reboot()
  const result = await monitor()
  return { outcome: result.online ? 'online' : 'timeout' }
}
