import assert from 'node:assert/strict'
import test from 'node:test'

import { formatSSMCheckActivity, formatSSMWaitDetails, requiresSSMRoleConfirmation, runConfirmedSSMRestart, shouldSkipSSMProfileSetup, ssmPollInterval, ssmStatusState, ssmWaitingLiveText, ssmWaitingStatusText, waitForSSMCredentialRefresh, waitForSSMOnline } from '../src/ssm-progress.js'

test('SSM setup skips only when the instance is already online', () => {
  assert.equal(shouldSkipSSMProfileSetup({ profileArn: 'arn:aws:iam::123:instance-profile/mobile-egress', roleName: 'mobile-egress-ssm', ssmOnline: false }), false)
  assert.equal(shouldSkipSSMProfileSetup({ profileArn: '', roleName: '', ssmOnline: true }), true)
  assert.equal(shouldSkipSSMProfileSetup({ profileArn: 'arn:aws:iam::123:instance-profile/mobile-egress', roleName: 'mobile-egress-ssm', ssmOnline: true }), true)
})

test('SSM waiting status distinguishes verified configuration from completed setup', () => {
  const verified = { startedAt: 0, checkCount: 1, setupSkipped: true, timeoutSeconds: 30 }
  const changed = { startedAt: 0, checkCount: 1, setupSkipped: false, timeoutSeconds: 30 }

  assert.equal(ssmWaitingStatusText(verified), 'Profile already configured · checking SSM')
  assert.equal(ssmWaitingLiveText(verified, 1_000), 'SSM permissions already configured · allowing 30 seconds for credential refresh · 1 check · checking now · waiting 0:01 / 0:30')
  assert.equal(ssmWaitingStatusText(changed), 'SSM setup complete · checking SSM')
  assert.equal(ssmWaitingLiveText(changed, 1_000), 'SSM setup complete · allowing 30 seconds for credential refresh · 1 check · checking now · waiting 0:01 / 0:30')
})

test('SSM role confirmation is requested only for the explicit missing-policy response', () => {
  assert.equal(requiresSSMRoleConfirmation('Explicit confirmation is required before adding AmazonSSMManagedInstanceCore to the existing role.'), true)
  assert.equal(requiresSSMRoleConfirmation(new Error('Explicit confirmation is required before adding AmazonSSMManagedInstanceCore to the existing role.')), true)
  assert.equal(requiresSSMRoleConfirmation(new Error('Unable to check IAM permissions.')), false)
  assert.equal(requiresSSMRoleConfirmation({ message: 'Explicit confirmation is required before adding AmazonSSMManagedInstanceCore to the existing role.' }), false)
})

test('SSM wait details expose checks, recency, and bounded elapsed time', () => {
  assert.equal(
    formatSSMWaitDetails({ startedAt: 0, lastCheckedAt: 27_000, checkCount: 10, timeoutSeconds: 30 }, 30_000),
    '10 checks · last checked 3s ago · waiting 0:30 / 0:30',
  )
})

test('credential refresh offers recovery after thirty seconds instead of waiting five minutes', async () => {
  let calls = 0
  const waits = []

  const result = await waitForSSMCredentialRefresh({
    checkOnline: async () => {
      calls += 1
      return false
    },
    onCheck: () => {},
    wait: async milliseconds => { waits.push(milliseconds) },
  })

  assert.equal(result.online, false)
  assert.equal(calls, 16)
  assert.equal(waits.length, 15)
  assert.equal(waits.reduce((total, milliseconds) => total + milliseconds, 0), 30_000)
})

test('SSM diagnostics distinguish no registration, offline, stale post-reboot, and ready', () => {
  const requestedAt = '2026-08-31T18:00:00.000Z'

  assert.equal(ssmStatusState({ registered: false, online: false, pingStatus: 'NotRegistered' }), 'unregistered')
  assert.equal(ssmStatusState({ registered: true, online: false, pingStatus: 'ConnectionLost' }), 'offline')
  assert.equal(ssmStatusState({ registered: true, online: true, pingStatus: 'Online', lastPingAt: '2026-08-31T17:59:59.000Z' }, requestedAt), 'stale')
  assert.equal(ssmStatusState({ registered: true, online: true, pingStatus: 'Online', lastPingAt: '2026-08-31T18:00:01.000Z' }, requestedAt), 'ready')
  assert.equal(ssmStatusState({ registered: true, online: true, pingStatus: 'Online' }, requestedAt), 'stale')
})

test('SSM activity explains the observed registration state without raw provider errors', () => {
  const requestedAt = '2026-08-31T18:00:00.000Z'

  assert.equal(formatSSMCheckActivity({ registered: false, online: false, pingStatus: 'NotRegistered' }, 1), 'Check 1: no SSM registration record yet.')
  assert.equal(
    formatSSMCheckActivity({ registered: true, online: false, pingStatus: 'ConnectionLost', agentVersion: '3.3.3050.0', lastPingAt: '2026-08-31T17:50:00Z' }, 2),
    'Check 2: SSM reports ConnectionLost · agent 3.3.3050.0 · last ping 2026-08-31T17:50:00Z.',
  )
  assert.equal(
    formatSSMCheckActivity({ registered: true, online: true, pingStatus: 'Online', agentVersion: '3.3.3050.0', lastPingAt: '2026-08-31T17:59:59Z' }, 3, requestedAt),
    'Check 3: SSM is online, but its last ping predates this restart · agent 3.3.3050.0 · last ping 2026-08-31T17:59:59Z.',
  )
  assert.equal(
    formatSSMCheckActivity({ registered: true, online: true, pingStatus: 'Online', agentVersion: '3.3.3050.0', lastPingAt: '2026-08-31T18:00:01Z' }, 4, requestedAt),
    'SSM Agent reported online after restart · agent 3.3.3050.0.',
  )
})

test('SSM recovery never reboots without confirmation and waits after an approved reboot', async () => {
  const cancelledCalls = []
  const cancelled = await runConfirmedSSMRestart({
    confirmed: false,
    reboot: async () => { cancelledCalls.push('reboot') },
    monitor: async () => { cancelledCalls.push('monitor'); return { online: true } },
  })
  assert.deepEqual(cancelled, { outcome: 'cancelled' })
  assert.deepEqual(cancelledCalls, [])

  const approvedCalls = []
  const approved = await runConfirmedSSMRestart({
    confirmed: true,
    reboot: async () => { approvedCalls.push('reboot') },
    monitor: async () => { approvedCalls.push('monitor'); return { online: true } },
  })
  assert.deepEqual(approved, { outcome: 'online' })
  assert.deepEqual(approvedCalls, ['reboot', 'monitor'])
})

test('SSM polling uses a rapid selected-instance check and installs immediately when online', async () => {
  const statuses = [false, true]
  const observed = []
  const waits = []
  let installs = 0

  const result = await waitForSSMOnline({
    maxAttempts: 5,
    checkOnline: async () => statuses.shift(),
    onCheck: online => observed.push(online),
    onOnline: async () => { installs += 1 },
    wait: async milliseconds => { waits.push(milliseconds) },
  })

  assert.equal(result.online, true)
  assert.deepEqual(observed, [false, true])
  assert.deepEqual(waits, [2_000])
  assert.equal(installs, 1)
})

test('SSM polling returns offline after its bounded attempts', async () => {
  let calls = 0

  const result = await waitForSSMOnline({
    maxAttempts: 3,
    checkOnline: async () => {
      calls += 1
      return false
    },
    onCheck: () => {},
    wait: async () => {},
  })

  assert.equal(result.online, false)
  assert.equal(calls, 3)
})

test('SSM polling starts fast and backs off while preserving the five-minute bound', () => {
  assert.equal(ssmPollInterval(1), 2_000)
  assert.equal(ssmPollInterval(15), 2_000)
  assert.equal(ssmPollInterval(16), 5_000)
  assert.equal(ssmPollInterval(27), 5_000)
  assert.equal(ssmPollInterval(28), 10_000)
  assert.equal(ssmPollInterval(48), 10_000)
})
