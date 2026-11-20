import type { ResourceRow } from '../components/resources/ResourceCollectionView.vue'
import { t } from '../i18n'

const statusTranslationKeys: Record<string, string> = {
  active: 'resources.status.active',
  available: 'resources.status.available',
  busy: 'resources.status.busy',
  calling: 'resources.status.calling',
  completed: 'resources.status.completed',
  connected: 'resources.status.connected',
  degraded: 'resources.status.degraded',
  denied: 'resources.status.denied',
  disabled: 'resources.status.disabled',
  enabled: 'resources.status.enabled',
  error: 'resources.status.failed',
  failed: 'resources.status.failed',
  fault: 'resources.status.failed',
  offline: 'resources.status.offline',
  ok: 'resources.status.ready',
  online: 'resources.status.online',
  pending: 'resources.status.pending',
  ready: 'resources.status.ready',
  recorded: 'resources.status.recorded',
  recovering: 'resources.status.recovering',
  registering: 'resources.status.registering',
  rejected: 'resources.status.rejected',
  reserved: 'resources.status.reserved',
  revoked: 'resources.status.revoked',
  ringing: 'resources.status.ringing',
  running: 'resources.status.running',
  success: 'resources.status.completed',
  switching: 'resources.status.switching',
  warning: 'resources.status.warning'
}

export function displayValue(value: unknown, fallback = '—'): string {
  if (value === null || value === undefined || value === '') return fallback
  if (typeof value === 'boolean') return value ? '是' : '否'
  return String(value)
}

export function displayStatus(value: unknown, fallback: string): string {
  const raw = displayValue(value, '')
  if (!raw) return fallback
  const key = statusTranslationKeys[raw.trim().toLowerCase()]
  return key ? t(key) : raw
}

export function displayDate(value: unknown): string {
  const raw = displayValue(value, '')
  if (!raw) return '—'
  const date = new Date(raw)
  return Number.isNaN(date.getTime()) ? raw : date.toLocaleString()
}

export function displayDuration(seconds: unknown): string {
  const total = Math.max(0, Number(seconds || 0))
  if (!Number.isFinite(total)) return '—'
  const minutes = Math.floor(total / 60)
  const rest = Math.floor(total % 60)
  return `${minutes}:${String(rest).padStart(2, '0')}`
}

export function maskIdentifier(value: unknown): string {
  const raw = displayValue(value, '')
  if (raw.length <= 10) return raw || '—'
  return `${raw.slice(0, 6)}…${raw.slice(-4)}`
}

export function statusTone(status: unknown): ResourceRow['tone'] {
  const normalized = String(status || '').trim().toLowerCase()
  if (['ready', 'available', 'online', 'enabled', 'active', 'ok', 'success', 'connected', 'completed'].includes(normalized)) return 'success'
  if (['failed', 'fault', 'offline', 'disabled', 'rejected', 'error', 'denied', 'revoked'].includes(normalized)) return 'danger'
  if (['switching', 'registering', 'recovering', 'reserved', 'busy', 'warning', 'degraded', 'pending'].includes(normalized)) return 'warning'
  if (['calling', 'ringing', 'connected', 'running'].includes(normalized)) return 'info'
  return 'muted'
}

export function booleanStatus(enabled: boolean | undefined, yes = '已启用', no = '已停用'): Pick<ResourceRow, 'status' | 'tone'> {
  if (enabled === undefined) return { status: '未配置', tone: 'muted' }
  return enabled ? { status: yes, tone: 'success' } : { status: no, tone: 'danger' }
}
