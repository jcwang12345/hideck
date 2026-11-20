<script setup lang="ts">
import PhoneMcpConnection from '../components/phone/PhoneMcpConnection.vue'
import { computed, onMounted, onUnmounted, ref, watch } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import {
  Backspace24Regular,
  Call24Regular,
  CallDismiss24Regular,
  CallEnd24Regular,
  Dialpad24Regular,
  LockClosed24Regular,
  Mic24Regular,
  MicOff24Regular,
  Pause24Regular,
  PersonAdd24Regular,
  Play24Regular,
  Speaker224Regular
} from '@vicons/fluent'
import PageHeader from '../components/PageHeader.vue'
import { t } from '../i18n'
import PhoneCallHistory from '../components/PhoneCallHistory.vue'
import PhoneContactsPanel from '../components/PhoneContactsPanel.vue'
import PhoneDialPad from '../components/PhoneDialPad.vue'
import type { PhoneCall, PhoneDevice, PhoneNumberPool, PhoneRecord } from '../services/phone'
import { phoneService } from '../services/phone'
import { devicesService } from '../services/devices'
import { usePhoneStore } from '../stores/phone'
import { useAuthStore } from '../stores/auth'
import { usePhoneIdentity } from '../composables/usePhoneIdentity'
import { usePhoneDeviceSelection } from '../composables/usePhoneDeviceSelection'
import { phoneContactsService } from '../services/phone-contacts'
import { dialNumberError, formatCallDuration, phoneCallStatusLabel, phoneErrorMessage } from '../utils/phone'
import {
  choosePhonePoolID,
  dispatchCallErrorMessage,
  phonePoolState,
  poolAvailableCount,
  poolIsEnabled,
  poolMemberCount
} from '../utils/phoneDispatch'
import { confirmPhoneModeChange, phoneModeDisabledReason } from '../utils/phoneModeSwitch'

const phone = usePhoneStore()
const auth = useAuthStore()
const identities = usePhoneIdentity()
const { selectedDevice, rememberDevice } = usePhoneDeviceSelection({
  devices: () => phone.devices,
  isReady: isDeviceReady,
  storage: () => window.localStorage,
  onStorageError: () => ElMessage.warning('无法保存或读取拨号设备，请检查浏览器存储权限')
})
const callee = ref('')
const action = ref('')
const keypadVisible = ref(false)
const lastDTMF = ref('')
const mobileSetupOpen = ref(false)
const diagnosticDirectDial = ref(false)
const numberPools = ref<PhoneNumberPool[]>([])
const poolsLoading = ref(true)
const poolsError = ref('')
let preferredPoolID = ''
try {
  preferredPoolID = window.localStorage.getItem('hideck_phone_pool') || ''
} catch {
  // Pool selection remains usable when browser storage is restricted.
}
const selectedPoolID = ref('')

const usingDiagnosticDial = computed(() => auth.isAdmin && diagnosticDirectDial.value)
function isCurrentUserCall(item: PhoneCall) {
  if (!auth.isAdmin) return true
  if (item.media_id && item.media_id === phone.mediaId) return true
  if (item.user_id) return item.user_id === auth.user?.id
  return !item.read_only
}
const call = computed(() => {
  if (usingDiagnosticDial.value) return phone.currentCall
  return phone.calls.find((item) => item.media_id === phone.mediaId && isCurrentUserCall(item))
    || phone.calls.find(isCurrentUserCall)
})
const callEnding = computed(() => call.value ? phone.isCallEnding(call.value.call_id) : false)
const connected = computed(() => call.value?.status === 'connected')
const modemCall = computed(() => call.value?.call_id.startsWith('modemvoice-') === true)
const incoming = computed(() => call.value?.direction === 'inbound'
  && call.value.status === 'ringing'
  && !call.value.media_id)
const waitingCall = computed(() => phone.calls.find((item) =>
  item.status === 'waiting'
  && item.call_id !== call.value?.call_id
  && (usingDiagnosticDial.value || isCurrentUserCall(item))))
const selected = computed(() => phone.devices.find((device) => device.id === selectedDevice.value))
const selectedPool = computed(() => numberPools.value.find((pool) => pool.id === selectedPoolID.value))
const identityDeviceID = computed(() => usingDiagnosticDial.value ? selectedDevice.value : '')
const dialRegion = computed(() => usingDiagnosticDial.value ? selected.value?.phone_region : selectedPool.value?.phone_region)
const dialCountryCode = computed(() => usingDiagnosticDial.value ? selected.value?.phone_country_code : selectedPool.value?.phone_country_code)
const calleeError = computed(() => dialNumberError(callee.value, dialRegion.value))
const calleeAttribution = computed(() => {
  const subtitle = identities.subtitleFor(callee.value, identityDeviceID.value)
  if (!subtitle) return ''
  return `${identities.titleFor(callee.value, identityDeviceID.value)} · ${subtitle}`
})
const activeCallStatuses = new Set(['calling', 'ringing', 'waiting', 'connected'])
const hasCurrentUserCall = computed(() => phone.calls.some((item) => {
  if (!activeCallStatuses.has(item.status)) return false
  return isCurrentUserCall(item)
}))
const dispatchState = computed(() => phonePoolState({
  loading: poolsLoading.value,
  error: poolsError.value,
  pools: numberPools.value,
  selected: selectedPool.value,
  hasActiveCall: hasCurrentUserCall.value,
  preparing: !!action.value
}))
const canPlaceCall = computed(() => {
  if (!callee.value || !!calleeError.value) return false
  if (!usingDiagnosticDial.value) return dispatchState.value.canDial
  return !!selected.value
    && (isDeviceReady(selected.value) || selected.value.phone_mode === 'cellular' || selected.value.phone_mode === 'volte')
    && !isDeviceBusy(selected.value)
})
watch(call, (current, previous) => {
  if (!current || current.status !== 'connected') keypadVisible.value = false
  if (current?.peer) void identities.resolve(current.peer, current.device_id)
  if (waitingCall.value?.peer) void identities.resolve(waitingCall.value.peer, waitingCall.value.device_id)
  if (previous && !current) void loadPools()
})
watch(() => phone.history.map((item) => `${item.device_id}\u0000${item.peer}`).join('|'), () => {
  for (const record of phone.history) {
    if (record.peer) void identities.resolve(record.peer, record.device_id)
  }
}, { immediate: true })
watch([callee, identityDeviceID, dialRegion], ([value, deviceId, region]) => {
  if (value && !dialNumberError(value, region)) void identities.resolve(value, deviceId)
})

watch(numberPools, (pools) => {
  selectedPoolID.value = choosePhonePoolID(pools, selectedPoolID.value, preferredPoolID)
}, { immediate: true })

onMounted(async () => {
  await Promise.all([
    phone.initialize({ includeDevices: auth.isAdmin }),
    loadPools()
  ])
})

async function loadPools() {
  poolsLoading.value = true
  poolsError.value = ''
  try {
    numberPools.value = await phoneService.numberPools()
  } catch (error) {
    poolsError.value = phoneErrorMessage(error, '号码池加载失败')
  } finally {
    poolsLoading.value = false
  }
}

function rememberPool(value: unknown) {
  if (typeof value !== 'string' || !numberPools.value.some(pool => pool.id === value)) return
  preferredPoolID = value
  selectedPoolID.value = value
  try {
    window.localStorage.setItem('hideck_phone_pool', value)
  } catch {
    // The current selection still works for this tab.
  }
}

function setDiagnosticDirectDial(enabled: boolean) {
  if (!auth.isAdmin) return
  diagnosticDirectDial.value = enabled
  mobileSetupOpen.value = false
  phone.clearError()
}

const statusAbort = new AbortController()
let statusPending = false
let modeRevision = 0
const statusTimer = window.setInterval(async () => {
  if (!usingDiagnosticDial.value || selected.value?.phone_mode !== 'modem_voice' || statusPending || modePending.value) return
  statusPending = true
  const revision = modeRevision
  try {
    const devices = await phoneService.devices(statusAbort.signal)
    if (!statusAbort.signal.aborted && !modePending.value && revision === modeRevision) phone.devices = devices
  } catch (error) {
    if (!statusAbort.signal.aborted) phone.error = phoneErrorMessage(error, '模组直拨状态刷新失败')
  } finally { statusPending = false }
}, 2_000)
onUnmounted(() => { window.clearInterval(statusTimer); statusAbort.abort() })

function isDeviceReady(device: PhoneDevice) {
  if (device.phone_mode === 'volte') {
    if (device.native_volte?.voice_indications_ready === false) return false
    return device.voice.ready === true || device.native_volte?.ims_registered === true
      || device.native_volte?.phase === 'registered'
  }
  return device.voice.ready === true || device.voice.registered === true
}

function isDeviceBusy(device: PhoneDevice) {
  return phone.calls.some((item) => item.device_id === device.id)
}

function deviceModeLabel(device?: PhoneDevice) {
  if (device?.phone_mode === 'modem_voice') return '模组直拨'
  if (device?.phone_mode === 'volte') return 'VoLTE'
  return device?.phone_mode === 'cellular' ? '蜂窝数据' : 'WiFi calling'
}

function deviceStatus(device?: PhoneDevice) {
  if (!device) return '未选择设备'
  if (isDeviceBusy(device)) return '通话占用'
  const mode = deviceModeLabel(device)
  if (device.phone_mode === 'modem_voice') {
    if (device.voice.phase === 'restarting') return `${mode} · 重启模组中`
    if (device.voice.last_error) return `${mode} · 准备失败`
    if (device.voice.phase === 'preparing') return `${mode} · 准备音频中`
    return `${mode} · ${device.voice.ready ? '就绪' : '未就绪'}`
  }
  if (device.phone_mode === 'volte' && device.native_volte?.voice_indications_ready === false) {
    return `${mode} · 通话状态未就绪`
  }
  if (isDeviceReady(device) || device.vowifi_active) return `${mode} · 就绪`
  if (device.phone_mode === 'volte' && device.vowifi_enabled) {
    if (device.native_volte?.ims_registered || device.native_volte?.phase === 'registered') return `${mode} · IMS 已注册`
    if (device.native_volte?.phase === 'registering') return `${mode} · IMS 注册中`
    if (device.native_volte?.phase === 'failed') return `${mode} · 失败`
    if (device.native_volte?.reboot_required) return `${mode} · 需重启模组`
    if (device.native_volte?.last_error) return `${mode} · ${device.native_volte.last_error}`
    return `${mode} · 连接中`
  }
  if (device.phone_mode === 'cellular' && device.vowifi_enabled) {
    if (!device.network_enabled && device.data_strategy !== 'always') return `${mode} · 驻网（未开流量）`
    return device.data_strategy === 'always' ? `${mode} · 连接中` : `${mode} · 仅打电话时开`
  }
  if (device.vowifi_enabled) return `${mode} · 连接中`
  return `${mode} · 未开启`
}

const selectedMode = computed(() => {
  const mode = selected.value?.phone_mode
  if (mode === 'cellular' || mode === 'volte' || mode === 'modem_voice') return mode
  return 'wifi'
})
const selectedStrategy = computed(() => selected.value?.data_strategy === 'always' ? 'always' : 'on_demand')
const modePending = ref(false)

function lebaraIdentityBusy(device?: PhoneDevice) {
  const status = device?.lebara_identity_status
  return status === 'recovering' || status === 'waiting_identity'
}

async function retryLebaraIdentity() {
  const iccid = selected.value?.lebara_identity_iccid || selected.value?.iccid
  if (!selectedDevice.value || !iccid || modePending.value) return
  modePending.value = true
  phone.clearError()
  try {
    const result = await devicesService.recoverLebaraIdentity(selectedDevice.value, iccid)
    if (!result.ok) throw new Error(result.error?.message || '恢复英国身份失败')
    await phone.refresh()
    ElMessage.success('已开始恢复英国身份')
  } catch (error) {
    phone.error = phoneErrorMessage(error, '恢复英国身份失败')
  } finally {
    modePending.value = false
  }
}

async function toggleWifiCalling(rawVal: string | number | boolean) {
  const enabled = rawVal === true
  if (!selectedDevice.value || !!call.value || modePending.value) return
  if (enabled === (selected.value?.vowifi_enabled === true)) return
  modePending.value = true
  phone.clearError()
  try {
    const result = enabled
      ? await devicesService.enableVoWiFi(selectedDevice.value, {
          mode: 'wifi',
          data_strategy: selectedStrategy.value
        })
      : await devicesService.disableVoWiFi(selectedDevice.value)
    if (!result.ok) throw new Error(result.error?.message || (enabled ? '打开 WiFi calling 失败' : '关闭 WiFi calling 失败'))
    await phone.refresh()
    ElMessage.success(enabled ? 'WiFi calling 已打开' : 'WiFi calling 已关闭')
  } catch (error) {
    phone.error = phoneErrorMessage(error, enabled ? '打开 WiFi calling 失败' : '关闭 WiFi calling 失败')
  } finally {
    modePending.value = false
  }
}

async function changePhoneMode(mode: string | number | boolean | undefined) {
  if (typeof mode !== 'string') return
  if (!selected.value || !!call.value || modePending.value) return
  const target = { ...selected.value }
  const strategy = selectedStrategy.value
  const disabledReason = phoneModeDisabledReason(target, mode)
  if (disabledReason) {
    ElMessage.warning(disabledReason)
    return
  }
  if (mode === selectedMode.value) {
    if (mode === 'wifi') return
    if (selected.value && (isDeviceReady(selected.value) || selected.value.vowifi_active)) return
  }
  modePending.value = true
  modeRevision++
  phone.clearError()
  try {
    const changed = await confirmPhoneModeChange({
      target, mode, current: () => selected.value, hasCall: () => !!call.value,
      confirm: ({ title, message }) => ElMessageBox.confirm(message, title, {
        confirmButtonText: '确认切换', cancelButtonText: '保持当前模式', type: 'warning',
        distinguishCancelAndClose: true, closeOnClickModal: false
      }),
      apply: async () => {
        const result = await devicesService.enableVoWiFi(target.id, { mode, data_strategy: strategy })
        if (!result.ok) throw new Error(result.error?.message || '切换通话方式失败')
      }
    })
    if (!changed) return
    await phone.refresh()
    if (mode === 'cellular') {
      ElMessage.success('已切到蜂窝。会正常驻网；要走流量再到卡策略打开「网络」')
    } else if (mode === 'modem_voice') {
      ElMessage.success('已选择模组直拨，正在检查设备并准备音频')
    } else if (mode === 'volte') {
      ElMessage.success('已切到 VoLTE。会驻网并由模组原生 IMS 打电话；打开「网络」才会走上网流量')
    } else {
      ElMessage.success('已切换到 WiFi calling')
    }
  } catch (error) {
    phone.error = phoneErrorMessage(error, '切换通话方式失败')
  } finally {
    modePending.value = false
  }
}

async function changeDataStrategy(strategy: string) {
  if (!selectedDevice.value || !!call.value || modePending.value) return
  modePending.value = true
  phone.clearError()
  try {
    const result = await devicesService.enableVoWiFi(selectedDevice.value, {
      mode: 'cellular',
      data_strategy: strategy
    })
    if (!result.ok) throw new Error(result.error?.message || '切换数据策略失败')
    await phone.refresh()
    ElMessage.success(strategy === 'always'
      ? '已改为长时间开启数据。打开「网络」后会一直走流量'
      : '已改为仅打电话时开数据。打开「网络」后，拨号才会连流量')
  } catch (error) {
    phone.error = phoneErrorMessage(error, '切换数据策略失败')
  } finally {
    modePending.value = false
  }
}

function appendDigit(digit: string) {
  if (connected.value && keypadVisible.value) {
    void sendDTMF(digit)
    return
  }
  if (digit === '+') {
    if (!callee.value) callee.value = '+'
    return
  }
  if (callee.value.length < (callee.value.startsWith('+') ? 33 : 32)) callee.value += digit
}

function insertDevicePrefix() {
  if ((!callee.value || callee.value === '+') && dialCountryCode.value) {
    callee.value = `+${dialCountryCode.value}`
  }
}

function eraseDigit() {
  callee.value = callee.value.slice(0, -1)
}

async function runAction(name: string, task: () => Promise<unknown>, success?: string) {
  if (action.value) return
  action.value = name
  phone.clearError()
  try {
    await task()
    if (success) ElMessage.success(success)
  } catch (error) {
    phone.error = phoneErrorMessage(error, `${name}失败`)
  } finally {
    action.value = ''
  }
}

async function runDispatchAction(name: string, task: () => Promise<unknown>) {
  if (action.value) return
  action.value = name
  phone.clearError()
  try {
    await task()
  } catch (error) {
    phone.error = dispatchCallErrorMessage(error)
  } finally {
    action.value = ''
    void loadPools()
  }
}

async function savePeerContact(number?: string) {
  const peer = String(number || '').trim()
  if (!peer) return
  const deviceId = call.value?.device_id || identityDeviceID.value
  const current = identities.identityFor(peer, deviceId)
  try {
    const { value } = await ElMessageBox.prompt('保存后，来电会显示这个名字', '加到联系人', {
      confirmButtonText: '保存',
      cancelButtonText: '取消',
      inputPlaceholder: '联系人名字',
      inputValue: current?.name || '',
      inputValidator: (v) => !!String(v || '').trim() || '请填写名字'
    })
    const ident = await phoneContactsService.save({
      number: peer,
      name: String(value).trim(),
      deviceId,
      contactId: current?.contact_id
    })
    identities.upsertLocal(ident, peer, deviceId)
    ElMessage.success('已保存联系人')
  } catch (error) {
    if (error === 'cancel' || error === 'close') return
    ElMessage.error(error instanceof Error ? error.message : '保存联系人失败')
  }
}

function enableMedia() {
  return runAction('启用听筒', () => phone.enableMedia(), '听筒和麦克风已启用')
}

function enableListenOnlyMedia() {
  return runAction('恢复仅听', () => phone.enableListenOnlyMedia(), '仅听模式已恢复')
}

function startListenOnlyCall() {
  if (!canPlaceCall.value) return
  if (!usingDiagnosticDial.value) {
    return runDispatchAction('仅听呼叫', () => phone.startPoolListenOnlyCall(selectedPoolID.value, callee.value))
  }
  if (selected.value?.phone_mode === 'cellular') {
    return runAction('仅听呼叫', async () => {
      const confirmed = await ElMessageBox.confirm(
        selected.value?.network_enabled
          ? '当前用蜂窝数据打电话，会消耗少量流量。是否继续？'
          : '当前未开流量。打这个电话会临时打开数据，挂断后关闭。是否继续？',
        '数据流量通话提醒',
        { confirmButtonText: '继续拨号', cancelButtonText: '取消', type: 'warning' }
      ).then(() => true).catch(() => false)
      if (!confirmed) return
      return phone.startListenOnlyCall(selectedDevice.value, callee.value)
    })
  }
  return runAction('仅听呼叫', () => phone.startListenOnlyCall(selectedDevice.value, callee.value))
}

function startTwoWayCall() {
  if (!canPlaceCall.value || !phone.secureContext) return
  if (!usingDiagnosticDial.value) {
    return runDispatchAction('双向呼叫', () => phone.startPoolCall(selectedPoolID.value, callee.value))
  }
  if (selected.value?.phone_mode === 'cellular') {
    return runAction('双向呼叫', async () => {
      const confirmed = await ElMessageBox.confirm(
        selected.value?.network_enabled
          ? '当前用蜂窝数据打电话，会消耗少量流量。是否继续？'
          : '当前未开流量。打这个电话会临时打开数据，挂断后关闭。是否继续？',
        '数据流量通话提醒',
        { confirmButtonText: '继续拨号', cancelButtonText: '取消', type: 'warning' }
      ).then(() => true).catch(() => false)
      if (!confirmed) return
      return phone.startCall(selectedDevice.value, callee.value)
    })
  }
  return runAction('双向呼叫', () => phone.startCall(selectedDevice.value, callee.value))
}

function redialRecord(record: PhoneRecord) {
  const peer = String(record.peer || '').trim()
  if (!peer || action.value || call.value) return
  callee.value = peer
  if (!phone.secureContext) {
    phone.error = '双向回拨需要通过受信任的 HTTPS 地址访问，并允许浏览器使用麦克风'
    return
  }
  if (!usingDiagnosticDial.value) {
    if (!dispatchState.value.canDial) {
      phone.error = dispatchState.value.detail
      return
    }
    void runDispatchAction('回拨', () => phone.startPoolCall(selectedPoolID.value, peer))
    return
  }
  const device = phone.devices.find((item) => item.id === record.device_id)
  if (!device) {
    phone.error = '历史通话设备当前不可用，请返回号码池调度后重试'
    return
  }
  if (isDeviceBusy(device)) {
    phone.error = '历史通话设备正在被其他通话占用'
    return
  }
  if (!isDeviceReady(device) && device.phone_mode !== 'cellular' && device.phone_mode !== 'volte') {
    phone.error = '历史通话设备尚未就绪'
    return
  }
  rememberDevice(device.id)
  void runAction('回拨', () => phone.startCall(device.id, peer))
}

function answerCall(current: PhoneCall) {
  return runAction('麦克风接听', () => phone.answer(current))
}

function answerListenOnly(current: PhoneCall) {
  return runAction('仅听接听', () => phone.answerListenOnly(current))
}

function rejectCall(current: PhoneCall) {
  return runAction('拒接', () => phone.reject(current))
}

function hangup(current: PhoneCall) {
  return runAction('挂断', () => phone.hangup(current), '已发送挂断请求')
}

function toggleHold() {
  return runAction(call.value?.held ? '恢复通话' : '保持', () => phone.toggleHold())
}

function takeOver(current: PhoneCall, mode: 'listen-only' | 'two-way') {
  const label = mode === 'listen-only' ? '仅听接管' : '双向接管'
  return runAction(label, () => phone.takeOver(current, mode), `已${label}这通电话`)
}

async function sendDTMF(digit: string) {
  if (!connected.value) return
  try {
    await phone.sendDTMF(digit)
    lastDTMF.value = digit
  } catch (error) {
    phone.error = phoneErrorMessage(error, 'DTMF 发送失败')
  }
}
</script>

<template>
  <div class="app-page phone-page">
    <PageHeader class="phone-page-header" :title="t('phone.title')" :subtitle="t('phone.subtitle')">
      <template #actions>
        <PhoneMcpConnection />
        <span class="media-state" :class="`is-${phone.mediaState}`">
          <span aria-hidden="true" />{{ phone.mediaReady
            ? phone.mediaMode === 'listen-only' ? '仅听已连接' : '双向语音已连接'
            : '听筒未连接' }}
        </span>
      </template>
    </PageHeader>

    <section v-if="!phone.secureContext" class="security-notice" role="alert">
      <div class="notice-icon"><el-icon><LockClosed24Regular /></el-icon></div>
      <div>
        <strong>麦克风需要受信任的 HTTPS</strong>
        <p>当前 HTTP 页面可“仅听接听”或“仅听呼叫”，不会申请麦克风，因此对方听不到你。请通过已配置的 Nginx、Caddy 或其他受信任 HTTPS 地址访问后再使用双向通话。</p>
      </div>
    </section>

    <div v-if="phone.error || phone.eventError" class="phone-error" role="alert">
      <span>{{ phone.error || phone.eventError }}</span>
      <button type="button" aria-label="关闭错误提示" @click="phone.clearError(); phone.eventError = ''">×</button>
    </div>

    <section class="phone-workspace ui-card ui-workspace-glow">
      <div class="phone-grid">
      <section class="phone-console" aria-labelledby="phone-console-title">
        <header class="console-header">
          <div class="console-heading">
            <span>WEBRTC HANDSET</span>
            <h2 id="phone-console-title">{{ call ? '当前电话' : '拨号' }}</h2>
            <p>{{ call
              ? phoneCallStatusLabel(call, callEnding)
              : usingDiagnosticDial ? '管理员设备直拨诊断' : '选择号码池并输入号码' }}</p>
          </div>
          <div v-if="!usingDiagnosticDial" class="pool-selector">
            <div class="pool-selector-heading">
              <label for="phone-pool">号码池</label>
              <button
                v-if="auth.isAdmin"
                type="button"
                class="diagnostic-entry"
                @click="setDiagnosticDirectDial(true)"
              >管理员诊断：设备直拨</button>
            </div>
            <el-select
              id="phone-pool"
              v-model="selectedPoolID"
              aria-label="号码池"
              placeholder="选择号码池"
              :loading="poolsLoading"
              :disabled="!!call || !!action || poolsLoading || numberPools.length === 0"
              popper-class="phone-pool-dropdown"
              @change="rememberPool"
            >
              <el-option v-if="!numberPools.length" label="当前账号暂无授权号码池" value="" />
              <el-option
                v-for="pool in numberPools"
                :key="pool.id"
                :label="`${pool.name} · ${poolAvailableCount(pool)} 条可用`"
                :value="pool.id"
                :disabled="!poolIsEnabled(pool)"
              />
            </el-select>
            <div class="pool-summary" :class="`is-${dispatchState.tone}`" role="status" aria-live="polite">
              <span class="pool-state-dot" aria-hidden="true" />
              <div class="pool-summary-copy">
                <strong>{{ dispatchState.title }}</strong>
                <small>{{ dispatchState.detail }}</small>
              </div>
              <div v-if="selectedPool" class="pool-capacity">
                <strong>{{ poolAvailableCount(selectedPool) }}</strong>
                <span>可用 / {{ poolMemberCount(selectedPool) }} 总线路</span>
              </div>
              <button v-if="poolsError" type="button" class="pool-retry" @click="loadPools">重试</button>
            </div>
          </div>
          <div v-else-if="auth.isAdmin" class="device-selector is-diagnostic">
            <div class="diagnostic-banner" role="note">
              <div>
                <strong>管理员设备直拨</strong>
                <p>指定当前线路，仍受设备占用、卡停用和调度锁限制，仅用于管理员线路诊断。</p>
              </div>
              <button type="button" @click="setDiagnosticDirectDial(false)">返回号码池调度</button>
            </div>
            <div class="device-selector-heading">
              <label for="phone-device">语音设备（管理员诊断）</label>
              <button
                v-if="selectedDevice"
                type="button"
                class="phone-setup-toggle"
                :aria-expanded="mobileSetupOpen"
                aria-controls="phone-mode-settings"
                @click="mobileSetupOpen = !mobileSetupOpen"
              >
                <span>{{ deviceModeLabel(selected) }}</span>
                {{ mobileSetupOpen ? '收起设置' : '通话设置' }}
              </button>
            </div>
            <el-select
              id="phone-device"
              v-model="selectedDevice"
              aria-label="语音设备"
              placeholder="选择语音设备"
              :disabled="!!call || modePending"
              popper-class="phone-device-dropdown"
              @change="rememberDevice"
            >
              <el-option v-if="!phone.devices.length" label="无可用设备" value="" />
              <el-option
                v-for="device in phone.devices"
                :key="device.id"
                :label="`${device.name || device.id} · ${deviceStatus(device)}`"
                :value="device.id"
                :disabled="isDeviceBusy(device)"
              />
            </el-select>
            <div
              v-if="selectedDevice"
              id="phone-mode-settings"
              class="phone-mode-bar"
              :class="{ 'is-mobile-open': mobileSetupOpen }"
            >
              <el-radio-group
                :model-value="selectedMode"
                size="small"
                :disabled="!!call || modePending"
                @change="changePhoneMode"
              >
                <el-radio-button value="wifi" :disabled="!!phoneModeDisabledReason(selected, 'wifi')" :title="phoneModeDisabledReason(selected, 'wifi')">WiFi calling</el-radio-button>
                <el-radio-button value="cellular" :disabled="!!phoneModeDisabledReason(selected, 'cellular')" :title="phoneModeDisabledReason(selected, 'cellular')">蜂窝数据</el-radio-button>
                <el-radio-button value="volte" :disabled="!!phoneModeDisabledReason(selected, 'volte')" :title="phoneModeDisabledReason(selected, 'volte')">VoLTE</el-radio-button>
                <el-radio-button value="modem_voice" :disabled="!!phoneModeDisabledReason(selected, 'modem_voice')" :title="phoneModeDisabledReason(selected, 'modem_voice')">模组直拨</el-radio-button>
              </el-radio-group>
              <el-select
                v-if="selectedMode === 'cellular'"
                :model-value="selectedStrategy"
                size="small"
                style="width: 160px"
                :disabled="!!call || modePending"
                @change="(val: string | number | boolean | undefined) => { if (typeof val === 'string') void changeDataStrategy(val) }"
              >
                <el-option label="仅打电话时开" value="on_demand" />
                <el-option label="长时间开启" value="always" />
              </el-select>
              <p v-if="selectedMode === 'cellular'" class="phone-mode-hint">
                {{ selected?.network_enabled
                  ? (selectedStrategy === 'always' ? '网络已开，数据会保持连接。' : '网络已开，只有拨号时才连数据，挂断后关闭。')
                  : '会正常驻网，待机不走流量。打蜂窝电话会临时打开数据。' }}
              </p>
              <div v-if="selectedMode === 'modem_voice'" class="phone-mode-hint" role="status" aria-live="polite">
                <p>使用 SIM 卡拨打和接听，声音通过网页传输。接通后可用键盘按键。</p>
                <p v-if="selected?.voice.last_error" class="phone-mode-hint is-warn" role="alert">{{ selected.voice.last_error }}</p>
                <el-button v-if="selected?.voice.phase === 'failed'" :loading="modePending" :disabled="!!call" @click="void changePhoneMode('modem_voice')">重新准备</el-button>
              </div>
              <p v-if="selectedMode === 'volte'" class="phone-mode-hint">
                {{ selected?.software_ims_blocked
                  ? '这张卡需要使用模组驻网通话，可选择 VoLTE 或已适配的模组直拨。打开「网络」才会用上网流量。'
                  : selected?.native_volte?.uac_unusable
                    ? '这台模组的 USB 声卡不能开（会把 QMI 打挂）。VoLTE 信令能打，没有模组声音。'
                    : selected?.native_volte?.reboot_required
                      ? '原生 IMS 已写入。UAC 声卡要重启模组后才出现，现在可以试信令，音频可能不可用。'
                      : '会驻网并用模组原生 IMS 打电话，不走软件 WiFi calling。打开「网络」才会用上网流量。' }}
              </p>
              <div
                v-show="selectedMode === 'wifi'"
                class="phone-wifi-calling-switch"
                :class="{ 'is-on': selected?.vowifi_enabled === true }"
              >
                <span>
                  启动
                  <small>{{
                    selected?.vowifi_enabled
                      ? (selected.vowifi_active || isDeviceReady(selected) ? '已启动，正在走 WiFi calling' : '已打开，正在注册')
                      : '打开后开始注册。关掉只停服务，仍是 WiFi calling'
                  }}</small>
                </span>
                <el-switch
                  :model-value="selected?.vowifi_enabled === true"
                  :disabled="!!call || modePending || lebaraIdentityBusy(selected)"
                  :loading="modePending || lebaraIdentityBusy(selected)"
                  aria-label="启动 WiFi calling"
                  @change="toggleWifiCalling"
                />
              </div>
              <p v-if="lebaraIdentityBusy(selected)" class="phone-mode-hint">
                {{ selected?.lebara_identity_message || '正在恢复英国身份，完成后会自动打开 WiFi calling' }}
              </p>
              <p v-else-if="selected?.lebara_identity_status === 'failed'" class="phone-mode-hint is-warn">
                {{ selected.lebara_identity_message || '自动恢复英国身份失败，当前仍是荷兰 20404。不要开 WiFi calling。' }}
                <button type="button" class="phone-lebara-retry" :disabled="modePending" @click="retryLebaraIdentity">再试一次</button>
              </p>
              <p v-else-if="selected?.rf_lock" class="phone-mode-hint is-warn">
                这张分享卡不能驻国内网，否则 IMSI 会切到 20404，WiFi calling 会废
              </p>
            </div>
          </div>
        </header>

        <div v-if="phone.loading" class="console-loading">正在加载电话状态…</div>

        <div v-else-if="call" class="active-call">
          <div class="call-identity">
            <span>{{ call.direction === 'inbound' ? 'INCOMING' : 'OUTGOING' }}</span>
            <strong :class="{ 'is-named': !!identities.identityFor(call.peer, call.device_id)?.name }">{{ identities.titleFor(call.peer, call.device_id) }}</strong>
            <p v-if="identities.identityFor(call.peer, call.device_id)?.name" class="call-number">{{ identities.identityFor(call.peer, call.device_id)?.display_number }}</p>
            <p v-if="identities.subtitleFor(call.peer, call.device_id)" class="call-attribution">{{ identities.subtitleFor(call.peer, call.device_id) }}</p>
            <p>{{ phoneCallStatusLabel(call, callEnding) }} · {{ formatCallDuration(call, phone.now) }}</p>
            <p v-if="waitingCall" class="call-waiting-hint">第二路来电 {{ identities.titleFor(waitingCall.peer, waitingCall.device_id) }}（呼叫等待）</p>
            <button type="button" class="contact-save" :disabled="!call.peer" @click="savePeerContact(call.peer)">
              <el-icon><PersonAdd24Regular /></el-icon>{{ identities.identityFor(call.peer, call.device_id)?.name ? '改联系人' : '加到联系人' }}
            </button>
          </div>

          <dl class="call-meta">
            <div>
              <dt>{{ auth.isAdmin ? '设备' : '线路' }}</dt>
              <dd>{{ auth.isAdmin ? call.device_id : (call.pool_name || selectedPool?.name || '号码池调度') }}</dd>
            </div>
            <div><dt>Codec</dt><dd>{{ call.codec || '协商中' }}</dd></div>
            <div>
              <dt>媒体</dt>
              <dd>{{ phone.mediaReady
                ? phone.mediaMode === 'listen-only' ? '仅听' : '双向'
                : call.read_only ? '其他浏览器控制' : '等待恢复' }}</dd>
            </div>
          </dl>

          <div v-if="incoming" class="incoming-actions">
            <button type="button" class="round-action is-reject" :disabled="!!action" @click="rejectCall(call)">
              <el-icon><CallDismiss24Regular /></el-icon><span>拒接</span>
            </button>
            <button
              type="button"
              class="round-action is-listen"
              :disabled="!!action"
              title="只接收对方声音，不申请麦克风"
              @click="answerListenOnly(call)"
            >
              <el-icon><Speaker224Regular /></el-icon><span>仅听接听</span><small>对方听不到你</small>
            </button>
            <button
              type="button"
              class="round-action is-answer"
              :disabled="!!action || !phone.secureContext"
              title="需要受信任的 HTTPS 和麦克风权限"
              @click="answerCall(call)"
            >
              <el-icon><Call24Regular /></el-icon><span>麦克风接听</span>
            </button>
          </div>

          <div v-else-if="call.read_only" class="takeover-panel">
            <strong>接管这通电话</strong>
            <p>接管后可在本页控制通话，原浏览器的媒体连接会断开。仅听不申请麦克风权限；双向语音需要受信任的 HTTPS。</p>
            <div class="restore-actions">
              <button type="button" class="restore-button" :disabled="!!action || callEnding" @click="takeOver(call, 'listen-only')">
                <el-icon><Speaker224Regular /></el-icon>仅听接管
              </button>
              <button type="button" class="restore-button" :disabled="!!action || callEnding || !phone.secureContext" @click="takeOver(call, 'two-way')">
                <el-icon><Mic24Regular /></el-icon>双向接管
              </button>
            </div>
          </div>

          <template v-else>
            <div v-if="!phone.mediaReady" class="restore-actions">
              <button
                type="button"
                class="restore-button"
                :disabled="!!action || callEnding"
                @click="enableListenOnlyMedia"
              >
                <el-icon><Speaker224Regular /></el-icon>在 15 秒内恢复仅听
              </button>
              <button
                type="button"
                class="restore-button"
                :disabled="!!action || callEnding || !phone.secureContext"
                @click="enableMedia"
              >
                <el-icon><Mic24Regular /></el-icon>恢复双向语音
              </button>
            </div>

            <div v-else-if="phone.mediaMode === 'listen-only'" class="listen-only-panel" role="status">
              <div>
                <strong>仅听模式</strong>
                <p>你能听到对方，对方听不到你；浏览器没有申请麦克风。</p>
              </div>
              <button
                type="button"
                class="secondary-button"
                :disabled="!!action || callEnding || !phone.secureContext"
                @click="enableMedia"
              >
                <el-icon><Mic24Regular /></el-icon>{{ phone.secureContext ? '启用麦克风' : 'HTTPS 下可启用麦克风' }}
              </button>
            </div>

            <div v-if="connected && keypadVisible" class="active-keypad">
              <p aria-live="polite">发送 DTMF{{ lastDTMF ? `：${lastDTMF}` : '' }}</p>
              <PhoneDialPad :disabled="!!action || !phone.canSendDTMF" @digit="appendDigit" />
            </div>

            <div class="call-controls" aria-label="通话控制">
              <button
                type="button"
                class="control-button"
                :disabled="callEnding || !phone.mediaReady || phone.mediaMode !== 'two-way'"
                :aria-pressed="phone.muted"
                @click="phone.toggleMute"
              >
                <el-icon>
                  <Speaker224Regular v-if="phone.mediaMode === 'listen-only'" />
                  <MicOff24Regular v-else-if="phone.muted" />
                  <Mic24Regular v-else />
                </el-icon>
                <span>{{ phone.mediaMode === 'listen-only' ? '仅听模式' : phone.muted ? '取消静音' : '静音' }}</span>
              </button>
              <button
                type="button"
                class="control-button"
                :disabled="!!action || callEnding || !connected || call.read_only || modemCall"
                :aria-pressed="!!call.held"
                @click="toggleHold"
              >
                <el-icon>
                  <Play24Regular v-if="call.held" />
                  <Pause24Regular v-else />
                </el-icon>
                <span>{{ call.held ? '恢复' : '保持' }}</span>
              </button>
              <button
                type="button"
                class="control-button"
                :disabled="!phone.canSendDTMF"
                :aria-pressed="keypadVisible"
                @click="keypadVisible = !keypadVisible"
              >
                <el-icon><Dialpad24Regular /></el-icon><span>键盘</span>
              </button>
              <button type="button" class="control-button is-hangup" :disabled="!!action || callEnding" @click="hangup(call)">
                <el-icon><CallEnd24Regular /></el-icon><span>{{ action === '挂断' || callEnding ? '挂断中…' : '挂断' }}</span>
              </button>
            </div>
          </template>
        </div>

        <div v-else class="dialer">
          <div v-if="usingDiagnosticDial" class="selected-device-status" :class="{ 'is-ready': selected && isDeviceReady(selected) }">
            <span aria-hidden="true" />{{ deviceStatus(selected) }}
          </div>
          <div v-else class="pool-dispatch-state" :class="`is-${dispatchState.tone}`" role="status" aria-live="polite">
            <span aria-hidden="true" />
            <div>
              <strong>{{ dispatchState.title }}</strong>
              <small>{{ dispatchState.detail }}</small>
            </div>
          </div>

          <div class="number-field">
            <div class="number-field-heading">
              <label for="callee">电话号码</label>
              <button
                v-if="dialCountryCode"
                type="button"
                :disabled="!!callee && callee !== '+'"
                :aria-label="`填入当前线路国际区号 +${dialCountryCode}`"
                @click="insertDevicePrefix"
              >线路区号 +{{ dialCountryCode }}</button>
            </div>
            <div class="number-input">
              <input
                id="callee"
                v-model.trim="callee"
                type="tel"
                inputmode="tel"
                maxlength="33"
                autocomplete="tel"
                placeholder="输入号码"
                :aria-invalid="!!calleeError"
                :aria-describedby="calleeError ? 'callee-error' : undefined"
              />
              <button type="button" aria-label="删除末位号码" :disabled="!callee" @click="eraseDigit">
                <el-icon><Backspace24Regular /></el-icon>
              </button>
            </div>
            <small v-if="calleeError" id="callee-error" role="alert">{{ calleeError }}</small>
            <small v-else class="callee-hint" :aria-hidden="calleeAttribution ? undefined : 'true'">{{ calleeAttribution || '\u00a0' }}</small>
          </div>

          <PhoneDialPad allow-plus :plus-disabled="!!callee" @digit="appendDigit" />

          <div class="call-mode-actions" aria-label="呼叫模式">
            <button
              type="button"
              class="dial-button is-listen"
              :disabled="!canPlaceCall || !!action"
              title="只接收对方声音，不申请麦克风"
              @click="startListenOnlyCall"
            >
              <el-icon><Speaker224Regular /></el-icon>{{ action === '仅听呼叫' ? '正在呼叫…' : '仅听呼叫' }}
            </button>
            <button
              type="button"
              class="dial-button"
              :disabled="!canPlaceCall || !!action || !phone.secureContext"
              title="需要受信任的 HTTPS 和麦克风权限"
              @click="startTwoWayCall"
            >
              <el-icon><Call24Regular /></el-icon>{{ action === '双向呼叫' ? '正在呼叫…' : '双向呼叫' }}
            </button>
          </div>
          <p class="call-mode-help">
            {{ phone.secureContext
              ? '仅听呼叫不会使用麦克风；双向呼叫会请求麦克风权限。'
              : '当前页面只能仅听呼叫，对方听不到你。' }}
          </p>
        </div>
      </section>

        <div class="phone-side">
          <PhoneContactsPanel :device-id="identityDeviceID" @dial="callee = $event" />
          <PhoneCallHistory
            :records="phone.history"
            :show-device-id="usingDiagnosticDial"
            :redial-disabled="!!action || !!call || (!usingDiagnosticDial && !dispatchState.canDial) || !phone.secureContext"
            @redial="redialRecord"
          />
        </div>
      </div>
    </section>
  </div>
</template>

<style scoped src="../styles/phone.css"></style>
