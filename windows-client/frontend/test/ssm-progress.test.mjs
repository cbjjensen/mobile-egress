import assert from 'node:assert/strict'
import test from 'node:test'

import { formatSSMWaitDetails, waitForSSMOnline } from '../src/ssm-progress.js'

test('SSM wait details expose checks, recency, and bounded elapsed time', () => {
  assert.equal(
    formatSSMWaitDetails({ startedAt: 0, lastCheckedAt: 92_000, checkCount: 10 }, 95_000),
    '10 checks · last checked 3s ago · waiting 1:35 / 5:00',
  )
})

test('SSM polling reports each inventory update and stops when the instance is online', async () => {
  const inventories = [
    [{ id: 'i-test', ssmOnline: false }],
    [{ id: 'i-test', ssmOnline: true }],
  ]
  const observed = []
  let waits = 0

  const result = await waitForSSMOnline({
    instanceId: 'i-test',
    maxAttempts: 5,
    intervalMs: 1,
    listInstances: async () => inventories.shift(),
    onInventory: inventory => observed.push(inventory),
    wait: async () => { waits += 1 },
  })

  assert.equal(result.online, true)
  assert.equal(result.inventory[0].ssmOnline, true)
  assert.equal(observed.length, 2)
  assert.equal(waits, 1)
})

test('SSM polling returns offline after its bounded attempts', async () => {
  let calls = 0

  const result = await waitForSSMOnline({
    instanceId: 'i-test',
    maxAttempts: 3,
    intervalMs: 1,
    listInstances: async () => {
      calls += 1
      return [{ id: 'i-test', ssmOnline: false }]
    },
    onInventory: () => {},
    wait: async () => {},
  })

  assert.equal(result.online, false)
  assert.equal(calls, 3)
})
