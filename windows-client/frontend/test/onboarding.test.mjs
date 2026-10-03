import test from 'node:test'
import assert from 'node:assert/strict'
import { nextSetupStep, canInstallNode } from '../src/onboarding.js'

test('Funnel alone never unlocks node installation', () => {
  assert.equal(canInstallNode({ ready: false, funnelReady: true }, { ssmOnline: true }, false, ''), false)
  assert.equal(nextSetupStep({ ready: false, tailscaleOnline: true }, true, []).tab, 'bridge')
})
test('setup resumes at the first incomplete dependency', () => {
  assert.equal(nextSetupStep({ ready: true, agentConnected: true }, false, []).tab, 'nodes')
  assert.equal(nextSetupStep({ ready: true, agentConnected: true }, true, []).tab, 'nodes')
  assert.equal(nextSetupStep({ ready: true, agentConnected: true }, true, [{ health: 'installed' }]).label, 'Verify connectivity')
  assert.equal(canInstallNode({ ready: true }, { ssmOnline: true }, false, ''), true)
  assert.equal(canInstallNode({ ready: true }, { ssmOnline: false }, false, ''), false)
  assert.equal(canInstallNode({ ready: true }, { ssmOnline: true }, true, ''), false)
  assert.equal(canInstallNode({ ready: true }, { ssmOnline: true }, false, 'install'), false)
})

test('phone pairing and sharing are separate steps before AWS', () => {
  const bridge = { ready: true, agentPaired: false, agentConnected: false }
  assert.equal(nextSetupStep(bridge, false, []).action, 'pair')
  assert.equal(nextSetupStep({ ...bridge, agentPaired: true }, false, []).label, 'Start cellular sharing')
  assert.equal(nextSetupStep({ ready: true }, false, []).label, 'Connect your Agent')
})

test('next action performs setup and completion requires a confirmed application request', () => {
  assert.equal(nextSetupStep({ ready: false }, false, []).action, 'setup')
  const bridge = { ready: true, agentConnected: true }
  const nodes = [{ health: 'installed' }]
  assert.equal(nextSetupStep(bridge, true, nodes).complete, undefined)
  assert.equal(nextSetupStep(bridge, true, nodes, { verified: true }).complete, true)
  assert.equal(nextSetupStep({ ...bridge, agentConnected: false }, true, nodes, { verified: true }).complete, undefined)
})

test('AWS validation only gates the explicitly selected AWS setup path', () => {
  const bridge = { ready: true, agentConnected: true }
  assert.equal(nextSetupStep(bridge, false, [], { awsChecking: true }).label, 'Add your Client')
  assert.equal(nextSetupStep(bridge, false, [], { awsChecking: true, clientMethod: 'aws' }).label, 'Checking saved AWS connection')
  assert.equal(nextSetupStep(bridge, false, [], { clientMethod: 'aws' }).label, 'Connect AWS')
  assert.equal(nextSetupStep(bridge, false, [{health:'installed',management:'paired'}], {awsChecking:true,verified:true}).complete,true)
})

test('setup guidance waits for initial and stale bridge checks', () => {
  assert.equal(nextSetupStep({checking:true},false,[]).label,'Checking bridge status')
  assert.equal(nextSetupStep({stale:true},false,[]).label,'Waiting for current bridge status')
})
