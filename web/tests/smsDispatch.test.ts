import assert from 'node:assert/strict'
import test from 'node:test'
import {
  chooseSmsPoolID,
  smsDispatchErrorMessage,
  smsPoolAvailableCount,
  smsPoolEnabled
} from '../src/utils/smsDispatch'

test('SMS pool selection keeps an enabled choice and otherwise prefers available capacity', () => {
  const pools = [
    { id: 'disabled', enabled: false, available_count: 5 },
    { id: 'waiting', enabled: true, available_count: 0 },
    { id: 'ready', enabled: true, available_count: 2 }
  ]
  assert.equal(chooseSmsPoolID(pools, 'waiting'), 'waiting')
  assert.equal(chooseSmsPoolID(pools, 'missing'), 'ready')
  assert.equal(smsPoolEnabled(pools[0]), false)
  assert.equal(smsPoolAvailableCount({ available_count: -3 }), 0)
})

test('SMS dispatch errors explain pool, profile switching and device busy states', () => {
  assert.match(smsDispatchErrorMessage({ code: 'sms_pool_forbidden', message: 'raw' }), /无权使用/)
  assert.match(smsDispatchErrorMessage({ code: 'sms_profile_inactive', message: 'raw' }), /Profile 未激活/)
  assert.match(smsDispatchErrorMessage({ code: 'sms_profile_switching', message: 'raw' }), /正在切换 Profile/)
  assert.match(smsDispatchErrorMessage({ code: 'sms_device_busy', message: 'raw' }), /通话或被占用/)
  assert.equal(smsDispatchErrorMessage({ code: 'unknown', message: '服务端说明' }), '服务端说明')
})
