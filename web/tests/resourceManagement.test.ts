import test from 'node:test'
import assert from 'node:assert/strict'
import {
  DEFAULT_DISABLE_SCOPE,
  DEFAULT_POLICY_TIMEZONE,
  DEFAULT_REJECTION_THRESHOLD,
  DEFAULT_RESTORE_TIME,
  cardIsTransitioning,
  cardTypePresentation,
  dispatchPolicyError,
  e164Error,
  passwordError,
  validatePoolMembers,
  validatePoolUsers
} from '../src/utils/resourceManagement'

test('E.164 validation accepts canonical international numbers only', () => {
  assert.equal(e164Error('+14155550100'), '')
  assert.equal(e164Error('+8613800138000'), '')
  assert.match(e164Error('14155550100'), /E\.164/)
  assert.match(e164Error('+0123'), /E\.164/)
  assert.match(e164Error('+1 415 555 0100'), /E\.164/)
  assert.match(e164Error('+1234567890123456'), /E\.164/)
})

test('pool member and user grants reject duplicate or invalid replacements', () => {
  assert.equal(validatePoolMembers([{ phone_number_id: 'n-1', priority: 0, weight: 1 }]), '')
  assert.match(validatePoolMembers([
    { phone_number_id: 'n-1', priority: 0, weight: 1 },
    { phone_number_id: 'n-1', priority: 1, weight: 2 }
  ]), /重复/)
  assert.match(validatePoolMembers([{ phone_number_id: 'n-1', priority: -1, weight: 1 }]), /优先级/)
  assert.match(validatePoolMembers([{ phone_number_id: 'n-1', priority: 0, weight: 0 }]), /权重/)
  assert.equal(validatePoolUsers([{ user_id: 'u-1' }]), '')
  assert.match(validatePoolUsers([{ user_id: 'u-1' }, { user_id: 'u-1' }]), /重复/)
})

test('dispatch policy defaults preserve whole-card Shanghai next-day recovery', () => {
  assert.equal(DEFAULT_REJECTION_THRESHOLD, 3)
  assert.equal(DEFAULT_DISABLE_SCOPE, 'card')
  assert.equal(DEFAULT_POLICY_TIMEZONE, 'Asia/Shanghai')
  assert.equal(DEFAULT_RESTORE_TIME, '00:00')
  assert.equal(dispatchPolicyError({ name: 'default', number_pool_id: 'pool-1', rejection_threshold: 3 }), '')
  assert.match(dispatchPolicyError({ name: 'default', number_pool_id: 'pool-1', rejection_threshold: 0 }), /拒接阈值/)
})

test('unknown card type explains missing evidence without claiming a normal SIM', () => {
  const presentation = cardTypePresentation({ card_type: 'unknown' })
  assert.equal(presentation.label, '卡型待确认')
  assert.equal(presentation.detail, '尚无 EID/ISD-R/明确物理 UICC 证据')
  assert.doesNotMatch(`${presentation.label}${presentation.detail}`, /普通 SIM/)
  assert.match(cardTypePresentation({ card_type: 'euicc', eid: '8901' }).detail, /EID/)
})

test('card profile operations lock while switching or registering', () => {
  assert.equal(cardIsTransitioning({ status: 'switching' }), true)
  assert.equal(cardIsTransitioning({ status: 'registering' }), true)
  assert.equal(cardIsTransitioning({ status: 'ready' }), false)
})

test('password validation catches weak management passwords before submission', () => {
  assert.match(passwordError('short', 'operator'), /8/)
  assert.match(passwordError('operator', 'operator'), /用户名/)
  assert.match(passwordError('abcdefgh', 'operator'), /两类/)
  assert.equal(passwordError('StrongPass9!', 'operator'), '')
})
