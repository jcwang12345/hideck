<script setup lang="ts">
import { computed } from 'vue'
import {
  ArrowRight20Regular,
  Checkmark12Regular,
  Dismiss12Regular,
  Location24Regular,
  Settings20Regular,
  Subtract12Regular
} from '@vicons/fluent'
import type { DashboardDevice } from '../types/api'
import type { DashboardConnectionStage } from '../utils/dashboardPresentation'
import {
  canAnimateDashboardConnection,
  createDashboardDevicePresentation
} from '../utils/dashboardPresentation'
import {
  formatHealthAvailability,
  formatHealthDuration,
  wifiCallingHealthLabel,
  wifiCallingHealthTone
} from '../utils/wifiCallingHealth'
import { t } from '../i18n'

type DashboardDeviceRuntime = DashboardDevice & {
  interface?: string
}

const props = withDefaults(defineProps<{
  device?: DashboardDevice
  e911SetupAvailable?: boolean
}>(), {
  e911SetupAvailable: false
})

const emit = defineEmits<{
  (event: 'open', deviceID?: string): void
  (event: 'open-e911', deviceID?: string): void
}>()

const emptyStages: readonly DashboardConnectionStage[] = Object.freeze(
  ['SIM', 'Access', 'Tunnel', 'IMS', 'SMS TX', 'SMS RX'].map(key => ({ key, ready: undefined }))
)
const presentation = computed(() => props.device ? createDashboardDevicePresentation(props.device) : null)
const stages = computed(() => presentation.value?.stages || emptyStages)
const pathIsFlowing = computed(() => !!props.device && canAnimateDashboardConnection(props.device))
const runtimeDevice = computed(() => props.device as DashboardDeviceRuntime | undefined)
const deviceInterface = computed(() => String(runtimeDevice.value?.interface || '').trim() || '--')
const focusMeta = computed(() => {
  if (!props.device || !presentation.value) return t('dashboard.waitDevice')
  return [props.device.id, deviceInterface.value, presentation.value.connectionType].join(' · ')
})
const focusDetail = computed(() => {
  if (!props.device) return t('dashboard.addDeviceFirst')
  if (!props.device.healthy) return t('dashboard.deviceOffline')
  if (props.device.phone_mode === 'modem_voice') return presentation.value?.connectionDetail
  if (presentation.value?.connectionKind === 'volte') return t('dashboard.viaVolte')
  if (props.device.vowifi_active) return presentation.value?.connectionDetail || t('dashboard.viaWifiIms')
  return t('dashboard.vowifiInactive')
})
const healthTone = computed(() => wifiCallingHealthTone(props.device?.vowifi_health?.state))
const healthLabel = computed(() => props.device?.vowifi_health
  ? wifiCallingHealthLabel(props.device.vowifi_health)
  : t('dashboard.waitState'))
const healthAvailability = computed(() => formatHealthAvailability(props.device?.vowifi_health))
const stableDuration = computed(() => {
  const health = props.device?.vowifi_health
  return health?.measured ? formatHealthDuration(health.stable_seconds) : '--'
})

function stageStatusLabel(stage: DashboardConnectionStage): string {
  if (stage.pending) return '待验证'
  if (stage.ready === false) return t('common.failed')
  if (stage.ready !== true) return t('dashboard.waitState')
  const labels: Record<string, string> = {
    SIM: '就绪',
    Access: '已接入',
    Tunnel: '已建立',
    IMS: '已注册',
    'SMS TX': '可发送',
    'SMS RX': '可接收',
    LTE: '已驻网',
    PDN: '已建立',
    Voice: '可用'
  }
  return labels[stage.key] || t('common.ready')
}
</script>

<template>
  <section class="connection-stage" :aria-label="t('dashboard.focusAria')">
    <header class="connection-stage-heading">
      <span class="dashboard-eyebrow">当前线路</span>
      <span
        class="focus-device-status"
        :class="device ? device.healthy ? 'is-online' : 'is-offline' : 'is-idle'"
      >
        <i aria-hidden="true" />
        {{ presentation?.statusLabel || t('dashboard.waitingDevice') }}
      </span>
    </header>

    <div class="connection-stage-copy">
      <h2>{{ presentation?.connectionTitle || t('dashboard.waitingDevice') }}</h2>
      <strong :class="{ 'is-idle': !device }">{{ presentation?.connectionState || t('dashboard.noDeviceAvailable') }}</strong>
      <p>{{ focusMeta }}</p>
      <small>{{ focusDetail }}</small>
    </div>

    <button
      v-if="e911SetupAvailable"
      type="button"
      class="e911-todo"
      @click="emit('open-e911', device?.id)"
    >
      <span class="e911-todo-icon" aria-hidden="true"><Location24Regular /></span>
      <span class="e911-todo-copy">
        <strong>启用 Wi-Fi Calling 前需完成紧急地址设置</strong>
        <small>当前 SIM 需要补充 911 地址（E911）</small>
      </span>
      <span class="e911-todo-action">
        补充 911 地址
        <ArrowRight20Regular aria-hidden="true" />
      </span>
    </button>

    <button type="button" class="focus-open-button" @click="emit('open', device?.id)">
      <Settings20Regular aria-hidden="true" />
      <span>{{ device ? '管理当前设备' : t('dashboard.goDevices') }}</span>
      <ArrowRight20Regular aria-hidden="true" />
    </button>

    <section class="connection-process" aria-label="通话服务链路">
      <h3>连接进程</h3>
      <div
        class="connection-path"
        :class="{ 'is-flowing': pathIsFlowing }"
        :style="{ gridTemplateColumns: `repeat(${stages.length}, minmax(0, 1fr))` }"
      >
        <div class="connection-path-track" aria-hidden="true" />
        <div
          v-for="stage in stages"
          :key="stage.key"
          class="connection-path-step"
          :class="{
            'is-ready': stage.ready === true,
            'is-failed': stage.ready === false
          }"
          :aria-label="`${stage.key}：${stageStatusLabel(stage)}`"
        >
          <span aria-hidden="true">
            <Checkmark12Regular v-if="stage.ready === true" />
            <Dismiss12Regular v-else-if="stage.ready === false" />
            <Subtract12Regular v-else />
          </span>
          <strong>{{ stage.key }}</strong>
          <small>{{ stageStatusLabel(stage) }}</small>
        </div>
      </div>
    </section>

    <dl class="connection-facts" :aria-label="t('dashboard.networkFacts')">
      <div>
        <dt>{{ t('dashboard.accessMode') }}</dt>
        <dd>{{ presentation?.connectionKind === 'wifi' ? 'Wi-Fi' : presentation?.connectionType || t('common.unavailable') }}</dd>
      </div>
      <div>
        <dt>{{ t('dashboard.operatorConnect') }}</dt>
        <dd>{{ presentation ? `${presentation.operator} · ${presentation.connectionType}` : t('common.unavailable') }}</dd>
      </div>
      <div>
        <dt>Wi-Fi Calling 健康</dt>
        <dd class="health-fact" :class="`is-${healthTone}`">{{ healthLabel }}</dd>
      </div>
      <div>
        <dt>本次会话可用率</dt>
        <dd class="is-tabular">{{ healthAvailability }}</dd>
      </div>
      <div>
        <dt>稳定时长</dt>
        <dd class="is-tabular">{{ stableDuration }}</dd>
      </div>
      <div>
        <dt>{{ t('dashboard.linkState') }}</dt>
        <dd>{{ presentation?.connectionState || t('dashboard.waitState') }}</dd>
      </div>
    </dl>
  </section>
</template>

<style scoped>
.connection-stage {
  min-width: 0;
  min-height: 560px;
  padding: 8px 30px 26px 10px;
  color: var(--ui-text);
}

.connection-stage-heading { display: flex; align-items: center; gap: 12px; }
.dashboard-eyebrow { color: var(--ui-text); font-size: 14px; font-weight: 700; }
.focus-device-status { display: inline-flex; align-items: center; gap: 7px; color: var(--ui-text-muted); font-size: var(--ui-font-caption); font-weight: 650; }
.focus-device-status i { width: 7px; height: 7px; border-radius: 50%; background: currentColor; }
.focus-device-status.is-online { color: var(--ui-success); }
.focus-device-status.is-offline { color: var(--ui-danger); }
.focus-device-status.is-idle { color: var(--ui-text-muted); }
.focus-device-status.is-online i { animation: online-pulse 2.4s var(--ui-ease-in-out) infinite; }

.connection-stage-copy { margin-top: 22px; }
.connection-stage h2 { margin: 0; color: var(--ui-text); font-size: clamp(48px, 4vw, 66px); font-weight: 620; letter-spacing: -.045em; line-height: 1; }
.connection-stage-copy > strong { display: block; margin: 10px 0 14px; color: var(--ui-success); font-size: clamp(30px, 2.6vw, 42px); font-weight: 650; line-height: 1.05; }
.connection-stage-copy > strong.is-idle { color: var(--ui-warning); }
.connection-stage-copy > p { margin: 0; color: var(--ui-text); font-size: 17px; font-weight: 560; overflow-wrap: anywhere; }
.connection-stage-copy > small { display: block; margin-top: 8px; color: var(--ui-text-muted); font-size: 14px; line-height: 1.55; }

.e911-todo {
  width: 100%;
  min-height: 62px;
  margin-top: 22px;
  padding: 10px 12px;
  display: grid;
  grid-template-columns: 34px minmax(0, 1fr) auto;
  align-items: center;
  gap: 10px;
  border: 1px solid color-mix(in srgb, var(--ui-warning) 42%, var(--ui-border));
  border-radius: var(--ui-radius-md);
  background: var(--ui-warning-surface);
  color: var(--ui-text);
  text-align: left;
  cursor: pointer;
}
.e911-todo-icon { width: 34px; height: 34px; display: grid; place-items: center; color: var(--ui-warning); }
.e911-todo-icon svg { width: 24px; height: 24px; }
.e911-todo-copy { min-width: 0; display: grid; gap: 3px; }
.e911-todo-copy strong { font-size: 13px; line-height: 1.35; }
.e911-todo-copy small { color: var(--ui-text-muted); font-size: var(--ui-font-caption); }
.e911-todo-action { min-height: 36px; padding: 0 10px; display: inline-flex; align-items: center; gap: 4px; border-radius: var(--ui-radius-sm); background: color-mix(in srgb, var(--ui-warning) 22%, var(--ui-surface)); color: var(--ui-text); font-size: var(--ui-font-caption); font-weight: 700; white-space: nowrap; }
.e911-todo-action svg { width: 16px; height: 16px; }

.focus-open-button {
  width: min(100%, 300px);
  min-height: 44px;
  margin-top: 14px;
  padding: 0 14px;
  display: grid;
  grid-template-columns: 20px minmax(0, 1fr) 18px;
  align-items: center;
  gap: 10px;
  border: 1px solid var(--ui-primary);
  border-radius: var(--ui-radius-sm);
  background: var(--ui-primary);
  color: var(--ui-on-primary, #fff);
  font-size: 14px;
  font-weight: 700;
  cursor: pointer;
  transition: filter 160ms var(--ui-ease-out), transform 140ms var(--ui-ease-out);
}
.focus-open-button svg { width: 18px; height: 18px; }
.focus-open-button:active { transform: scale(.98); }

.connection-process { margin-top: 22px; }
.connection-process h3 { margin: 0 0 12px; color: var(--ui-text); font-size: 16px; font-weight: 700; }
.connection-path { position: relative; display: grid; container-type: inline-size; }
.connection-path-track { position: absolute; top: 16px; right: 8%; left: 8%; height: 1px; background: var(--ui-border); }
.connection-path.is-flowing .connection-path-track { background: var(--ui-success); }
.connection-path-step { position: relative; z-index: 1; min-width: 0; display: grid; justify-items: center; gap: 3px; color: var(--ui-text-muted); text-align: center; }
.connection-path-step > span { width: 34px; height: 34px; display: grid; place-items: center; border: 1px solid var(--ui-border); border-radius: 50%; background: var(--ui-surface); color: inherit; }
.connection-path-step svg { width: 14px; height: 14px; }
.connection-path-step strong { margin-top: 3px; color: var(--ui-text); font-size: var(--ui-font-caption); }
.connection-path-step small { color: var(--ui-text-muted); font-size: var(--ui-font-caption); }
.connection-path-step.is-ready { color: var(--ui-success); }
.connection-path-step.is-ready > span { border-color: var(--ui-success); }
.connection-path-step.is-failed { color: var(--ui-danger); }
.connection-path-step.is-failed > span { border-color: var(--ui-danger); }

.connection-facts { margin: 18px 0 0; padding-top: 6px; display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); border-top: 1px solid var(--ui-border); }
.connection-facts > div { min-width: 0; min-height: 34px; display: grid; grid-template-columns: minmax(112px, .8fr) minmax(0, 1.2fr); align-items: center; gap: 12px; }
.connection-facts dt { color: var(--ui-text-muted); font-size: var(--ui-font-caption); }
.connection-facts dd { min-width: 0; margin: 0; color: var(--ui-text); font-size: 13px; font-weight: 600; overflow-wrap: anywhere; }
.connection-facts .is-tabular { font-family: "v-mono", ui-monospace, monospace; font-variant-numeric: tabular-nums; }
.health-fact.is-success { color: var(--ui-success); }
.health-fact.is-warning { color: var(--ui-warning); }
.health-fact.is-danger { color: var(--ui-danger); }

@keyframes online-pulse { 0%, 100% { opacity: .55; } 50% { opacity: 1; } }

@media (hover: hover) and (pointer: fine) {
  .focus-open-button:hover { filter: brightness(1.08); }
  .e911-todo:hover { border-color: var(--ui-warning); }
}

@media (max-width: 1260px) {
  .connection-stage { padding-right: 24px; }
  .connection-stage h2 { font-size: 48px; }
  .connection-facts { grid-template-columns: minmax(0, 1fr); }
}

@media (max-width: 820px) {
  .connection-stage { min-height: 0; padding: 4px 0 24px; }
  .connection-stage h2 { font-size: clamp(38px, 12vw, 54px); }
  .connection-stage-copy > strong { font-size: 32px; }
  .focus-open-button { width: 100%; }
  .connection-facts { grid-template-columns: repeat(2, minmax(0, 1fr)); }
}

@media (max-width: 560px) {
  .e911-todo { grid-template-columns: 30px minmax(0, 1fr); }
  .e911-todo-action { min-height: 38px; grid-column: 1 / -1; justify-content: center; }
  .connection-path { grid-template-columns: minmax(0, 1fr) !important; gap: 0; }
  .connection-path-track { top: 16px; right: auto; bottom: 16px; left: 16px; width: 1px; height: auto; }
  .connection-path-step { min-height: 44px; grid-template-columns: 34px 72px minmax(0, 1fr); justify-items: start; align-items: center; gap: 10px; text-align: left; }
  .connection-path-step > span { grid-column: 1; }
  .connection-path-step strong { margin: 0; }
  .connection-facts { grid-template-columns: minmax(0, 1fr); }
}

@media (prefers-reduced-motion: reduce) {
  .focus-device-status.is-online i { animation: none; }
  .focus-open-button { transition-duration: 0ms; }
}
</style>
