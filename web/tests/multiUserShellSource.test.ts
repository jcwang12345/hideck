import test from 'node:test'
import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'

const auth = await readFile(new URL('../src/stores/auth.ts', import.meta.url), 'utf8')
const router = await readFile(new URL('../src/router/index.ts', import.meta.url), 'utf8')
const shell = await readFile(new URL('../src/layouts/AuthenticatedShell.vue', import.meta.url), 'utf8')
const app = await readFile(new URL('../src/App.vue', import.meta.url), 'utf8')

test('authenticated identity comes from the current-user and logout contracts', () => {
  assert.match(auth, /api\.get<MeResponse>\('\/auth\/me'\)/)
  assert.match(auth, /api\.post\('\/auth\/logout'\)/)
  assert.match(auth, /user: AuthUser \| null/)
  assert.match(auth, /displayName:/)
})

test('router protects administrator resources and exposes a forbidden page', () => {
  for (const path of ['/cards', '/number-pools', '/dispatch-policies', '/users', '/audit-log']) {
    assert.match(router, new RegExp(`path: '${path.replaceAll('/', '\\/')}'[\\s\\S]*?roles: \\['admin'\\]`))
  }
  assert.match(router, /path: '\/call-records'[\s\S]*?requiresAuth: true/)
  assert.match(router, /name: 'Forbidden'/)
  assert.match(router, /canAccess\(authStore\.user/)
})

test('desktop and mobile navigation are derived from the real role', () => {
  assert.match(shell, /canAccess\(auth\.user, item\)/)
  assert.match(shell, /auth\.isAdmin \? \[/)
  assert.match(shell, /auth\.displayName/)
  assert.match(shell, /roleLabel/)
  assert.doesNotMatch(shell, /\{\{ t\('nav\.admin'\) \}\}/)
  assert.match(shell, /gridTemplateColumns: `repeat\(\$\{mobileMenuItems\.length\}/)
})

test('member startup skips administrator-only time and disclaimer routes', () => {
  assert.match(app, /\[auth\.isAuthenticated, auth\.role\]/)
  assert.match(app, /if \(role !== 'admin'\)[\s\S]*deviceTimeState\.value = 'ready'[\s\S]*disclaimerState\.value = 'ready'/)
  assert.match(app, /auth\.isAdmin && canShowDisclaimer/)
})
