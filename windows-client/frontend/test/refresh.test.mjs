import test from 'node:test'
import assert from 'node:assert/strict'
import { createRefreshController } from '../src/refresh.js'

test('overlapping refreshes share a request and action invalidation drops old results', async () => {
  let finish, calls = 0
  const seen = []
  const refresh = createRefreshController(() => { calls++; return new Promise(resolve => { finish = resolve }) }, value => seen.push(value))
  const first = refresh.run()
  const second = refresh.run()
  assert.equal(calls, 1)
  refresh.pause()
  finish('old')
  await Promise.all([first, second])
  assert.deepEqual(seen, [])
  await refresh.run()
  assert.equal(calls, 1)
  refresh.resume()
  const next = refresh.run()
  finish('new')
  await next
  assert.deepEqual(seen, ['new'])
})

test('a failed refresh releases the request so the next poll can recover', async () => {
  let calls = 0
  const seen = []
  const refresh = createRefreshController(async () => { if (++calls === 1) throw Error('offline'); return 'online' }, value => seen.push(value))
  await assert.rejects(refresh.run(), /offline/)
  await refresh.run()
  assert.deepEqual(seen, ['online'])
})

test('a read invalidated by an action cannot replace the action error', async () => {
  let reject
  const refresh = createRefreshController(() => new Promise((_, fail) => { reject = fail }), () => {})
  const reading = refresh.run()
  refresh.pause()
  reject(Error('stale failure'))
  await reading
})

test('resuming during an old request waits for it and then performs a fresh read', async () => {
  const finish = [], seen = []
  const refresh = createRefreshController(() => new Promise(resolve => finish.push(resolve)), value => seen.push(value))
  const old = refresh.run()
  refresh.pause()
  refresh.resume()
  const next = refresh.run()
  assert.equal(finish.length, 1)
  finish[0]('old')
  await old
  await new Promise(resolve => setImmediate(resolve))
  assert.equal(finish.length, 2)
  finish[1]('new')
  await next
  assert.deepEqual(seen, ['new'])
})
