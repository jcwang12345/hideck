<script setup lang="ts">
import { computed } from 'vue'
import {
  ArrowRight20Regular,
  CheckmarkCircle20Regular,
  Warning20Regular
} from '@vicons/fluent'
import type { DashboardDevice } from '../types/api'
import {
  createDashboardDevicePresentation,
  hasDashboardSignal
} from '../utils/dashboardPresentation'
import {
  formatHealthEventTime,
  wifiCallingHealthEventLabel,
  wifiCallingHealthLabel,
  wifiCallingHealthTone
} from '../utils/wifiCallingHealth'

type DashboardDeviceRuntime = DashboardDevice & {
  interface?: string
}

const props = withDefaults(defineProps<{
  device: DashboardDevice
  selected?: boolean
}>(), {
  selected: false
})

const presentation = computed(() => createDashboardDevicePresentation(props.device))
const runtimeDevice = computed(() => props.device as DashboardDeviceRuntime)
const interfaceLabel = computed(() => String(runtimeDevice.value.interface || '').trim() || '--')
const signalBars = computed(() => {
  const dbm = props.device.signal_dbm
  if (!hasDashboardSignal(dbm)) return 0
  if (dbm > -70) return 4
  if (dbm > -85) return 3
  if (dbm > -100) return 2
  return 1
})
const signalTone = computed(() => {
  if (signalBars.value === 0) return 'is-unavailable'
  if (signalBars.value === 1) return 'is-danger'
  if (signalBars.value === 2) return 'is-warning'
  return 'is-good'
})
const healthTone = computed(() => wifiCallingHealthTone(props.device.vowifi_health?.state))
const healthLabel = computed(() => props.device.vowifi_health
  ? wifiCallingHealthLabel(props.device.vowifi_health)
  : props.device.healthy ? '状态监测中' : '设备离线')
const latestEvent = computed(() => {
  const events = props.device.vowifi_health?.events || []
  const event = events.at(-1)
  if (event) {
    return {
      label: wifiCallingHealthEventLabel(event),
      time: formatHealthEventTime(event.at)
    }
  }
  const updatedAt = props.device.vowifi_runtime?.updated_at
  if (updatedAt) {
    return {
      label: presentation.value.connectionDetail || '状态已更新',
      time: formatHealthEventTime(updatedAt)
    }
  }
  return {
    label: props.device.healthy ? '等待状态更新' : '设备当前离线',
    time: '--:--:--'
  }
})
</script>

<template>
  <button
    type="button"
    class="device-card"
    :class="{ 'device-card-selected': selected }"
    :aria-label="`选择 ${presentation.displayName}；双击打开设备工作区`"
    :aria-pressed="selected"
  >
    <span class="device-cell device-cell-identity">
      <span class="device-state-dot" :class="device.healthy ? 'is-online' : 'is-offline'" aria-hidden="true" />
      <span class="device-identity-copy">
        <strong>{{ presentation.displayName }}</strong>
        <small>{{ interfaceLabel }} · {{ presentation.connectionType }}</small>
      </span>
    </span>

    <span class="device-cell device-cell-signal" :class="signalTone">
      <span class="signal-bars" aria-hidden="true">
        <i v-for="bar in 4" :key="bar" :class="{ 'is-filled': signalBars >= bar }" />
      </span>
      <small>{{ presentation.signal }}</small>
    </span>

    <span class="device-cell device-cell-operator">
      <strong>{{ presentation.operator }}</strong>
      <small>{{ presentation.connectionType }}</small>
    </span>

    <span class="device-cell device-cell-health" :class="`is-${healthTone}`">
      <component :is="healthTone === 'danger' ? Warning20Regular : CheckmarkCircle20Regular" aria-hidden="true" />
      <strong>{{ healthLabel }}</strong>
    </span>

    <span class="device-cell device-cell-event">
      <strong>{{ latestEvent.label }}</strong>
      <time>{{ latestEvent.time }}</time>
    </span>

    <ArrowRight20Regular class="device-row-arrow" aria-hidden="true" />
  </button>
</template>

<style scoped>
.device-card {
  width: 100%;
  min-width: 0;
  min-height: 72px;
  padding: 10px 12px;
  display: grid;
  grid-template-columns: minmax(150px, 1.15fr) 92px minmax(110px, .9fr) minmax(104px, .85fr) minmax(132px, 1fr) 20px;
  align-items: center;
  gap: 14px;
  border: 0;
  border-bottom: 1px solid var(--ui-border);
  background: transparent;
  color: var(--ui-text);
  text-align: left;
  cursor: pointer;
  transition: background-color 160ms var(--ui-ease-out), box-shadow 160ms var(--ui-ease-out);
}
.device-card:first-of-type { border-top: 1px solid var(--ui-border); }
.device-card-selected { background: color-mix(in srgb, var(--ui-primary) 7%, transparent); box-shadow: 3px 0 0 var(--ui-primary) inset; }
.device-cell { min-width: 0; }
.device-cell strong,
.device-cell small,
.device-cell time { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }

.device-cell-identity { display: grid; grid-template-columns: 8px minmax(0, 1fr); align-items: center; gap: 10px; }
.device-state-dot { width: 8px; height: 8px; border-radius: 50%; background: var(--ui-text-muted); }
.device-state-dot.is-online { background: var(--ui-success); }
.device-state-dot.is-offline { background: var(--ui-danger); }
.device-identity-copy { min-width: 0; display: grid; gap: 4px; }
.device-identity-copy strong { font-size: 14px; font-weight: 700; }
.device-identity-copy small { color: var(--ui-text-muted); font-size: var(--ui-font-caption); }

.device-cell-signal { display: grid; justify-items: start; gap: 5px; color: var(--ui-text-muted); }
.device-cell-signal.is-good { color: var(--ui-success); }
.device-cell-signal.is-warning { color: var(--ui-warning); }
.device-cell-signal.is-danger { color: var(--ui-danger); }
.device-cell-signal small { color: var(--ui-text-muted); font: var(--ui-font-caption) "v-mono", ui-monospace, monospace; }
.signal-bars { width: 28px; height: 18px; display: flex; align-items: flex-end; gap: 3px; }
.signal-bars i { width: 4px; border-radius: 1px; background: color-mix(in srgb, currentColor 22%, transparent); }
.signal-bars i:nth-child(1) { height: 25%; }
.signal-bars i:nth-child(2) { height: 50%; }
.signal-bars i:nth-child(3) { height: 75%; }
.signal-bars i:nth-child(4) { height: 100%; }
.signal-bars i.is-filled { background: currentColor; }

.device-cell-operator,
.device-cell-event { display: grid; gap: 4px; }
.device-cell-operator strong,
.device-cell-event strong { font-size: 13px; font-weight: 650; }
.device-cell-operator small,
.device-cell-event time { color: var(--ui-text-muted); font-size: var(--ui-font-caption); }
.device-cell-event time { font-family: "v-mono", ui-monospace, monospace; }

.device-cell-health { display: flex; align-items: center; gap: 7px; color: var(--ui-text-muted); }
.device-cell-health svg { width: 18px; height: 18px; flex: 0 0 18px; }
.device-cell-health strong { font-size: var(--ui-font-caption); font-weight: 700; }
.device-cell-health.is-success { color: var(--ui-success); }
.device-cell-health.is-warning { color: var(--ui-warning); }
.device-cell-health.is-danger { color: var(--ui-danger); }
.device-row-arrow { width: 18px; height: 18px; color: var(--ui-text-muted); }

@media (hover: hover) and (pointer: fine) {
  .device-card:hover { background: color-mix(in srgb, var(--ui-primary) 5%, transparent); }
}

@container fleet-panel (max-width: 700px) {
  .device-card { grid-template-columns: minmax(138px, 1.2fr) 82px minmax(100px, .9fr) minmax(126px, 1fr) 20px; gap: 10px; }
  .device-cell-operator { display: none; }
}

@container fleet-panel (max-width: 520px) {
  .device-card { min-height: 82px; grid-template-columns: minmax(0, 1fr) auto 18px; grid-template-rows: auto auto; gap: 7px 12px; padding: 11px 8px; }
  .device-cell-identity { grid-column: 1; grid-row: 1; }
  .device-cell-signal { grid-column: 2; grid-row: 1; display: flex; align-items: center; gap: 7px; }
  .device-cell-health { grid-column: 1; grid-row: 2; padding-left: 18px; }
  .device-cell-event { grid-column: 2; grid-row: 2; justify-items: end; }
  .device-row-arrow { grid-column: 3; grid-row: 1 / span 2; }
}

@media (prefers-reduced-motion: reduce) {
  .device-card { transition: none; }
}
</style>
