import assert from 'node:assert/strict'
import test from 'node:test'
import {
  e911PromptKey,
  markE911PromptShown,
  type E911PromptStorage,
  wasE911PromptShown
} from '../src/utils/e911Onboarding'

function memoryStorage(): E911PromptStorage {
  const values = new Map<string, string>()
  return {
    getItem: key => values.get(key) ?? null,
    setItem: (key, value) => { values.set(key, value) }
  }
}

test('builds a stable prompt key without storing the raw ICCID', () => {
  const key = e911PromptKey('gw-02', '89014103211118510720')
  assert.equal(key, e911PromptKey(' GW-02 ', ' 89014103211118510720 '))
  assert.match(key, /^hideck:e911-address:v1:[0-9a-f]{8}$/)
  assert.doesNotMatch(key, /89014103211118510720/)
  assert.equal(e911PromptKey('', '89014103211118510720'), '')
})

test('remembers a successfully opened E911 address page per device and card', () => {
  const storage = memoryStorage()
  const firstCard = e911PromptKey('gw-02', 'card-a')
  const secondCard = e911PromptKey('gw-02', 'card-b')

  assert.equal(wasE911PromptShown(storage, firstCard), false)
  markE911PromptShown(storage, firstCard)
  assert.equal(wasE911PromptShown(storage, firstCard), true)
  assert.equal(wasE911PromptShown(storage, secondCard), false)
})

test('continues safely when browser storage is unavailable', () => {
  assert.equal(wasE911PromptShown(null, 'key'), false)
  assert.doesNotThrow(() => markE911PromptShown(null, 'key'))
})
