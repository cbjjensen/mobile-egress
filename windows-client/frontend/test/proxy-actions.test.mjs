import assert from 'node:assert/strict'
import test from 'node:test'

import { copyProxyLine, copySOCKS5URL, nodeProxyActions } from '../src/proxy-actions.js'

test('copy proxy line uses the default HTTP proxy binding', async () => {
  const writes = []
  const backend = {
    NodeProxyLine: async instanceId => {
      assert.equal(instanceId, 'i-http')
      return '127.0.0.2:1081:user:password'
    },
  }
  await copyProxyLine(backend, { writeText: async value => writes.push(value) }, 'i-http')
  assert.deepEqual(writes, ['127.0.0.2:1081:user:password'])
})

test('copy SOCKS5 URL uses the fallback SOCKS binding', async () => {
  const writes = []
  const backend = {
    NodeSOCKSProxyURL: async instanceId => {
      assert.equal(instanceId, 'i-socks')
      return 'socks5://user:password@127.0.0.2:1080'
    },
  }
  await copySOCKS5URL(backend, { writeText: async value => writes.push(value) }, 'i-socks')
  assert.deepEqual(writes, ['socks5://user:password@127.0.0.2:1080'])
})

test('node proxy actions require an updated Client before either proxy format can be copied', () => {
  assert.deepEqual(nodeProxyActions({ proxyReady: false }), {
    primaryLabel: 'Copy proxy line',
    primaryDisabled: true,
    secondaryDisabled: true,
    guidance: 'Update Client to activate the 127.0.0.2 proxy endpoint',
  })
  assert.deepEqual(nodeProxyActions({ proxyReady: true }), {
    primaryLabel: 'Copy proxy line',
    primaryDisabled: false,
    secondaryDisabled: false,
    guidance: '',
  })
})
