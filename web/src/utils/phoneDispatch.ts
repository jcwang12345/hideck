import type { PhoneNumberPool } from '../services/phone'
import { phoneErrorMessage } from './phone'

export type PhonePoolState = {
  code: 'loading' | 'load_failed' | 'no_pool' | 'user_busy' | 'preparing' | 'pool_disabled' | 'no_resource' | 'ready'
  tone: 'muted' | 'info' | 'warning' | 'danger' | 'success'
  title: string
  detail: string
  canDial: boolean
}

export function poolAvailableCount(pool: PhoneNumberPool | undefined): number {
  const value = Number(pool?.available_count ?? 0)
  return Number.isFinite(value) ? Math.max(0, Math.floor(value)) : 0
}

export function poolMemberCount(pool: PhoneNumberPool | undefined): number {
  const value = Number(pool?.member_count ?? pool?.members?.length ?? 0)
  return Number.isFinite(value) ? Math.max(0, Math.floor(value)) : 0
}

export function poolIsEnabled(pool: PhoneNumberPool | undefined): boolean {
  if (!pool || pool.enabled === false) return false
  return !['disabled', 'inactive', 'deleted'].includes(String(pool.status || '').trim().toLowerCase())
}

export function choosePhonePoolID(pools: PhoneNumberPool[], current = '', preferred = ''): string {
  const available = pools.filter(poolIsEnabled)
  if (available.some(pool => pool.id === current)) return current
  if (available.some(pool => pool.id === preferred)) return preferred
  return available.find(pool => poolAvailableCount(pool) > 0)?.id || available[0]?.id || ''
}

export function phonePoolState(input: {
  loading: boolean
  error: string
  pools: PhoneNumberPool[]
  selected?: PhoneNumberPool
  hasActiveCall: boolean
  preparing: boolean
}): PhonePoolState {
  if (input.loading) {
    return { code: 'loading', tone: 'info', title: '正在加载号码池', detail: '正在读取当前账号可用的通话资源', canDial: false }
  }
  if (input.error) {
    return { code: 'load_failed', tone: 'danger', title: '号码池加载失败', detail: input.error, canDial: false }
  }
  if (!input.pools.length) {
    return { code: 'no_pool', tone: 'warning', title: '暂无授权号码池', detail: '请联系管理员为当前账号授权号码池', canDial: false }
  }
  if (!input.selected) {
    return { code: 'pool_disabled', tone: 'danger', title: '可用号码池均已停用', detail: '请联系管理员启用号码池或调整授权', canDial: false }
  }
  if (input.hasActiveCall) {
    return { code: 'user_busy', tone: 'warning', title: '你已有一通正在进行的电话', detail: '请先结束当前通话，再发起新的拨号', canDial: false }
  }
  if (input.preparing) {
    return { code: 'preparing', tone: 'info', title: '正在分配并准备线路', detail: '系统正在选择号码、准备 Profile 并检查 IMS', canDial: false }
  }
  if (!poolIsEnabled(input.selected)) {
    return { code: 'pool_disabled', tone: 'danger', title: '号码池已停用', detail: '请选择其他号码池或联系管理员', canDial: false }
  }
  const available = poolAvailableCount(input.selected)
  if (available === 0) {
    return { code: 'no_resource', tone: 'warning', title: '当前号码池暂无可用线路', detail: '线路可能正在通话、切换 Profile 或注册 IMS，请稍后重试', canDial: false }
  }
  return {
    code: 'ready',
    tone: 'success',
    title: `${available} 条线路可用`,
    detail: `将由「${input.selected.name}」自动选择号码`,
    canDial: true
  }
}

const dispatchErrorMessages: Record<string, string> = {
  no_resource: '当前号码池暂无可用线路，请稍后重试或选择其他号码池',
  user_busy: '你已有一通正在进行的电话，请先结束后再拨号',
  profile_switch_failed: '线路已分配，但 Profile 切换失败，请稍后重试或联系管理员检查卡状态',
  ims_not_ready: '线路已分配，但 IMS 未就绪，请等待 WiFi Calling 或 VoLTE 注册完成后重试',
  dial_failed: '线路已准备，但呼叫建立失败',
  dispatch_unavailable: '号码池调度服务暂不可用，请稍后重试',
  forbidden_pool: '当前账号无权使用这个号码池，请重新选择或联系管理员'
}

export function dispatchCallErrorMessage(error: unknown): string {
  const data = (error as { response?: { data?: { code?: unknown; message?: unknown } } })?.response?.data
  const code = typeof data?.code === 'string' ? data.code.trim() : ''
  const mapped = dispatchErrorMessages[code]
  if (!mapped) return phoneErrorMessage(error, '发起调度呼叫失败')
  const detail = typeof data?.message === 'string' ? data.message.trim() : ''
  return code === 'dial_failed' && detail && detail !== mapped ? `${mapped}：${detail}` : mapped
}
