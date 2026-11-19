<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from 'vue'
import { api } from '../stores/auth'
import type { CarrierWebsheetInfo } from '../types/api'

const props = defineProps<{
  modelValue: boolean
  websheet: CarrierWebsheetInfo | null
}>()

const emit = defineEmits<{
  'update:modelValue': [value: boolean]
  done: []
}>()

const loaded = ref(false)
const iframeEl = ref<HTMLIFrameElement | null>(null)
let completing = false
let websheetChannel: BroadcastChannel | null = null

const iframeSrc = computed(() => props.websheet?.embedUrl || '')
const websheetToken = computed(() => {
  const raw = props.websheet?.embedUrl || ''
  if (!raw) return ''
  try {
    const url = new URL(raw, window.location.origin)
    return url.searchParams.get('token') || ''
  } catch {
    return ''
  }
})

watch(() => props.websheet?.id, () => {
  loaded.value = false
})

async function sendCallback(callback: unknown) {
  const id = props.websheet?.id
  if (!id || !callback || typeof callback !== 'object') return
  try {
    await api.post(`/websheets/${id}/callback`, callback)
  } catch (err) {
    console.error('[CarrierWebsheetDialog] relay callback failed:', err)
  }
}

async function completeWebsheet() {
  if (completing) return
  completing = true
  try {
    const id = props.websheet?.id
    if (id) {
      try {
        await api.post(`/websheets/${id}/done`)
      } catch (err) {
        console.error('[CarrierWebsheetDialog] complete websheet failed:', err)
      }
    }
    emit('done')
    emit('update:modelValue', false)
  } finally {
    completing = false
  }
}

function isTerminalCallback(callback: unknown) {
  if (!callback || typeof callback !== 'object') return true
  const record = callback as { event?: unknown; method?: unknown; resultCode?: unknown }
  const value = String(record.event ?? record.method ?? record.resultCode ?? '').toLowerCase()
  if (!value) return true
  return !value.includes('phoneservicesaccountstatuschanged')
}

function isCurrentWebsheetMessage(data: unknown): data is { type: string; token?: unknown; callback?: unknown } {
  if (!data || typeof data !== 'object') return false
  const record = data as { type?: unknown; token?: unknown }
  if (record.type !== 'hideck-websheet-callback') return false
  const currentToken = websheetToken.value
  const incomingToken = typeof record.token === 'string' ? record.token : ''
  if (currentToken && incomingToken && incomingToken !== currentToken) return false
  return true
}

function handleShellMessage(data: unknown) {
  if (!props.modelValue || !isCurrentWebsheetMessage(data)) return
  if (isTerminalCallback(data.callback)) {
    void completeWebsheet()
  } else {
    void sendCallback(data.callback)
  }
}

function onMessage(event: MessageEvent) {
  handleShellMessage(event.data)
}

function onStorage(event: StorageEvent) {
  if (event.key !== 'hideck-websheet-complete' || !event.newValue) return
  try {
    handleShellMessage(JSON.parse(event.newValue))
  } catch {
    // Ignore stale or malformed completion notifications.
  }
}

onMounted(() => {
  window.addEventListener('message', onMessage)
  window.addEventListener('storage', onStorage)
  try {
    websheetChannel = new BroadcastChannel('hideck-websheet')
    websheetChannel.onmessage = event => handleShellMessage(event.data)
  } catch {
    websheetChannel = null
  }
})
onUnmounted(() => {
  window.removeEventListener('message', onMessage)
  window.removeEventListener('storage', onStorage)
  websheetChannel?.close()
  websheetChannel = null
})
</script>

<template>
  <el-dialog
    :model-value="modelValue"
    :title="websheet?.title || '补充 911 紧急地址'"
    width="min(520px, 96vw)"
    class="carrier-websheet-dialog"
    destroy-on-close
    @update:model-value="emit('update:modelValue', $event)"
  >
    <div class="websheet-guidance">
      <strong>请在运营商页面填写实际使用地址</strong>
      <span>该地址仅用于紧急呼叫定位；完成后页面会自动返回 HiDeck。</span>
    </div>
    <div class="websheet-frame-shell relative overflow-hidden rounded-[var(--ui-radius-lg)] border border-[var(--ui-border)]">
      <div v-if="!loaded" class="absolute inset-0 z-10 flex items-center justify-center bg-[color-mix(in_srgb,var(--ui-surface)_82%,transparent)] text-sm text-[var(--ui-muted)]">
        加载中...
      </div>
      <iframe
        v-if="iframeSrc"
        ref="iframeEl"
        :src="iframeSrc"
        class="block h-full w-full border-0"
        sandbox="allow-forms allow-same-origin allow-scripts allow-popups allow-popups-to-escape-sandbox"
        @load="loaded = true"
      />
    </div>
  </el-dialog>
</template>

<style scoped>
.websheet-frame-shell {
  height: min(680px, 78vh);
}

.websheet-guidance {
  margin: -4px 0 12px;
  padding: 11px 13px;
  display: grid;
  gap: 3px;
  border: 1px solid color-mix(in srgb, var(--ui-warning) 34%, var(--ui-border));
  border-radius: var(--ui-radius-md);
  background: color-mix(in srgb, var(--ui-warning) 8%, var(--ui-surface));
}

.websheet-guidance strong {
  color: var(--ui-text);
  font-size: 14px;
}

.websheet-guidance span {
  color: var(--ui-text-muted);
  font-size: var(--ui-font-body-sm);
  line-height: 1.5;
}

@media (max-width: 640px) {
  :deep(.carrier-websheet-dialog) {
    width: 100% !important;
    max-width: none;
    height: 100dvh;
    margin: 0;
    border-radius: 0;
  }

  :deep(.carrier-websheet-dialog .el-dialog__header) {
    padding: calc(15px + env(safe-area-inset-top)) 16px 12px;
  }

  :deep(.carrier-websheet-dialog .el-dialog__body) {
    height: calc(100dvh - 64px - env(safe-area-inset-top));
    padding: 10px 12px calc(12px + env(safe-area-inset-bottom));
    display: flex;
    flex-direction: column;
  }

  .websheet-frame-shell {
    min-height: 0;
    height: auto;
    flex: 1 1 auto;
  }
}
</style>
