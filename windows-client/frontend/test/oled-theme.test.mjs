import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import test from 'node:test'

const css = await readFile(new URL('../src/styles.css', import.meta.url), 'utf8')

function cssVariable(name) {
  return css.match(new RegExp(`${name.replace(/[.*+?^${}()|[\\]\\\\]/g, '\\\\$&')}\\s*:\\s*([^;]+);`, 'i'))?.[1]?.trim()
}

function cssRule(selector) {
  const escaped = selector.replace(/[.*+?^${}()|[\\]\\\\]/g, '\\\\$&')
  return css.match(new RegExp(`${escaped}\\s*\\{([^}]*)\\}`, 'is'))?.[1] ?? ''
}

test('OLED theme exposes the canonical surface, text, and accent roles', () => {
  assert.deepEqual({
    canvas: cssVariable('--ip-canvas'),
    surfaceSubtle: cssVariable('--ip-surface-subtle'),
    surface: cssVariable('--ip-surface'),
    surfaceRaised: cssVariable('--ip-surface-raised'),
    action: cssVariable('--ip-action'),
    text: cssVariable('--ip-text'),
    muted: cssVariable('--ip-muted'),
    subtle: cssVariable('--ip-subtle'),
    primary: cssVariable('--ip-accent-primary'),
    info: cssVariable('--ip-accent-info'),
    violet: cssVariable('--ip-accent-violet'),
    warning: cssVariable('--ip-status-warning'),
    danger: cssVariable('--ip-status-danger'),
  }, {
    canvas: '#000000',
    surfaceSubtle: '#05070b',
    surface: '#080a0f',
    surfaceRaised: '#0b0e14',
    action: '#0c111a',
    text: '#f2f5fb',
    muted: '#aeb7c6',
    subtle: '#747d8c',
    primary: '#7ef2c5',
    info: '#7db7ff',
    violet: '#d6b3ff',
    warning: '#f4df74',
    danger: '#ff8d98',
  })
})

test('primary actions use solid mint with dark foreground text', () => {
  const rule = cssRule('button.primary')

  assert.match(rule, /background:\s*var\(--ip-accent-primary\)/)
  assert.match(rule, /color:\s*var\(--ip-text-on-accent\)/)
})

test('page canvas is true black without the displaced blue wash', () => {
  const body = cssRule('body')

  assert.match(body, /background:\s*var\(--ip-canvas\)/)
  assert.doesNotMatch(body, /gradient/i)
  assert.doesNotMatch(css, /#0787ff|#32d0ff|#55e881/i)
})

test('interactive controls retain visible focus and reduced-motion treatment', () => {
  assert.match(css, /:focus-visible[^{]*\{[^}]*var\(--ip-focus-ring\)/is)
  assert.match(css, /@media\s*\(prefers-reduced-motion:\s*reduce\)[^{]*\{[\s\S]*?animation:\s*none/is)
})
