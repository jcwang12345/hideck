export type AppRole = 'admin' | 'member' | string

export type AuthUser = {
  id: string
  username: string
  display_name?: string
  role: AppRole
  disabled?: boolean
  created_at?: string
  updated_at?: string
  permissions?: string[]
}

export type AuthSession = {
  id: string
  expires_at?: string
}

export type CardResource = {
  id?: string
  card_id?: string
  iccid?: string
  eid?: string
  kind?: string
  card_type?: string
  device_id?: string
  device_name?: string
  active_profile_id?: string
  active_profile_name?: string
  active_number?: string
  status?: string
  occupied_by?: string
  daily_calls?: number
  daily_rejections?: number
  disabled_reason?: string
  can_switch_profile?: boolean
  updated_at?: string
  capabilities?: CardCapabilityResource
  profiles?: SubscriptionProfileResource[]
}

export type CardCapabilityResource = {
  type?: string
  has_eid?: boolean
  has_isdr?: boolean
  multiple_profiles?: boolean
  profile_count?: number
  physical_uicc_confirmed?: boolean
}

export type SubscriptionProfileResource = {
  id: string
  iccid?: string
  aid?: string
  imsi?: string
  name?: string
  service_provider?: string
  present?: boolean
  active?: boolean
  last_observed_at?: string
}

export type PhoneNumberResource = {
  id: string
  subscription_profile_id?: string
  e164: string
  display_number?: string
  label?: string
  status?: string
  created_at?: string
  updated_at?: string
}

export type NumberPoolMemberResource = {
  id?: string
  number_pool_id?: string
  phone_number_id: string
  priority?: number
  weight?: number
  enabled?: boolean
  number?: PhoneNumberResource
  profile?: SubscriptionProfileResource
  card?: { id?: string; type?: string; last_device_id?: string }
}

export type NumberPoolUserResource = {
  id?: string
  number_pool_id?: string
  user_id: string
  role?: string
  enabled?: boolean
}

export type NumberPoolResource = {
  id?: string
  name?: string
  description?: string
  enabled?: boolean
  status?: string
  member_count?: number
  available_count?: number
  occupied_count?: number
  authorized_user_count?: number
  updated_at?: string
  members?: NumberPoolMemberResource[]
  users?: NumberPoolUserResource[]
  policies?: DispatchPolicyResource[]
}

export type DispatchPolicyResource = {
  id?: string
  name?: string
  enabled?: boolean
  status?: string
  number_pool_id?: string
  strategy?: string
  priority?: number
  config_json?: string
  rejection_threshold?: number
  disable_scope?: string
  timezone?: string
  restore_time?: string
  pool_name?: string
  updated_at?: string
}

export type ManagedUserResource = AuthUser & {
  session_version?: number
  last_login_at?: string
  active_call_count?: number
  pool_count?: number
}

export type CallRecordResource = {
  id?: string | number
  call_id?: string
  user_id?: string
  username?: string
  direction?: string
  peer?: string
  status?: string
  started_at?: string
  ended_at?: string
  duration_seconds?: number
  pool_name?: string
  local_number?: string
  device_id?: string
  end_reason?: string
}

export type AuditEventResource = {
  id?: string | number
  actor_id?: string
  actor_name?: string
  action?: string
  resource_type?: string
  resource_id?: string
  result?: string
  message?: string
  created_at?: string
}
