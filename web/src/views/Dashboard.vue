<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { storeToRefs } from 'pinia'
import { useRouter } from 'vue-router'
import DeviceCard from '../components/DeviceCard.vue'
import ConnectionFocusStage from '../components/ConnectionFocusStage.vue'
import PageHeader from '../components/PageHeader.vue'
import EmptyState from '../components/EmptyState.vue'
import ListSkeleton from '../components/ListSkeleton.vue'
import ErrorState from '../components/ErrorState.vue'
import RefreshButton from '../components/RefreshButton.vue'
import TrafficAnalysisPanel from '../components/TrafficAnalysisPanel.vue'
import { usePollingScheduler } from '../composables/usePollingScheduler'
import { useDashboardStore } from '../stores/dashboard'
import { devicesService } from '../services/devices'
import type { TrafficRange } from '../services/traffic'
import type { DashboardDevice } from '../types/api'
import type { DeviceDetailVM } from '../types/view-model'
import { formatDeviceTime } from '../utils/deviceTime'
import { filterDashboardDevices } from '../utils/dashboardPresentation'
import { formatPlmnOperatorLabel, getMccMncIndex, type MccMncRow } from '../utils/mcc-mnc'
import { Search } from '@element-plus/icons-vue'
import { Add24Regular, ArrowRight24Regular } from '@vicons/fluent'
import { t, useLocale } from '../i18n'

useLocale()

const dashboard = useDashboardStore()
const router = useRouter()
const {
  devices,
  devicesLoading: loading,
  devicesLastOkAt,
  devicesError,
  analysis,
  analysisLoading,
  analysisLastOkAt,
  analysisError
} = storeToRefs(dashboard)

const analysisRange = ref<TrafficRange>('day')
const searchQuery = ref('')
const statusFilter = ref<'all' | 'online' | 'offline'>('all')
const selectedDeviceID = ref('')
const selectedDetail = ref<DeviceDetailVM | null>(null)
const mccMncIndex = ref<Map<string, MccMncRow> | null>(null)
let detailRequestID = 0

const labeledDevices = computed(() => devices.value.map((device) => {
  const operator = formatPlmnOperatorLabel(device.operator || '', mccMncIndex.value)
  if (!operator || operator === (device.operator || '')) return device
  return { ...device, operator }
}))
const totalCount = computed(() => devices.value.length)
const onlineCount = computed(() => devices.value.filter(d => d?.healthy).length)
const offlineCount = computed(() => Math.max(0, totalCount.value - onlineCount.value))
const filteredDevices = computed(() => filterDashboardDevices(labeledDevices.value, {
  query: searchQuery.value,
  status: statusFilter.value
}))
const deviceGridKey = computed(() => [
  statusFilter.value,
  searchQuery.value.trim().toLocaleLowerCase(),
  filteredDevices.value.map(device => device.id).join(',')
].join(':'))
const selectedDevice = computed(() => {
  return labeledDevices.value.find((device) => device.id === selectedDeviceID.value)
    || labeledDevices.value.find((device) => device.healthy)
    || labeledDevices.value[0]
})
const selectedDeviceModel = computed({
  get: () => selectedDevice.value?.id || '',
  set: (deviceID: string) => { selectedDeviceID.value = deviceID }
})
const selectedE911SetupAvailable = computed(() => (
  selectedDetail.value?.id === selectedDevice.value?.id
  && selectedDetail.value?.e911_setup_available === true
))

async function fetchDevices() {
  await dashboard.fetchDevices()
}

async function fetchSelectedDeviceDetail() {
  const id = String(selectedDevice.value?.id || '').trim()
  const requestID = ++detailRequestID
  if (!id) {
    selectedDetail.value = null
    return
  }
  const result = await devicesService.getOverviewLite(id)
  if (requestID !== detailRequestID || selectedDevice.value?.id !== id) return
  selectedDetail.value = result.ok ? result.data : null
}

async function fetchTrafficAnalysis() {
  await dashboard.fetchAnalysis(analysisRange.value)
}

function handleAnalysisRangeChange(range: TrafficRange) {
  if (analysisRange.value === range) return
  analysisRange.value = range
  void fetchTrafficAnalysis()
}

function openDeviceOverview(id?: string) {
  const deviceID = String(id || '').trim()
  if (!deviceID) {
    void router.push({ name: 'Devices' })
    return
  }
  void router.push({
    name: 'Devices',
    query: {
      device: deviceID,
      tab: 'overview'
    }
  })
}

function openE911Setup(id?: string) {
  const deviceID = String(id || '').trim()
  if (!deviceID) return
  void router.push({
    name: 'Devices',
    query: {
      device: deviceID,
      tab: 'overview',
      e911: '1'
    }
  })
}

function selectDevice(device: DashboardDevice) {
  selectedDeviceID.value = device.id
}

watch(
  () => selectedDevice.value?.id || '',
  () => { void fetchSelectedDeviceDetail() },
  { immediate: true }
)

usePollingScheduler(fetchDevices, 5000, {
  immediate: true,
  maxIntervalMs: 30000,
  backgroundIntervalMs: 15000
})
usePollingScheduler(fetchSelectedDeviceDetail, 30000, {
  immediate: false,
  maxIntervalMs: 120000,
  backgroundIntervalMs: 60000
})
usePollingScheduler(fetchTrafficAnalysis, 60000, {
  immediate: false,
  maxIntervalMs: 300000,
  backgroundIntervalMs: 120000
})

onMounted(() => {
  void getMccMncIndex().then((index) => {
    mccMncIndex.value = index
  })
  const win = window as Window & {
    requestIdleCallback?: (cb: IdleRequestCallback, opts?: IdleRequestOptions) => number
  }
  if (typeof win.requestIdleCallback === 'function') {
    win.requestIdleCallback(() => fetchTrafficAnalysis(), { timeout: 1500 })
  } else {
    setTimeout(fetchTrafficAnalysis, 800)
  }
})
</script>

<template>
  <div class="app-page dashboard-page">
    <PageHeader :title="t('dashboard.title')" :subtitle="t('dashboard.subtitle')">
      <template #actions>
        <RefreshButton :loading="loading" @click="fetchDevices" />
      </template>
    </PageHeader>

    <ErrorState
      v-if="devicesError"
      class="mb-4"
      :title="t('dashboard.loadFailed')"
      :message="devicesError.message"
      :status-code="devicesError.status"
      :request-method="devicesError.method"
      :request-url="devicesError.url"
      :last-success-at="devicesLastOkAt"
      :retry-text="t('common.retry')"
      @retry="fetchDevices"
    />

    <section class="signal-workspace ui-card ui-workspace-glow" aria-label="信号工作台">
      <div v-if="devices.length > 0" class="mobile-device-picker">
        <span>当前设备</span>
        <el-select v-model="selectedDeviceModel" aria-label="选择当前设备">
          <el-option
            v-for="device in labeledDevices"
            :key="device.id"
            :label="`${device.name || device.id} · ${device.id}`"
            :value="device.id"
          />
        </el-select>
      </div>

      <div class="signal-workspace-grid">
        <div class="current-line-panel">
          <Transition name="focus-swap" mode="out-in">
            <ConnectionFocusStage
              :key="selectedDevice?.id || 'empty'"
              :device="selectedDevice"
              :e911-setup-available="selectedE911SetupAvailable"
              @open="openDeviceOverview"
              @open-e911="openE911Setup"
            />
          </Transition>
        </div>

        <section class="fleet-activity" :aria-label="t('dashboard.fleetSummary')">
          <header class="device-overview-toolbar">
            <div>
              <span class="section-kicker">DEVICE FLEET</span>
              <h2>设备编队 · 活动</h2>
              <p>
                共 {{ totalCount }} 台设备，{{ onlineCount }} 台在线
                <template v-if="devicesLastOkAt"> · 更新 {{ formatDeviceTime(devicesLastOkAt, { clientClock: true }) }}</template>
              </p>
            </div>
            <RefreshButton :loading="loading" @click="fetchDevices" />
          </header>

          <div class="fleet-summary" aria-label="设备汇总">
            <div class="fleet-metric">
              <span>{{ t('dashboard.all') }}</span>
              <strong>{{ totalCount }}</strong>
            </div>
            <div class="fleet-metric is-online">
              <span>{{ t('dashboard.online') }}</span>
              <strong>{{ onlineCount }}</strong>
            </div>
            <div class="fleet-metric is-offline">
              <span>{{ t('dashboard.offline') }}</span>
              <strong>{{ offlineCount }}</strong>
            </div>
          </div>

          <div v-if="devices.length > 0" class="device-filter-controls" :aria-label="t('dashboard.filterAria')">
            <el-input v-model="searchQuery" clearable :placeholder="t('dashboard.searchPlaceholder')" :prefix-icon="Search" />
            <el-segmented
              v-model="statusFilter"
              :options="[
                { label: t('dashboard.all'), value: 'all' },
                { label: t('dashboard.online'), value: 'online' },
                { label: t('dashboard.offline'), value: 'offline' }
              ]"
            />
          </div>

          <ListSkeleton v-if="loading && devices.length === 0" :rows="4" />

          <button
            v-else-if="devices.length === 0"
            type="button"
            class="device-fleet-empty"
            :aria-label="t('dashboard.emptyAria')"
            @click="openDeviceOverview()"
          >
            <span class="device-fleet-empty-icon" aria-hidden="true"><Add24Regular /></span>
            <span class="device-fleet-empty-copy">
              <strong>{{ t('dashboard.emptyTitle') }}</strong>
              <small>{{ t('dashboard.emptyHint') }}</small>
            </span>
            <span class="device-fleet-empty-action">
              {{ t('dashboard.manageDevices') }}
              <ArrowRight24Regular aria-hidden="true" />
            </span>
          </button>

          <template v-else>
            <EmptyState
              v-if="filteredDevices.length === 0"
              title="没有匹配的设备"
              subtitle="请调整搜索关键词或在线状态筛选"
            />
            <template v-else>
              <div class="fleet-table-heading" aria-hidden="true">
                <span>设备编号</span>
                <span>信号</span>
                <span>运营商</span>
                <span>健康状态</span>
                <span>最近事件</span>
                <span />
              </div>
              <section :key="deviceGridKey" class="device-status-grid" aria-label="设备实时状态">
                <DeviceCard
                  v-for="(dev, index) in filteredDevices"
                  :key="dev.id"
                  :device="dev"
                  :selected="selectedDevice?.id === dev.id"
                  :style="{ '--device-index': index }"
                  @click="selectDevice(dev)"
                  @dblclick="openDeviceOverview(dev.id)"
                />
              </section>
            </template>
          </template>
        </section>
      </div>
    </section>

    <TrafficAnalysisPanel
      v-if="devices.length > 0 || !loading"
      class="dashboard-traffic"
      :analysis="analysis"
      :loading="analysisLoading"
      :error="analysisError"
      :last-ok-at="analysisLastOkAt"
      :range="analysisRange"
      mode="global"
      @update:range="handleAnalysisRangeChange"
      @refresh="fetchTrafficAnalysis"
    />
  </div>
</template>

<style scoped>
.dashboard-page :deep(.page-header) { margin-bottom: 18px; }
.section-kicker { color: var(--ui-primary); font: 700 var(--ui-font-caption) "v-mono", ui-monospace, monospace; letter-spacing: .14em; }
.signal-workspace { min-width: 0; overflow: hidden; border-radius: var(--ui-radius-xl); background: var(--ui-surface); }
.signal-workspace-grid { min-width: 0; display: grid; grid-template-columns: minmax(470px, .86fr) minmax(620px, 1.34fr); }
.current-line-panel { min-width: 0; padding: 28px 0 22px 30px; border-right: 1px solid var(--ui-border); }
.fleet-activity { min-width: 0; padding: 34px 30px 26px; container: fleet-panel / inline-size; }

.device-overview-toolbar { margin: 0 0 20px; display: flex; align-items: start; justify-content: space-between; gap: 24px; }
.device-overview-toolbar h2 { margin: 5px 0 4px; color: var(--ui-text); font-size: 26px; font-weight: 680; letter-spacing: -.025em; }
.device-overview-toolbar p { margin: 0; color: var(--ui-text-muted); font-size: 13px; }
.fleet-summary { margin-bottom: 16px; display: grid; grid-template-columns: repeat(3, minmax(0, 1fr)); border-block: 1px solid var(--ui-border); }
.fleet-metric { min-width: 0; padding: 10px 12px; display: flex; align-items: baseline; justify-content: space-between; gap: 10px; border-right: 1px solid var(--ui-border); }
.fleet-metric:first-child { padding-left: 0; }
.fleet-metric:last-child { padding-right: 0; border-right: 0; }
.fleet-metric span { color: var(--ui-text-muted); font-size: var(--ui-font-caption); }
.fleet-metric strong { color: var(--ui-text); font: 700 18px "v-mono", ui-monospace, monospace; }
.fleet-metric.is-online strong { color: var(--ui-success); }
.fleet-metric.is-offline strong { color: var(--ui-danger); }

.device-filter-controls { margin-bottom: 18px; display: grid; grid-template-columns: minmax(200px, 1fr) auto; gap: 10px; }
.fleet-table-heading { min-height: 38px; padding: 0 12px; display: grid; grid-template-columns: minmax(150px, 1.15fr) 92px minmax(110px, .9fr) minmax(104px, .85fr) minmax(132px, 1fr) 20px; align-items: center; gap: 14px; color: var(--ui-text-muted); font-size: var(--ui-font-caption); }
.device-status-grid { min-width: 0; display: grid; }
.device-status-grid :deep(.device-card) { animation: device-card-enter 220ms var(--ui-ease-out) both; animation-delay: min(calc(var(--device-index, 0) * 30ms), 180ms); }

.device-fleet-empty { width: 100%; min-height: 96px; padding: 18px 20px; display: grid; grid-template-columns: 44px minmax(0, 1fr) auto; align-items: center; gap: 16px; border: 1px solid var(--ui-border); border-radius: var(--ui-radius-lg); background: var(--ui-surface-muted); color: var(--ui-text); text-align: left; cursor: pointer; }
.device-fleet-empty-icon { width: 44px; height: 44px; display: grid; place-items: center; border: 1px solid var(--ui-border); border-radius: var(--ui-radius-md); background: var(--ui-surface); color: var(--ui-primary); }
.device-fleet-empty-icon svg { width: 21px; height: 21px; }
.device-fleet-empty-copy { min-width: 0; display: grid; gap: 4px; }
.device-fleet-empty-copy strong { font-size: 14px; font-weight: 650; }
.device-fleet-empty-copy small { color: var(--ui-text-muted); font-size: var(--ui-font-caption); line-height: 1.45; }
.device-fleet-empty-action { min-height: 38px; padding: 0 13px; display: inline-flex; align-items: center; gap: 8px; color: var(--ui-primary); font-size: var(--ui-font-caption); font-weight: 650; }
.device-fleet-empty-action svg { width: 17px; height: 17px; }
.mobile-device-picker { display: none; }
.dashboard-traffic { margin-top: 18px !important; }

.focus-swap-enter-active { transition: opacity 200ms var(--ui-ease-out), transform 200ms var(--ui-ease-out); }
.focus-swap-leave-active { transition: opacity 100ms var(--ui-ease-out), transform 100ms var(--ui-ease-out); }
.focus-swap-enter-from { opacity: 0; transform: translateY(8px); }
.focus-swap-leave-to { opacity: 0; transform: translateY(-3px); }
@keyframes device-card-enter { from { opacity: 0; transform: translateY(6px); } to { opacity: 1; transform: translateY(0); } }

@container fleet-panel (max-width: 700px) {
  .fleet-table-heading { grid-template-columns: minmax(138px, 1.2fr) 82px minmax(100px, .9fr) minmax(126px, 1fr) 20px; gap: 10px; }
  .fleet-table-heading span:nth-child(3) { display: none; }
}

@media (max-width: 1360px) {
  .signal-workspace-grid { grid-template-columns: minmax(430px, .9fr) minmax(520px, 1.1fr); }
  .current-line-panel { padding-left: 24px; }
  .fleet-activity { padding-inline: 22px; }
}

@media (max-width: 1080px) {
  .signal-workspace-grid { grid-template-columns: minmax(0, 1fr); }
  .current-line-panel { padding: 26px 24px 0; border-right: 0; border-bottom: 1px solid var(--ui-border); }
  .fleet-activity { padding: 26px 24px; }
}

@media (max-width: 820px) {
  .signal-workspace { border-radius: var(--ui-radius-lg); }
  .mobile-device-picker { padding: 14px 14px 0; display: grid; grid-template-columns: auto minmax(0, 1fr); align-items: center; gap: 12px; }
  .mobile-device-picker > span { color: var(--ui-text); font-size: 13px; font-weight: 700; }
  .current-line-panel { padding: 24px 16px 0; }
  .fleet-activity { padding: 24px 16px; }
  .device-overview-toolbar h2 { font-size: 22px; }
  .fleet-table-heading { display: none; }
  .device-filter-controls { grid-template-columns: minmax(0, 1fr); }
}

@media (max-width: 520px) {
  .mobile-device-picker { grid-template-columns: minmax(0, 1fr); gap: 7px; }
  .device-overview-toolbar { align-items: center; }
  .device-overview-toolbar p { max-width: 240px; }
  .fleet-summary { grid-template-columns: repeat(3, minmax(0, 1fr)); }
  .fleet-metric { padding-inline: 8px; display: grid; gap: 3px; }
  .device-fleet-empty { min-height: 118px; padding: 16px; grid-template-columns: 40px minmax(0, 1fr); gap: 12px; }
  .device-fleet-empty-icon { width: 40px; height: 40px; }
  .device-fleet-empty-action { min-height: 44px; grid-column: 1 / -1; justify-content: space-between; }
}

@media (prefers-reduced-motion: reduce) {
  .device-status-grid :deep(.device-card) { animation: device-card-fade 140ms ease both; }
  .focus-swap-enter-from,
  .focus-swap-leave-to { transform: none; }
  @keyframes device-card-fade { from { opacity: 0; } to { opacity: 1; } }
}
</style>
