import { api } from '../stores/auth'
import type {
  AuditEventResource,
  CallRecordResource,
  CardResource,
  DispatchPolicyResource,
  ManagedUserResource,
  NumberPoolResource,
  NumberPoolMemberResource,
  NumberPoolUserResource,
  PhoneNumberResource
} from '../types/admin-resources'

type UnknownRecord = Record<string, unknown>

function isRecord(value: unknown): value is UnknownRecord {
  return !!value && typeof value === 'object' && !Array.isArray(value)
}

export function normalizeCollection<T>(payload: unknown, keys: string[]): T[] {
  if (Array.isArray(payload)) return payload as T[]
  if (!isRecord(payload)) return []
  for (const key of [...keys, 'items', 'data', 'results']) {
    const value = payload[key]
    if (Array.isArray(value)) return value as T[]
    if (isRecord(value)) {
      for (const nestedKey of keys) {
        const nested = value[nestedKey]
        if (Array.isArray(nested)) return nested as T[]
      }
    }
  }
  return []
}

async function getCollection<T>(url: string, keys: string[]): Promise<T[]> {
  const response = await api.get<unknown>(url)
  return normalizeCollection<T>(response.data, keys)
}

export const adminResourcesService = {
  cards: () => getCollection<CardResource>('/cards', ['cards']),
  numberPools: () => getCollection<NumberPoolResource>('/number-pools', ['number_pools', 'pools']),
  phoneNumbers: () => getCollection<PhoneNumberResource>('/phone-numbers', ['phone_numbers', 'numbers']),
  dispatchPolicies: () => getCollection<DispatchPolicyResource>('/dispatch-policies', ['dispatch_policies', 'policies']),
  users: () => getCollection<ManagedUserResource>('/admin/users', ['users']),
  callRecords: () => getCollection<CallRecordResource>('/call-records', ['call_records', 'records']),
  auditEvents: () => getCollection<AuditEventResource>('/audit-events', ['audit_events', 'events']),

  async createNumberPool(input: { name: string; description: string; enabled: boolean }) {
    return (await api.post<{ number_pool: NumberPoolResource }>('/number-pools', input)).data.number_pool
  },
  async updateNumberPool(id: string, input: { name: string; description: string; enabled: boolean }) {
    return (await api.patch<{ number_pool: NumberPoolResource }>(`/number-pools/${encodeURIComponent(id)}`, input)).data.number_pool
  },
  async replaceNumberPoolMembers(id: string, members: Array<{ phone_number_id: string; priority: number; weight: number; enabled: boolean }>) {
    return (await api.put<{ members: NumberPoolMemberResource[] }>(`/number-pools/${encodeURIComponent(id)}/members`, { members })).data.members
  },
  async replaceNumberPoolUsers(id: string, users: Array<{ user_id: string; role: string; enabled: boolean }>) {
    return (await api.put<{ users: NumberPoolUserResource[] }>(`/number-pools/${encodeURIComponent(id)}/users`, { users })).data.users
  },
  async upsertPhoneNumber(input: {
    id?: string
    subscription_profile_id?: string
    e164: string
    display_number?: string
    label?: string
    status?: string
  }) {
    return (await api.post<{ phone_number: PhoneNumberResource }>('/phone-numbers', input)).data.phone_number
  },
  async upsertDispatchPolicy(input: {
    id?: string
    number_pool_id: string
    name: string
    strategy: string
    priority: number
    enabled: boolean
    config: { rejection_threshold: number; disable_scope: string; timezone: string; restore_time: string }
  }) {
    return (await api.post<{ dispatch_policy: DispatchPolicyResource }>('/dispatch-policies', input)).data.dispatch_policy
  },
  async disableCard(id: string, reason: string) {
    await api.post(`/cards/${encodeURIComponent(id)}/disable`, { reason })
  },
  async enableCard(id: string) {
    await api.post(`/cards/${encodeURIComponent(id)}/enable`, {})
  },
  async activateCardProfile(cardId: string, profileId: string) {
    await api.post(`/cards/${encodeURIComponent(cardId)}/profiles/${encodeURIComponent(profileId)}/actions/activate`, {})
  },
  async createUser(input: { username: string; password: string; role: 'admin' | 'member' }) {
    return (await api.post<{ user: ManagedUserResource }>('/admin/users', input)).data.user
  },
  async updateUser(id: string, input: { username: string; role: 'admin' | 'member'; disabled: boolean }) {
    return (await api.patch<{ user: ManagedUserResource }>(`/admin/users/${encodeURIComponent(id)}`, input)).data.user
  },
  async resetUserPassword(id: string, password: string) {
    return (await api.post<{ user: ManagedUserResource }>(`/admin/users/${encodeURIComponent(id)}/password`, { password })).data.user
  },
  async revokeUserSessions(id: string) {
    await api.post(`/admin/users/${encodeURIComponent(id)}/sessions/revoke`, {})
  }
}
