const E911_PROMPT_PREFIX = 'hideck:e911-address:v1:'

export type E911PromptStorage = Pick<Storage, 'getItem' | 'setItem'>

function normalizedIdentityPart(value: string): string {
  return String(value || '').trim().toLowerCase()
}

function fnv1a(value: string): string {
  let hash = 0x811c9dc5
  for (let index = 0; index < value.length; index += 1) {
    hash ^= value.charCodeAt(index)
    hash = Math.imul(hash, 0x01000193)
  }
  return (hash >>> 0).toString(16).padStart(8, '0')
}

export function e911PromptKey(deviceID: string, iccid: string): string {
  const device = normalizedIdentityPart(deviceID)
  const card = normalizedIdentityPart(iccid)
  if (!device || !card) return ''
  return `${E911_PROMPT_PREFIX}${fnv1a(`${device}:${card}`)}`
}

export function browserE911PromptStorage(): E911PromptStorage | null {
  try {
    return typeof localStorage === 'undefined' ? null : localStorage
  } catch {
    return null
  }
}

export function wasE911PromptShown(storage: E911PromptStorage | null, key: string): boolean {
  if (!storage || !key) return false
  try {
    return storage.getItem(key) === 'shown'
  } catch {
    return false
  }
}

export function markE911PromptShown(storage: E911PromptStorage | null, key: string): void {
  if (!storage || !key) return
  try {
    storage.setItem(key, 'shown')
  } catch {
    // Private browsing or a full storage quota must not block the carrier websheet.
  }
}
