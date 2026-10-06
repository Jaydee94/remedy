import assert from 'node:assert/strict'
import { describe, it } from 'node:test'
import { isUndoKey } from './undokey.ts'

const key = (over: Partial<Parameters<typeof isUndoKey>[0]> = {}) => ({
  key: 'z',
  ctrlKey: true,
  metaKey: false,
  altKey: false,
  shiftKey: false,
  defaultPrevented: false,
  editing: false,
  ...over,
})

describe('isUndoKey', () => {
  it('takes Ctrl+Z and Cmd+Z, in either case', () => {
    assert.equal(isUndoKey(key()), true)
    assert.equal(isUndoKey(key({ key: 'Z' })), true)
    assert.equal(isUndoKey(key({ ctrlKey: false, metaKey: true })), true)
  })

  it('leaves Z alone, and the other combinations (redo is Shift, Alt is a layout key)', () => {
    assert.equal(isUndoKey(key({ ctrlKey: false })), false)
    assert.equal(isUndoKey(key({ shiftKey: true })), false)
    assert.equal(isUndoKey(key({ altKey: true })), false)
    assert.equal(isUndoKey(key({ key: 'y' })), false)
  })

  it('leaves the undo of a text field to the field, and a key that something else handled', () => {
    assert.equal(isUndoKey(key({ editing: true })), false)
    assert.equal(isUndoKey(key({ defaultPrevented: true })), false)
  })
})
