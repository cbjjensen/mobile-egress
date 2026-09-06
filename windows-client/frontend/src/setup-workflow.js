function checkCancelled(signal) {
  if (signal?.aborted) throw new Error('Setup cancelled. You can continue when ready.')
}

function delay(signal) {
  return new Promise((resolve, reject) => {
    const done = () => { signal?.removeEventListener('abort', abort); resolve() }
    const timer = setTimeout(done, 1000)
    const abort = () => { clearTimeout(timer); signal?.removeEventListener('abort', abort); reject(new Error('Setup cancelled.')) }
    signal?.addEventListener('abort', abort, { once: true })
    if (signal?.aborted) abort()
  })
}

// Only runs in response to a user action. Observation never registers a service
// or starts an installer; a pending approval can therefore be polled safely.
export async function runSetupWorkflow(api, { signal, onStage = () => {}, onBridge = () => {}, wait = () => delay(signal), now = Date.now } = {}) {
  async function observeUntil(predicate, timeout, guidance, requireFresh = true) {
    const deadline = now() + timeout
    for (;;) {
      checkCancelled(signal)
      const bridge = await api.GetBridgeStatus()
      onBridge(bridge)
      checkCancelled(signal)
      if (!bridge.checking && (!requireFresh || !bridge.stale) && predicate(bridge)) return bridge
      if (now() >= deadline) throw new Error(guidance)
      await wait()
    }
  }
  onStage('Checking this computer…')
  // Repair must be available when a broken relay has made its health stale.
  // Backend operations revalidate prerequisites; final success requires freshness.
  let bridge = await observeUntil(() => true, 60000, 'Status is still unavailable. Wait for the checks to finish, then continue setup.', false)
  if (bridge.ready) return bridge
  if (bridge.tailscaleError) throw new Error(bridge.tailscaleError)
  if (bridge.needsRotation) throw new Error('The endpoint changed. Use Rotate endpoint safely to continue.')
  if (!bridge.tailscaleInstalled) {
    onStage('Preparing Tailscale…')
    await api.InstallTailscale()
    checkCancelled(signal)
  }
  if (!bridge.tailscaleOnline) {
    onStage('Connect Tailscale in the window or browser that opens.')
    await api.ConnectTailscale()
    bridge = await observeUntil(value => value.tailscaleOnline && !value.tailscaleError, 60000, 'Tailscale is not connected yet. Finish its permissions and sign-in, then continue setup.', false)
  }
  checkCancelled(signal)
  onStage('Setting up your background relay…')
  bridge = bridge.ownerReady ? await api.RepairLocalBridge() : await api.SetupLocalBridge()
  onBridge(bridge)
  checkCancelled(signal)
  if (bridge.platform === 'macos' && bridge.relayServiceState === 'approval-required') {
    onStage('Allow ZFNF Mobile Egress in Login Items. Setup will continue after approval.')
    bridge = await observeUntil(value => value.relayServiceState === 'enabled', 10 * 60000, 'Background service approval is still pending. Approve it in Login Items, then continue setup.')
    checkCancelled(signal)
    onStage('Finishing your bridge…')
    bridge = await api.SetupLocalBridge()
    onBridge(bridge)
  }
  checkCancelled(signal)
  onStage('Checking your bridge connection…')
  return observeUntil(value => value.ready, 60000, 'Bridge setup needs another check. Review the connection details, then continue setup.')
}
