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

export async function waitForSSMOnline({
  instanceId,
  listInstances,
  onInventory,
  intervalMs = 10_000,
  maxAttempts = 30,
  wait = delay,
}) {
  let inventory = []
  for (let attempt = 0; attempt < maxAttempts; attempt += 1) {
    if (attempt > 0) await wait(intervalMs)
    inventory = await listInstances()
    onInventory(inventory)
    if (inventory.some(instance => instance.id === instanceId && instance.ssmOnline)) {
      return { online: true, inventory }
    }
  }
  return { online: false, inventory }
}
