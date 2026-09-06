import test from 'node:test'
import assert from 'node:assert/strict'
import { componentPresentation } from '../src/controller-status.js'
test('component freshness distinguishes checking, current, stale and failure', () => {
  assert.equal(componentPresentation(), 'Checking')
  assert.equal(componentPresentation({checking:true,stale:false}), 'Checking')
  assert.equal(componentPresentation({checking:false,stale:false}), 'Current')
  assert.equal(componentPresentation({checking:false,stale:true}), 'Stale')
  assert.equal(componentPresentation({checking:false,stale:false,error:'safe'}), 'Unavailable')
  assert.equal(componentPresentation({checking:false,stale:true,error:'safe'}), 'Unavailable / stale')
})
