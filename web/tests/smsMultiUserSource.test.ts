import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import test from 'node:test'

const serviceSource = await readFile(new URL('../src/services/sms.ts', import.meta.url), 'utf8')
const storeSource = await readFile(new URL('../src/stores/sms.ts', import.meta.url), 'utf8')
const viewSource = await readFile(new URL('../src/views/Sms.vue', import.meta.url), 'utf8')
const composerSource = await readFile(new URL('../src/components/sms/SmsComposer.vue', import.meta.url), 'utf8')
const dispatchSource = await readFile(new URL('../src/utils/smsDispatch.ts', import.meta.url), 'utf8')

test('member SMS uses the authorized number-pool contract without hardware selectors', () => {
  assert.match(serviceSource, /api\.get<\{ number_pools\?: NumberPoolResource\[\] \}>\('\/number-pools'\)/)
  assert.match(serviceSource, /pool_id: string/)
  assert.match(serviceSource, /number_id\?: string/)
  assert.match(serviceSource, /device_id\?: never/)
  assert.match(storeSource, /fetchNumberPools/)
  assert.match(viewSource, /auth\.isAdmin \? selectedDevice\.value : undefined/)
  assert.match(viewSource, /pool_id: selectedSendPoolId\.value/)
  assert.match(viewSource, /number_id: selectedSendNumberId\.value \|\| undefined/)

  const threadSend = viewSource.slice(
    viewSource.indexOf('async function sendToCurrentThread'),
    viewSource.indexOf('async function confirmDeleteMessage')
  )
  assert.match(threadSend, /effectiveSendMode\.value === 'diagnostic'/)
  assert.match(threadSend, /pool_id: selectedSendPoolId\.value/)
  assert.doesNotMatch(threadSend, /iccid:\s*t\.iccid|imsi:\s*t\.imsi/)
})

test('admin device sending is clearly isolated as diagnostics and members do not render the device rail', () => {
  assert.match(viewSource, /<SmsDeviceRail\s+v-if="auth\.isAdmin"/)
  assert.match(viewSource, /管理员设备诊断/)
  assert.match(viewSource, /仍受设备占用、卡停用和调度锁限制/)
  assert.match(viewSource, /effectiveSendMode\.value === 'diagnostic'/)
  assert.match(viewSource, /if \(!auth\.isAdmin\) \{[\s\S]*devices\.value = \[\]/)
})

test('SMS sending exposes actionable resource states and responsive controls', () => {
  for (const text of [
    '没有已授权号码池',
    'Profile 未激活',
    '正在切换 Profile',
    '正在通话或被占用'
  ]) assert.match(viewSource + serviceSource + composerSource + dispatchSource, new RegExp(text))
  assert.match(viewSource, /\.sms-main-layout\.is-member/)
  assert.match(viewSource, /@media \(max-width: 820px\)[\s\S]*\.sms-route-controls/)
  assert.match(composerSource, /:disabled="disabled \|\| sending \|\| !modelValue\.trim\(\)"/)
})
