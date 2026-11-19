<script setup lang="ts">
import { nextTick, ref, watch } from 'vue'
import { Dismiss24Regular } from '@vicons/fluent'
import BalancePanel from './BalancePanel.vue'
import type { DeviceMgmtListItem } from '../../types/api'
import type { BalanceQuery, CarrierQueryRule } from '../../types/commands'

const props = defineProps<{
  modelValue: boolean
  devices: DeviceMgmtListItem[]
  selectedDevice: string
  queries: BalanceQuery[]
  builtInRules: CarrierQueryRule[]
  customRules: CarrierQueryRule[]
  loading: boolean
  querying: boolean
  manualBalanceOpening: boolean
  rulesLoading: boolean
  rulesLoaded: boolean
  rulesError: string
}>()

const emit = defineEmits<{
  'update:modelValue': [value: boolean]
  'update:selectedDevice': [value: string]
  query: []
  editManualBalance: []
  editRules: []
  editBuiltInRules: []
  editRule: [rule: CarrierQueryRule]
  refreshRules: []
}>()

const rail = ref<HTMLElement | null>(null)

watch(() => props.modelValue, async (open) => {
  if (!open) return
  await nextTick()
  const reduceMotion = window.matchMedia('(prefers-reduced-motion: reduce)').matches
  const mobileTray = window.matchMedia('(max-width: 820px)').matches
  if (!mobileTray) {
    rail.value?.scrollIntoView({ behavior: reduceMotion ? 'auto' : 'smooth', block: 'nearest' })
  }
  rail.value?.focus({ preventScroll: true })
  if (!mobileTray) emit('update:modelValue', false)
})

function closeMobileTray() {
  emit('update:modelValue', false)
}
</script>

<template>
  <div class="balance-drawer-host" :class="{ 'is-open': modelValue }">
    <button
      v-if="modelValue"
      type="button"
      class="balance-tray-scrim"
      aria-label="关闭余额面板"
      @click="closeMobileTray"
    />
    <aside
      id="command-balance-rail"
      ref="rail"
      class="balance-rail"
      tabindex="-1"
      aria-label="余额查询与运营商规则"
    >
      <header class="balance-mobile-heading">
        <div><small>BALANCE &amp; RULES</small><strong>余额与运营商规则</strong></div>
        <button type="button" aria-label="关闭余额面板" @click="closeMobileTray">
          <Dismiss24Regular aria-hidden="true" />
        </button>
      </header>
      <BalancePanel
        :selected-device="selectedDevice"
        :devices="devices"
        :queries="queries"
        :built-in-rules="builtInRules"
        :custom-rules="customRules"
        :loading="loading"
        :querying="querying"
        :manual-balance-opening="manualBalanceOpening"
        :rules-loading="rulesLoading"
        :rules-loaded="rulesLoaded"
        :rules-error="rulesError"
        @update:selected-device="emit('update:selectedDevice', $event)"
        @query="emit('query')"
        @edit-manual-balance="emit('editManualBalance')"
        @edit-rules="emit('editRules')"
        @edit-built-in-rules="emit('editBuiltInRules')"
        @edit-rule="emit('editRule', $event)"
        @refresh-rules="emit('refreshRules')"
      />
    </aside>
  </div>
</template>

<style scoped>
.balance-drawer-host { min-width: 0; min-height: 0; display: contents; }
.balance-rail {
  min-width: 0;
  min-height: 0;
  overflow: hidden;
  border-left: 1px solid var(--ui-border);
  background: color-mix(in srgb, var(--ui-surface-strong) 72%, var(--ui-surface));
  outline: none;
}
.balance-rail:focus-visible { box-shadow: inset 0 0 0 2px color-mix(in srgb, var(--ui-primary) 34%, transparent); }
.balance-mobile-heading,
.balance-tray-scrim { display: none; }
@media (max-width: 1023px) {
  .balance-rail { min-height: 560px; border-top: 1px solid var(--ui-border); border-left: 0; }
}
@media (max-width: 820px) {
  .balance-drawer-host {
    position: fixed;
    z-index: 52;
    inset: 0 0 calc(68px + env(safe-area-inset-bottom));
    display: block;
    pointer-events: none;
  }
  .balance-drawer-host.is-open { pointer-events: auto; }
  .balance-tray-scrim {
    position: absolute;
    inset: 0;
    display: block;
    border: 0;
    background: color-mix(in srgb, #000 46%, transparent);
  }
  .balance-rail {
    position: absolute;
    right: 0;
    bottom: 0;
    left: 0;
    height: min(78dvh, 720px);
    min-height: 0;
    overflow: hidden;
    border: 1px solid var(--ui-border);
    border-bottom: 0;
    border-radius: var(--ui-radius-xl) var(--ui-radius-xl) 0 0;
    background: var(--ui-surface);
    box-shadow: 0 -18px 50px color-mix(in srgb, #000 22%, transparent);
    visibility: hidden;
    transform: translateY(102%);
    transition: transform 220ms var(--ui-ease-drawer), visibility 0s linear 220ms;
  }
  .is-open .balance-rail { visibility: visible; transform: translateY(0); transition-delay: 0s; }
  .balance-mobile-heading {
    min-height: 58px;
    padding: 8px 12px 8px 16px;
    display: flex;
    align-items: center;
    justify-content: space-between;
    border-bottom: 1px solid var(--ui-border);
  }
  .balance-mobile-heading div { display: grid; gap: 2px; }
  .balance-mobile-heading small { color: var(--ui-primary); font: 700 var(--ui-font-caption) "v-mono", monospace; letter-spacing: .1em; }
  .balance-mobile-heading strong { color: var(--ui-text); font-size: 15px; }
  .balance-mobile-heading button {
    width: 44px;
    height: 44px;
    display: grid;
    place-items: center;
    border: 0;
    border-radius: var(--ui-radius-md);
    background: var(--ui-surface-muted);
    color: var(--ui-text-muted);
  }
  .balance-mobile-heading svg { width: 20px; height: 20px; }
  .balance-rail :deep(.balance-panel) { height: calc(100% - 58px); min-height: 0; }
}
@media (prefers-reduced-motion: reduce) {
  .balance-rail { transition: none; }
}
</style>
