import type {
  CardResource,
  NumberPoolMemberResource,
  NumberPoolUserResource
} from '../types/admin-resources'

export const E164_PATTERN = /^\+[1-9][0-9]{1,14}$/
export const DEFAULT_REJECTION_THRESHOLD = 3
export const DEFAULT_DISABLE_SCOPE = 'card'
export const DEFAULT_POLICY_TIMEZONE = 'Asia/Shanghai'
export const DEFAULT_RESTORE_TIME = '00:00'

export function e164Error(value: string): string {
  const normalized = String(value || '').trim()
  if (!normalized) return '请填写 E.164 号码'
  return E164_PATTERN.test(normalized) ? '' : '号码必须使用 E.164 格式，例如 +14155550100'
}

export function usernameError(value: string): string {
  const normalized = String(value || '').trim()
  if (!normalized) return '请填写用户名'
  const hasControlCharacter = [...normalized].some(character => {
    const codePoint = character.codePointAt(0) || 0
    return codePoint <= 0x1f || codePoint === 0x7f
  })
  if ([...normalized].length > 64 || hasControlCharacter) return '用户名不能超过 64 个字符或包含控制字符'
  return ''
}

export function passwordError(password: string, username = ''): string {
  const value = String(password || '')
  if (value.length < 8) return '密码至少 8 位'
  if (value.trim().toLowerCase() === String(username || '').trim().toLowerCase()) return '密码不能与用户名相同'
  if (value.length < 12) {
    const classes = [/[a-z]/.test(value), /[A-Z]/.test(value), /[0-9]/.test(value), /[^A-Za-z0-9]/.test(value)].filter(Boolean).length
    if (classes < 2) return '少于 12 位的密码需包含至少两类字符'
  }
  return ''
}

export function poolNameError(value: string): string {
  const normalized = String(value || '').trim()
  if (!normalized) return '请填写号码池名称'
  return [...normalized].length > 128 ? '号码池名称不能超过 128 个字符' : ''
}

export function poolDescriptionError(value: string): string {
  return [...String(value || '').trim()].length > 512 ? '号码池说明不能超过 512 个字符' : ''
}

export function validatePoolMembers(members: Array<Pick<NumberPoolMemberResource, 'phone_number_id' | 'priority' | 'weight'>>): string {
  const ids = members.map(item => String(item.phone_number_id || '').trim())
  if (ids.some(id => !id)) return '号码池成员必须关联号码资源'
  if (new Set(ids).size !== ids.length) return '同一个号码不能重复加入号码池'
  if (members.some(item => !Number.isInteger(Number(item.priority)) || Number(item.priority) < 0)) return '成员优先级必须是大于等于 0 的整数'
  if (members.some(item => !Number.isInteger(Number(item.weight)) || Number(item.weight) < 1)) return '成员权重必须是大于等于 1 的整数'
  return ''
}

export function validatePoolUsers(users: Array<Pick<NumberPoolUserResource, 'user_id'>>): string {
  const ids = users.map(item => String(item.user_id || '').trim())
  if (ids.some(id => !id)) return '授权项必须关联用户'
  return new Set(ids).size === ids.length ? '' : '同一个用户不能重复授权'
}

export function dispatchPolicyError(input: { name: string; number_pool_id: string; rejection_threshold: number }): string {
  if (!String(input.name || '').trim()) return '请填写策略名称'
  if (!String(input.number_pool_id || '').trim()) return '请选择适用号码池'
  const threshold = Number(input.rejection_threshold)
  if (!Number.isInteger(threshold) || threshold < 1) return '拒接阈值必须是大于等于 1 的整数'
  return ''
}

export function cardTypePresentation(card: Pick<CardResource, 'card_type' | 'kind' | 'capabilities' | 'eid'>): { label: string; detail: string } {
  const type = String(card.card_type || card.kind || card.capabilities?.type || 'unknown').toLowerCase()
  if (type === 'euicc') {
    const evidence = [(card.eid || card.capabilities?.has_eid) ? 'EID' : '', card.capabilities?.has_isdr ? 'ISD-R' : ''].filter(Boolean).join(' / ')
    return { label: 'eUICC', detail: evidence ? `已由 ${evidence} 证据确认` : '已确认支持 eSIM Profile' }
  }
  if (type === 'physical_uicc') return { label: '实体 UICC', detail: '已获得明确物理 UICC 证据' }
  return { label: '卡型待确认', detail: '尚无 EID/ISD-R/明确物理 UICC 证据' }
}

export function cardIsTransitioning(card: Pick<CardResource, 'status'>): boolean {
  return ['switching', 'registering'].includes(String(card.status || '').trim().toLowerCase())
}
