const delay = milliseconds => new Promise(resolve => window.setTimeout(resolve, milliseconds))

const duration = seconds => `${Math.floor(seconds / 60)}:${String(seconds % 60).padStart(2, '0')}`

export function formatSSMWaitDetails(progress, now = Date.now()) {
  const elapsedSeconds = Math.min(300, Math.max(0, Math.floor((now - progress.startedAt) / 1000)))
  const checks = `${progress.checkCount} ${progress.checkCount === 1 ? 'check' : 'checks'}`
  const recency = progress.lastCheckedAt == null
    ? 'checking now'
    : `last checked ${Math.max(0, Math.floor((now - progress.lastCheckedAt) / 1000))}s ago`
  return `${checks} · ${recency} · waiting ${duration(elapsedSeconds)} / 5:00`
}

export function shouldSkipSSMProfileSetup(instance) {
  return Boolean(instance?.ssmOnline)
}

export function requiresSSMRoleConfirmation(reason) {
  const message = reason instanceof Error ? reason.message : typeof reason === 'string' ? reason : ''
  return message.includes('Explicit confirmation is required before adding AmazonSSMManagedInstanceCore to the existing role.')
}

export function ssmWaitingStatusText(progress) {
  return progress?.setupSkipped ? 'Profile already configured · checking SSM' : 'SSM setup complete · checking SSM'
}

export function ssmWaitingLiveText(progress, now = Date.now()) {
  const preparation = progress?.setupSkipped ? 'SSM permissions already configured' : 'SSM setup complete'
  return `${preparation} · checking this instance rapidly · ${formatSSMWaitDetails(progress, now)}`
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
