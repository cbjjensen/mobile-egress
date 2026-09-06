import test from 'node:test'
import assert from 'node:assert/strict'
import { runSetupWorkflow } from '../src/setup-workflow.js'

const fresh = { platform: 'windows', ready: false, tailscaleInstalled: false, tailscaleOnline: false, relayServiceState: 'not-required' }
function fixture(initial = {}) {
  let bridge = { ...fresh, ...initial }
  const calls = []
  const api = {
    GetBridgeStatus: async () => ({ ...bridge }),
    InstallTailscale: async () => { calls.push('install'); bridge.tailscaleInstalled = true },
    ConnectTailscale: async () => { calls.push('connect'); bridge.tailscaleOnline = true; return bridge },
    SetupLocalBridge: async () => { calls.push('setup'); bridge.ready = true; return bridge },
    RepairLocalBridge: async () => { calls.push('repair'); bridge.ready = true; return bridge },
  }
  return { api, calls, bridge }
}

test('new computer installs, connects and creates bridge with one action', async () => {
  const f = fixture()
  assert.equal((await runSetupWorkflow(f.api)).ready, true)
  assert.deepEqual(f.calls, ['install', 'connect', 'setup'])
})
test('existing connected Tailscale skips installation and login', async () => {
  const f = fixture({ tailscaleInstalled: true, tailscaleOnline: true })
  await runSetupWorkflow(f.api)
  assert.deepEqual(f.calls, ['setup'])
})
test('macOS resumes once after observed approval without registering repeatedly', async () => {
  const f = fixture({ platform: 'macos', tailscaleInstalled: true, tailscaleOnline: true })
  let waits = 0
  f.api.SetupLocalBridge = async () => {
    f.calls.push('setup')
    if (f.calls.length === 1) f.bridge.relayServiceState = 'approval-required'
    else f.bridge.ready = true
    return { ...f.bridge }
  }
  await runSetupWorkflow(f.api, { wait: async () => { if (++waits === 2) f.bridge.relayServiceState = 'enabled' } })
  assert.equal(waits, 2)
  assert.deepEqual(f.calls, ['setup', 'setup'])
})
test('cancellation while waiting for approval prevents subsequent setup', async () => {
  const f = fixture({ platform: 'macos', tailscaleInstalled: true, tailscaleOnline: true })
  const controller = new AbortController()
  f.api.SetupLocalBridge = async () => { f.calls.push('setup'); f.bridge.relayServiceState = 'approval-required'; return f.bridge }
  await assert.rejects(runSetupWorkflow(f.api, { signal: controller.signal, wait: async () => controller.abort() }), /cancel/i)
  assert.deepEqual(f.calls, ['setup'])
})
test('installer rejection stops flow without reconnect or automatic retry', async () => {
  const f = fixture()
  f.api.InstallTailscale = async () => { f.calls.push('install'); throw new Error('Permission declined') }
  await assert.rejects(runSetupWorkflow(f.api), /Permission declined/)
  assert.deepEqual(f.calls, ['install'])
})
test('unavailable Tailscale status never triggers reinstallation', async () => {
  const f = fixture({ tailscaleError: 'Cannot verify installation' })
  await assert.rejects(runSetupWorkflow(f.api), /Cannot verify installation/)
  assert.deepEqual(f.calls, [])
})
test('existing Owner uses repair and endpoint rotation requires explicit action', async () => {
  const f = fixture({ tailscaleInstalled: true, tailscaleOnline: true, ownerReady: true })
  await runSetupWorkflow(f.api)
  assert.deepEqual(f.calls, ['repair'])
  f.bridge.ready = false; f.bridge.needsRotation = true
  await assert.rejects(runSetupWorkflow(f.api), /endpoint/i)
  assert.deepEqual(f.calls, ['repair'])
})

test('stale relay health does not block the repair needed to restore it', async () => {
  const f = fixture({ tailscaleInstalled: true, tailscaleOnline: true, ownerReady: true, stale: true })
  let time = 0
  f.api.RepairLocalBridge = async () => { f.calls.push('repair'); f.bridge.stale = false; f.bridge.ready = true; return f.bridge }
  await runSetupWorkflow(f.api, { now: () => time, wait: async () => { time += 61000 } })
  assert.deepEqual(f.calls, ['repair'])
})

test('reconnecting Tailscale can advance to repair while relay health is stale', async () => {
  const f = fixture({ tailscaleInstalled: true, tailscaleOnline: false, ownerReady: true, stale: true })
  let time = 0
  f.api.RepairLocalBridge = async () => { f.calls.push('repair'); f.bridge.stale = false; f.bridge.ready = true; return f.bridge }
  await runSetupWorkflow(f.api, { now: () => time, wait: async () => { time += 61000 } })
  assert.deepEqual(f.calls, ['connect', 'repair'])
})
