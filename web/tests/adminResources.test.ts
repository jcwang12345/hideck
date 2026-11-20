import test from 'node:test'
import assert from 'node:assert/strict'
import { normalizeCollection } from '../src/services/admin-resources'

test('resource collection accepts backend envelopes without inventing rows', () => {
  assert.deepEqual(normalizeCollection<{ id: string }>({ cards: [{ id: 'card-1' }] }, ['cards']), [{ id: 'card-1' }])
  assert.deepEqual(normalizeCollection<{ id: string }>({ data: { cards: [{ id: 'card-2' }] } }, ['cards']), [{ id: 'card-2' }])
  assert.deepEqual(normalizeCollection<{ id: string }>({ status: 'ok' }, ['cards']), [])
})

test('resource collection supports direct arrays and common list envelopes', () => {
  assert.deepEqual(normalizeCollection<number>([1, 2], ['records']), [1, 2])
  assert.deepEqual(normalizeCollection<number>({ items: [3, 4] }, ['records']), [3, 4])
  assert.deepEqual(normalizeCollection<number>(null, ['records']), [])
})

