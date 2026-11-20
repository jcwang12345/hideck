import assert from 'node:assert/strict'
import test from 'node:test'
import { readFile } from 'node:fs/promises'
import { createPinia, setActivePinia } from 'pinia'
import { phoneService, type PhoneCall, type PhoneNumberPool } from '../src/services/phone'
import { usePhoneStore } from '../src/stores/phone'
import {
  choosePhonePoolID,
  dispatchCallErrorMessage,
  phonePoolState
} from '../src/utils/phoneDispatch'

function pool(overrides: Partial<PhoneNumberPool> = {}): PhoneNumberPool {
  return {
    id: 'pool-us',
    name: '美国线路',
    status: 'active',
    member_count: 4,
    available_count: 2,
    occupied_count: 1,
    ...overrides
  }
}

function call(mediaId: string): PhoneCall {
  return {
    call_id: 'call-pool-1',
    device_id: 'server-selected-device',
    direction: 'outbound',
    peer: '+14155550100',
    status: 'calling',
    media_id: mediaId,
    started_at: '2026-11-20T08:00:00Z',
    read_only: false,
    pool_id: 'pool-us',
    pool_name: '美国线路'
  }
}

test('pool selection keeps an explicit choice and otherwise prefers an available pool', () => {
  const pools = [pool({ id: 'empty', available_count: 0 }), pool({ id: 'ready' })]
  assert.equal(choosePhonePoolID(pools), 'ready')
  assert.equal(choosePhonePoolID(pools, 'empty'), 'empty')
  assert.equal(choosePhonePoolID(pools, '', 'empty'), 'empty')
  assert.equal(choosePhonePoolID([pool({ enabled: false })]), '')
})

test('pool dispatch state distinguishes authorization capacity ownership and preparation', () => {
  const ready = pool()
  assert.equal(phonePoolState({ loading: false, error: '', pools: [], hasActiveCall: false, preparing: false }).code, 'no_pool')
  assert.equal(phonePoolState({ loading: false, error: '', pools: [pool({ enabled: false })], hasActiveCall: false, preparing: false }).code, 'pool_disabled')
  assert.equal(phonePoolState({ loading: false, error: '', pools: [ready], selected: pool({ available_count: 0 }), hasActiveCall: false, preparing: false }).code, 'no_resource')
  assert.equal(phonePoolState({ loading: false, error: '', pools: [ready], selected: ready, hasActiveCall: true, preparing: false }).code, 'user_busy')
  assert.equal(phonePoolState({ loading: false, error: '', pools: [ready], selected: ready, hasActiveCall: false, preparing: true }).code, 'preparing')
  const state = phonePoolState({ loading: false, error: '', pools: [ready], selected: ready, hasActiveCall: false, preparing: false })
  assert.equal(state.code, 'ready')
  assert.equal(state.canDial, true)
  assert.match(state.title, /2 条线路可用/)
})

test('dispatch API errors have stable actionable messages', () => {
  const expected: Record<string, RegExp> = {
    no_resource: /暂无可用线路/,
    user_busy: /已有一通/,
    profile_switch_failed: /Profile 切换失败/,
    ims_not_ready: /IMS 未就绪/,
    dial_failed: /呼叫建立失败/,
    dispatch_unavailable: /调度服务暂不可用/,
    forbidden_pool: /无权使用/
  }
  for (const [code, pattern] of Object.entries(expected)) {
    assert.match(dispatchCallErrorMessage({ response: { data: { code, message: 'server detail' } } }), pattern)
  }
})

test('pool calls prepare media and let the server choose the device', async () => {
  const original = phoneService.requestCall
  const received: string[][] = []
  phoneService.requestCall = async (poolID, callee, mediaID, lease) => {
    received.push([poolID, callee, mediaID, lease])
    return {
      request: { id: 'request-1', pool_id: poolID, state: 'dialing' },
      lease: { id: 'resource-lease-1', pool_id: poolID, state: 'dialing' },
      call: call(mediaID)
    }
  }
  try {
    setActivePinia(createPinia())
    const store = usePhoneStore()
    store.prepareReceiveOnlyMedia = async () => ({ mediaId: 'listen-media', lease: 'browser-lease' })
    await store.startPoolListenOnlyCall('pool-us', '+14155550100')
    assert.deepEqual(received, [['pool-us', '+14155550100', 'listen-media', 'browser-lease']])
    assert.equal(store.calls[0]?.call_id, 'call-pool-1')
  } finally {
    phoneService.requestCall = original
  }
})

test('member refresh never requests the device inventory', async () => {
  const originals = {
    devices: phoneService.devices,
    active: phoneService.active,
    history: phoneService.history
  }
  let deviceRequests = 0
  phoneService.devices = async () => { deviceRequests += 1; return [] }
  phoneService.active = async () => []
  phoneService.history = async () => []
  try {
    setActivePinia(createPinia())
    const store = usePhoneStore()
    store.includeDevices = false
    await store.refresh()
    assert.equal(deviceRequests, 0)
    assert.deepEqual(store.devices, [])
  } finally {
    phoneService.devices = originals.devices
    phoneService.active = originals.active
    phoneService.history = originals.history
  }
})

const serviceSource = await readFile(new URL('../src/services/phone.ts', import.meta.url), 'utf8')
const storeSource = await readFile(new URL('../src/stores/phone.ts', import.meta.url), 'utf8')
const viewSource = await readFile(new URL('../src/views/Phone.vue', import.meta.url), 'utf8')
const shellSource = await readFile(new URL('../src/layouts/AuthenticatedShell.vue', import.meta.url), 'utf8')
const historySource = await readFile(new URL('../src/components/PhoneCallHistory.vue', import.meta.url), 'utf8')

test('phone page uses the pool request contract and confines device dialing to admin diagnostics', () => {
  assert.match(serviceSource, /api\.get<\{ number_pools\?: PhoneNumberPool\[\] \}>\('\/number-pools'\)/)
  assert.match(serviceSource, /api\.post<PhoneCallRequestResult>\('\/phone\/call-requests', \{[\s\S]*pool_id: poolId,[\s\S]*callee,[\s\S]*media_id: mediaId/)
  assert.doesNotMatch(serviceSource.match(/async requestCall[\s\S]*?\n  \},/)?.[0] || '', /device_id/)
  assert.match(storeSource, /includeDevices \? phoneService\.devices\(\) : Promise\.resolve\(\[\]\)/)
  assert.match(shellSource, /phone\.initialize\(\{ includeDevices: auth\.isAdmin \}\)/)
  assert.match(viewSource, /v-if="!usingDiagnosticDial" class="pool-selector"/)
  assert.match(viewSource, /v-else-if="auth\.isAdmin" class="device-selector is-diagnostic"/)
  assert.match(viewSource, /指定当前线路，仍受设备占用、卡停用和调度锁限制，仅用于管理员线路诊断/)
  assert.doesNotMatch(viewSource, /绕过号码池|绕过.*权限|绕过.*调度/)
  assert.match(viewSource, /@redial="redialRecord"/)
  assert.match(viewSource, /runDispatchAction\('回拨', \(\) => phone\.startPoolCall\(selectedPoolID\.value, peer\)\)/)
  assert.doesNotMatch(historySource, /phone\.startCall\(record\.device_id/)
  assert.match(historySource, /emit\('redial', record\)/)
})
