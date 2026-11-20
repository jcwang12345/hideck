import test from 'node:test'
import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'

const service = await readFile(new URL('../src/services/admin-resources.ts', import.meta.url), 'utf8')
const collection = await readFile(new URL('../src/components/resources/ResourceCollectionView.vue', import.meta.url), 'utf8')
const cards = await readFile(new URL('../src/views/Cards.vue', import.meta.url), 'utf8')
const pools = await readFile(new URL('../src/views/NumberPools.vue', import.meta.url), 'utf8')
const policies = await readFile(new URL('../src/views/DispatchPolicies.vue', import.meta.url), 'utf8')
const users = await readFile(new URL('../src/views/Users.vue', import.meta.url), 'utf8')
const views = await Promise.all([
  'Cards.vue',
  'NumberPools.vue',
  'DispatchPolicies.vue',
  'Users.vue',
  'CallRecords.vue',
  'AuditLog.vue'
].map(name => readFile(new URL(`../src/views/${name}`, import.meta.url), 'utf8')))

test('all resource pages use the production API contracts', () => {
  for (const endpoint of ['/cards', '/number-pools', '/dispatch-policies', '/admin/users', '/call-records', '/audit-events']) {
    assert.match(service, new RegExp(`'${endpoint.replaceAll('/', '\\/')}'`))
  }
  for (const source of views) {
    assert.match(source, /useResourceCollection/)
    assert.match(source, /ResourceCollectionView/)
  }
})

test('resource collection exposes loading error empty and responsive states', () => {
  assert.match(collection, /<ErrorState/)
  assert.match(collection, /<ListSkeleton/)
  assert.match(collection, /<EmptyState/)
  assert.match(collection, /role="table"/)
  assert.match(collection, /@media \(max-width: 900px\)/)
  assert.match(collection, /@media \(max-width: 480px\)/)
  assert.match(collection, /minmax\(0, 1fr\)/)
})

test('admin resource service exposes every management endpoint', () => {
  for (const fragment of [
    "'/phone-numbers'",
    "'/dispatch-policies'",
    '/members`, { members }',
    '/users`, { users }',
    '/disable`, { reason }',
    '/enable`, {})',
    '/actions/activate`, {})',
    "'/admin/users'",
    '/password`, { password }',
    '/sessions/revoke`, {})'
  ]) {
    assert.ok(service.includes(fragment), `missing management contract: ${fragment}`)
  }
})

test('admin controls are role gated and dangerous operations name their target', () => {
  for (const source of [cards, pools, policies, users]) {
    assert.match(source, /auth\.isAdmin/)
  }
  assert.match(cards, /ElMessageBox\.(confirm|prompt)/)
  assert.match(cards, /cardIsTransitioning/)
  assert.match(pools, /成员替换为/)
  assert.match(pools, /授权替换为/)
  assert.match(policies, /Asia\/Shanghai/)
  assert.match(policies, /次日 00:00/)
  assert.match(users, /撤销「.*」的全部登录会话/)
})

test('number and policy editors keep required management defaults visible', () => {
  assert.match(pools, /e164Error/)
  assert.match(pools, /validatePoolMembers/)
  assert.match(pools, /validatePoolUsers/)
  assert.match(policies, /DEFAULT_REJECTION_THRESHOLD/)
  assert.match(policies, /DEFAULT_DISABLE_SCOPE/)
  assert.match(policies, /DEFAULT_POLICY_TIMEZONE/)
  assert.match(policies, /DEFAULT_RESTORE_TIME/)
})
