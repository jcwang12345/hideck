import test from 'node:test'
import assert from 'node:assert/strict'
import { canAccess, homePathForUser } from '../src/utils/accessControl'
import type { AuthUser } from '../src/types/admin-resources'

const admin: AuthUser = { id: 'admin-1', username: 'admin', role: 'admin', permissions: [] }
const member: AuthUser = { id: 'member-1', username: 'member', role: 'member', permissions: ['calls.read'] }

test('administrator can open management routes while a member cannot', () => {
  assert.equal(canAccess(admin, { roles: ['admin'] }), true)
  assert.equal(canAccess(member, { roles: ['admin'] }), false)
  assert.equal(canAccess(null, {}), false)
})

test('member permissions are explicit and disabled accounts are rejected', () => {
  assert.equal(canAccess(member, { permissions: ['calls.read'] }), true)
  assert.equal(canAccess(member, { permissions: ['calls.read', 'recordings.read'] }), false)
  assert.equal(canAccess({ ...member, disabled: true }, {}), false)
})

test('role home keeps members away from the hardware dashboard', () => {
  assert.equal(homePathForUser(admin), '/')
  assert.equal(homePathForUser(member), '/phone')
})

