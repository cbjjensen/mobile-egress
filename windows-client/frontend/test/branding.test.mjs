import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import test from 'node:test'

const branding = await import('../src/branding.js').catch(() => ({}))

test('frontend exposes the ZFNF product display name', () => {
  assert.equal(branding.productDisplayName, 'ZFNF Mobile Egress')
})

test('HTML fallback title identifies ZFNF Mobile Egress', async () => {
  const html = await readFile(new URL('../index.html', import.meta.url), 'utf8')

  assert.match(html, /<title>ZFNF Mobile Egress<\/title>/)
})
