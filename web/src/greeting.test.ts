import assert from 'node:assert/strict'
import { describe, it } from 'node:test'
import { greeting } from './greeting.ts'

describe('greeting', () => {
  it('says good morning from five to eleven', () => {
    assert.equal(greeting(5), 'Good morning')
    assert.equal(greeting(11), 'Good morning')
  })

  it('says good afternoon from noon to five in the afternoon', () => {
    assert.equal(greeting(12), 'Good afternoon')
    assert.equal(greeting(17), 'Good afternoon')
  })

  it('says good evening at the evening and through the night', () => {
    assert.equal(greeting(18), 'Good evening')
    assert.equal(greeting(23), 'Good evening')
    assert.equal(greeting(0), 'Good evening')
    assert.equal(greeting(4), 'Good evening')
  })
})
