import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import { createElement } from 'react'
import { renderToStaticMarkup } from 'react-dom/server'
import test from 'node:test'

const branding = await import('../src/branding.js').catch(() => ({}))

test('frontend exposes the ZFNF product display name', () => {
  assert.equal(branding.productDisplayName, 'ZFNF Mobile Egress')
})

test('HTML fallback title identifies ZFNF Mobile Egress', async () => {
  const html = await readFile(new URL('../index.html', import.meta.url), 'utf8')

  assert.match(html, /<title>ZFNF Mobile Egress<\/title>/)
})

test('brand identity renders the selected logo with the product heading', async () => {
  const brand = await import('../src/brand-identity.js').catch(() => ({}))

  assert.equal(typeof brand.BrandIdentity, 'function')
  const markup = renderToStaticMarkup(createElement(brand.BrandIdentity, {
    eyebrow: 'Personal cellular bridge',
    name: 'ZFNF Mobile Egress',
  }))
  assert.match(markup, /<img[^>]+src="\/zfnf-logo\.png"[^>]+alt=""/)
  assert.match(markup, /<p class="eyebrow">Personal cellular bridge<\/p>/)
  assert.match(markup, /<h1>ZFNF Mobile Egress<\/h1>/)
})
