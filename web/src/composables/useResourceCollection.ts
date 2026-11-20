import { onMounted, ref, type Ref } from 'vue'
import type { AppError } from '../types/domain'
import { toAppError } from '../services/http'

export type ResourceCollection<T> = {
  items: Ref<T[]>
  loading: Ref<boolean>
  error: Ref<AppError | null>
  lastOkAt: Ref<number | null>
  load: () => Promise<void>
}

export function useResourceCollection<T>(loader: () => Promise<T[]>): ResourceCollection<T> {
  const items = ref<T[]>([]) as Ref<T[]>
  const loading = ref(false)
  const error = ref<AppError | null>(null)
  const lastOkAt = ref<number | null>(null)
  let revision = 0

  async function load() {
    const current = ++revision
    loading.value = true
    error.value = null
    try {
      const next = await loader()
      if (current !== revision) return
      items.value = next
      lastOkAt.value = Date.now()
    } catch (cause) {
      if (current !== revision) return
      error.value = toAppError(cause)
    } finally {
      if (current === revision) loading.value = false
    }
  }

  onMounted(() => { void load() })
  return { items, loading, error, lastOkAt, load }
}

