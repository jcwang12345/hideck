import type { AuthUser } from '../types/admin-resources'

export type AccessRule = {
  roles?: string[]
  permissions?: string[]
}

export function isAdminUser(user: AuthUser | null | undefined): boolean {
  return user?.role === 'admin'
}

export function canAccess(user: AuthUser | null | undefined, rule: AccessRule): boolean {
  if (!user || user.disabled) return false
  if (isAdminUser(user)) return true
  if (rule.roles?.length && !rule.roles.includes(user.role)) return false
  if (rule.permissions?.length) {
    const granted = new Set(user.permissions || [])
    return rule.permissions.every(permission => granted.has(permission))
  }
  return true
}

export function homePathForUser(user: AuthUser | null | undefined): string {
  return isAdminUser(user) ? '/' : '/phone'
}

