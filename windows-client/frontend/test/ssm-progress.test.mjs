import assert from 'node:assert/strict'
import test from 'node:test'

import { formatSSMWaitDetails, requiresSSMRoleConfirmation, shouldSkipSSMProfileSetup, ssmPollInterval, ssmWaitingLiveText, ssmWaitingStatusText, waitForSSMOnline } from '../src/ssm-progress.js'

test('SSM setup skips only when the instance is already online', () => {
  assert.equal(shouldSkipSSMProfileSetup({ profileArn: 'arn:aws:iam::123:instance-profile/mobile-egress', roleName: 'mobile-egress-ssm', ssmOnline: false }), false)
  assert.equal(shouldSkipSSMProfileSetup({ profileArn: '', roleName: '', ssmOnline: true }), true)
  assert.equal(shouldSkipSSMProfileSetup({ profileArn: 'arn:aws:iam::123:instance-profile/mobile-egress', roleName: 'mobile-egress-ssm', ssmOnline: true }), true)
})

test('SSM waiting status distinguishes verified configuration from completed setup', () => {
  const verified = { startedAt: 0, checkCount: 1, setupSkipped: true }
  const changed = { startedAt: 0, checkCount: 1, setupSkipped: false }

  assert.equal(ssmWaitingStatusText(verified), 'Profile already configured · checking SSM')
  assert.equal(ssmWaitingLiveText(verified, 1_000), 'SSM permissions already configured · checking this instance rapidly · 1 check · checking now · waiting 0:01 / 5:00')
  assert.equal(ssmWaitingStatusText(changed), 'SSM setup complete · checking SSM')
  assert.equal(ssmWaitingLiveText(changed, 1_000), 'SSM setup complete · checking this instance rapidly · 1 check · checking now · waiting 0:01 / 5:00')
})

test('SSM role confirmation is requested only for the explicit missing-policy response', () => {
  assert.equal(requiresSSMRoleConfirmation('Explicit confirmation is required before adding AmazonSSMManagedInstanceCore to the existing role.'), true)
  assert.equal(requiresSSMRoleConfirmation(new Error('Explicit confirmation is required before adding AmazonSSMManagedInstanceCore to the existing role.')), true)
  assert.equal(requiresSSMRoleConfirmation(new Error('Unable to check IAM permissions.')), false)
  assert.equal(requiresSSMRoleConfirmation({ message: 'Explicit confirmation is required before adding AmazonSSMManagedInstanceCore to the existing role.' }), false)
})

test('SSM wait details expose checks, recency, and bounded elapsed time', () => {
  assert.equal(
    formatSSMWaitDetails({ startedAt: 0, lastCheckedAt: 92_000, checkCount: 10 }, 95_000),
    '10 checks · last checked 3s ago · waiting 1:35 / 5:00',
  )
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
