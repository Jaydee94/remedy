import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { test } from 'node:test'
import { incidentStateText } from './incidents.ts'

/** The custom properties of the first :root block of index.css: the dark theme's tokens. */
function tokens(): Record<string, string> {
  const css = readFileSync(new URL('./index.css', import.meta.url), 'utf8')
  const block = /:root\s*\{([^}]*)\}/.exec(css)
  assert.ok(block, 'index.css has a :root block')
  const out: Record<string, string> = {}
  for (const m of block[1].matchAll(/--([a-z-]+):\s*(#[0-9a-fA-F]{6})\s*;/g)) out[m[1]] = m[2]
  return out
}

function luminance(hex: string): number {
  const channels = [1, 3, 5].map((i) => {
    const c = parseInt(hex.slice(i, i + 2), 16) / 255
    return c <= 0.03928 ? c / 12.92 : ((c + 0.055) / 1.055) ** 2.4
  })
  return 0.2126 * channels[0] + 0.7152 * channels[1] + 0.0722 * channels[2]
}

function contrast(a: string, b: string): number {
  const [hi, lo] = [luminance(a), luminance(b)].sort((x, y) => y - x)
  return (hi + 0.05) / (lo + 0.05)
}

function token(name: string): string {
  const value = tokens()[name]
  assert.ok(value, `--${name} is defined as a hex colour in the first :root block`)
  return value
}

const surfaces = ['background', 'card', 'sidebar']

test('--subtle is readable text on every surface', () => {
  for (const s of surfaces) {
    const ratio = contrast(token('subtle'), token(s))
    assert.ok(ratio >= 4.5, `--subtle on --${s} is ${ratio.toFixed(2)}, want at least 4.5`)
  }
})

test('--muted-foreground is readable text on every surface, and --subtle is quieter', () => {
  for (const s of surfaces) {
    const ratio = contrast(token('muted-foreground'), token(s))
    assert.ok(ratio >= 4.5, `--muted-foreground on --${s} is ${ratio.toFixed(2)}, want at least 4.5`)
  }
  assert.ok(luminance(token('subtle')) < luminance(token('muted-foreground')), '--subtle is darker than --muted-foreground')
})

test('--field, the border of a text field, is visible on the background and the card', () => {
  for (const s of ['background', 'card']) {
    const ratio = contrast(token('field'), token(s))
    assert.ok(ratio >= 3, `--field on --${s} is ${ratio.toFixed(2)}, want at least 3`)
  }
})

test('the ignored chip does not use --neutral as text', () => {
  assert.notEqual(incidentStateText.ignored, 'text-neutral')
})
