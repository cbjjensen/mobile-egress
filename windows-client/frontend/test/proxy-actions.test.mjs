import assert from 'node:assert/strict'
import test from 'node:test'

import { copyProxyLine, copySOCKS5URL, nodeProxyActions } from '../src/proxy-actions.js'

test('copy proxy line uses the default HTTP proxy binding', async () => {
  const writes = []
  const backend = {
    NodeProxyLine: async instanceId => {
      assert.equal(instanceId, 'i-http')
      return '127.0.0.1:1081:user:password'
    },
  }
  await copyProxyLine(backend, { writeText: async value => writes.push(value) }, 'i-http')
  assert.deepEqual(writes, ['127.0.0.1:1081:user:password'])
})

test('copy SOCKS5 URL uses the fallback SOCKS binding', async () => {
  const writes = []
  const backend = {
    NodeSOCKSProxyURL: async instanceId => {
      assert.equal(instanceId, 'i-socks')
      return 'socks5://user:password@127.0.0.1:1080'
    },
  }
  await copySOCKS5URL(backend, { writeText: async value => writes.push(value) }, 'i-socks')
  assert.deepEqual(writes, ['socks5://user:password@127.0.0.1:1080'])
})

test('node proxy actions require an updated Client before HTTP copying', () => {
  assert.deepEqual(nodeProxyActions({ httpProxyReady: false }), {
    primaryLabel: 'Copy proxy line',
    primaryDisabled: true,
    guidance: 'Update Client to enable HTTP proxying',
  })
  assert.deepEqual(nodeProxyActions({ httpProxyReady: true }), {
    primaryLabel: 'Copy proxy line',
    primaryDisabled: false,
    guidance: '',
  })
})
