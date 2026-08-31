import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import test from 'node:test'

import { appendActivityEvent, filterActivityEvents, formatActivityEvents } from '../src/activity-log.js'

test('activity log keeps the newest 200 session events', () => {
  let events = []
  for (let index = 0; index < 205; index += 1) {
    events = appendActivityEvent(events, { id: String(index) })
  }

  assert.equal(events.length, 200)
  assert.equal(events[0].id, '204')
  assert.equal(events.at(-1).id, '5')
})

test('activity log filters events for one EC2 instance', () => {
  const events = [
    { id: 'system', instanceId: '' },
    { id: 'a', instanceId: 'i-a' },
    { id: 'b', instanceId: 'i-b' },
  ]

  assert.deepEqual(filterActivityEvents(events, 'all'), events)
  assert.deepEqual(filterActivityEvents(events, 'i-a'), [events[1]])
})

test('activity log copy output contains only the visible event fields', () => {
  const output = formatActivityEvents([{
    id: '1',
    timestamp: '2026-08-31T05:00:00.000Z',
    instanceId: 'i-a',
    instanceName: 'Bot 4.0',
    action: 'SSM',
    severity: 'success',
    message: 'Profile attached.',
  }])

  assert.equal(output, '2026-08-31T05:00:00.000Z [SUCCESS] Bot 4.0 (i-a) · SSM · Profile attached.')
})

test('EC2 UI exposes session-only filtered activity log controls without raw errors', async () => {
  const source = await readFile(new URL('../src/App.tsx', import.meta.url), 'utf8')

  assert.match(source, />Activity logs</)
  assert.match(source, />All instances</)
  assert.match(source, />Copy visible logs</)
  assert.match(source, />Clear logs</)
  assert.match(source, /Session only/)
  assert.doesNotMatch(source, /recordActivity\([^\n]*errorMessage\(/)
})
