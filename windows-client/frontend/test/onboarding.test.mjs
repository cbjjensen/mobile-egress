import test from 'node:test'
import assert from 'node:assert/strict'
import { nextSetupStep, canInstallNode } from '../src/onboarding.js'

test('Funnel alone never unlocks node installation', () => {
  assert.equal(canInstallNode({ ready: false, funnelReady: true }, { ssmOnline: true }, false, ''), false)
  assert.equal(nextSetupStep({ ready: false, tailscaleOnline: true }, true, []).tab, 'bridge')
})
test('setup resumes at the first incomplete dependency', () => {
  assert.equal(nextSetupStep({ ready: true }, false, []).tab, 'settings')
  assert.equal(nextSetupStep({ ready: true }, true, []).tab, 'nodes')
  assert.equal(nextSetupStep({ ready: true }, true, [{ health: 'installed' }]).label, 'Verify connectivity')
  assert.equal(canInstallNode({ ready: true }, { ssmOnline: true }, false, ''), true)
  assert.equal(canInstallNode({ ready: true }, { ssmOnline: false }, false, ''), false)
  assert.equal(canInstallNode({ ready: true }, { ssmOnline: true }, true, ''), false)
  assert.equal(canInstallNode({ ready: true }, { ssmOnline: true }, false, 'install'), false)
})
