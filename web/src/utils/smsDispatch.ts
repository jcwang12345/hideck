import type { AppError } from '../types/domain'
import type { NumberPoolResource } from '../types/admin-resources'

const smsDispatchMessages: Record<string, string> = {
  sms_pool_required: '请选择当前账号已授权的号码池',
  sms_pool_forbidden: '当前账号无权使用这个号码池，请刷新授权或联系管理员',
  sms_no_resource: '号码池中暂无可发送短信的线路，请稍后重试',
  sms_profile_inactive: '所选线路的 Profile 未激活；短信发送不会自动切换 Profile',
  sms_profile_switching: '设备正在切换 Profile，请等待切换完成后重试',
  sms_device_busy: '所选线路正在通话或被占用，请稍后重试',
  sms_device_offline: '所选线路设备离线，暂时无法发送短信',
  sms_dispatch_unavailable: '短信调度服务暂不可用，请稍后重试',
  sms_device_forbidden: '普通用户只能通过已授权号码池发送短信',
  sms_selector_conflict: '发送方式冲突，请重新选择号码池或管理员诊断设备'
}

function errorCode(error: unknown) {
  const direct = (error as AppError | undefined)?.code
  if (typeof direct === 'string' && direct.trim()) return direct.trim()
  const responseCode = (error as { response?: { data?: { code?: unknown } } })?.response?.data?.code
  return typeof responseCode === 'string' ? responseCode.trim() : ''
}

export function smsDispatchErrorMessage(error: unknown, fallback = '短信发送失败') {
  const mapped = smsDispatchMessages[errorCode(error)]
  if (mapped) return mapped
  const direct = (error as AppError | undefined)?.message
  if (typeof direct === 'string' && direct.trim()) return direct.trim()
  const responseMessage = (error as { response?: { data?: { message?: unknown } } })?.response?.data?.message
  return typeof responseMessage === 'string' && responseMessage.trim() ? responseMessage.trim() : fallback
}

export function smsPoolEnabled(pool: NumberPoolResource | undefined) {
  if (!pool || pool.enabled === false) return false
  return !['disabled', 'inactive', 'deleted'].includes(String(pool.status || '').trim().toLowerCase())
}

export function chooseSmsPoolID(pools: NumberPoolResource[], current = '') {
  const enabled = pools.filter(smsPoolEnabled)
  if (enabled.some(pool => pool.id === current)) return current
  return enabled.find(pool => Number(pool.available_count || 0) > 0)?.id || enabled[0]?.id || ''
}

export function smsPoolAvailableCount(pool: NumberPoolResource | undefined) {
  const count = Number(pool?.available_count || 0)
  return Number.isFinite(count) ? Math.max(0, Math.floor(count)) : 0
}
