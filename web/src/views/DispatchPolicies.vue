<script setup lang="ts">
import { computed, reactive, ref } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import ResourceCollectionView, { type ResourceColumn, type ResourceRow } from '../components/resources/ResourceCollectionView.vue'
import ResourceEditorDrawer from '../components/resources/ResourceEditorDrawer.vue'
import { useResourceCollection } from '../composables/useResourceCollection'
import { adminResourcesService } from '../services/admin-resources'
import { toAppError } from '../services/http'
import { useAuthStore } from '../stores/auth'
import type { DispatchPolicyResource } from '../types/admin-resources'
import {
  DEFAULT_DISABLE_SCOPE,
  DEFAULT_POLICY_TIMEZONE,
  DEFAULT_REJECTION_THRESHOLD,
  DEFAULT_RESTORE_TIME,
  dispatchPolicyError
} from '../utils/resourceManagement'
import { displayStatus, displayValue, statusTone } from '../utils/resourcePresentation'
import { t } from '../i18n'

const DEFAULT_STRATEGY = 'active_registered_least_used'
const auth = useAuthStore()
const state = useResourceCollection(adminResourcesService.dispatchPolicies)
const poolsState = useResourceCollection(adminResourcesService.numberPools)
const drawerOpen = ref(false)
const selectedPolicyID = ref('')
const busy = ref(false)
const formError = ref('')

const draft = reactive({
  name: '',
  number_pool_id: '',
  strategy: DEFAULT_STRATEGY,
  priority: 0,
  enabled: true,
  rejection_threshold: DEFAULT_REJECTION_THRESHOLD,
  disable_scope: DEFAULT_DISABLE_SCOPE,
  timezone: DEFAULT_POLICY_TIMEZONE,
  restore_time: DEFAULT_RESTORE_TIME
})

const columns = computed<ResourceColumn[]>(() => [
  { key: 'pool', label: t('resources.policies.pool') },
  { key: 'threshold', label: t('resources.policies.threshold') },
  { key: 'scope', label: t('resources.policies.scope') },
  { key: 'schedule', label: t('resources.policies.schedule') }
])
const rows = computed<ResourceRow[]>(() => state.items.value.map((policy, index) => {
  const current = policy.status
    ? { status: displayStatus(policy.status, t('resources.common.pending')), tone: statusTone(policy.status) }
    : policy.enabled === undefined
      ? { status: t('resources.common.unconfigured'), tone: 'muted' as const }
      : policy.enabled
        ? { status: t('resources.common.enabled'), tone: 'success' as const }
        : { status: t('resources.common.disabled'), tone: 'danger' as const }
  return {
    id: displayValue(policy.id, `policy-${index}`),
    title: displayValue(policy.name, `${t('resources.policies.title')} ${index + 1}`),
    subtitle: t('resources.common.policySubtitle'),
    status: current.status,
    tone: current.tone,
    values: {
      pool: displayValue(policy.pool_name, poolName(policy.number_pool_id)),
      threshold: t('resources.common.times', { count: Number(policy.rejection_threshold || DEFAULT_REJECTION_THRESHOLD) }),
      scope: policy.disable_scope === DEFAULT_DISABLE_SCOPE ? '整张卡' : displayValue(policy.disable_scope),
      schedule: `${displayValue(policy.timezone, DEFAULT_POLICY_TIMEZONE)} · 次日 ${displayValue(policy.restore_time, DEFAULT_RESTORE_TIME)}`
    }
  }
}))

const policyByID = computed(() => new Map(state.items.value.map(policy => [String(policy.id || ''), policy])))
const editingPolicy = computed(() => policyByID.value.get(selectedPolicyID.value))

function poolName(id: string | undefined) {
  return poolsState.items.value.find(pool => pool.id === id)?.name || '未绑定号码池'
}

function openCreate() {
  selectedPolicyID.value = ''
  Object.assign(draft, {
    name: '',
    number_pool_id: String(poolsState.items.value.find(pool => pool.enabled !== false)?.id || ''),
    strategy: DEFAULT_STRATEGY,
    priority: 0,
    enabled: true,
    rejection_threshold: DEFAULT_REJECTION_THRESHOLD,
    disable_scope: DEFAULT_DISABLE_SCOPE,
    timezone: DEFAULT_POLICY_TIMEZONE,
    restore_time: DEFAULT_RESTORE_TIME
  })
  formError.value = ''
  drawerOpen.value = true
}

function openEdit(policy: DispatchPolicyResource | undefined) {
  if (!policy) return
  selectedPolicyID.value = String(policy.id || '')
  Object.assign(draft, {
    name: String(policy.name || ''),
    number_pool_id: String(policy.number_pool_id || ''),
    strategy: String(policy.strategy || DEFAULT_STRATEGY),
    priority: Number(policy.priority || 0),
    enabled: policy.enabled !== false,
    rejection_threshold: Number(policy.rejection_threshold || DEFAULT_REJECTION_THRESHOLD),
    disable_scope: DEFAULT_DISABLE_SCOPE,
    timezone: DEFAULT_POLICY_TIMEZONE,
    restore_time: DEFAULT_RESTORE_TIME
  })
  formError.value = ''
  drawerOpen.value = true
}

function validateDraft() {
  const error = dispatchPolicyError(draft)
  if (error) return error
  if ([...draft.name.trim()].length > 128) return '策略名称不能超过 128 个字符'
  if (!Number.isInteger(Number(draft.priority)) || Number(draft.priority) < 0) return '策略优先级必须是大于等于 0 的整数'
  if (draft.strategy !== DEFAULT_STRATEGY) return '请选择受支持的调度策略'
  if (draft.disable_scope !== DEFAULT_DISABLE_SCOPE || draft.timezone !== DEFAULT_POLICY_TIMEZONE || draft.restore_time !== DEFAULT_RESTORE_TIME) {
    return '当前版本仅支持整卡停用、Asia/Shanghai 统计和次日 00:00 恢复'
  }
  return ''
}

async function savePolicy() {
  if (!auth.isAdmin || busy.value) return
  formError.value = validateDraft()
  if (formError.value) return
  const existing = editingPolicy.value
  if (existing) {
    try {
      const disabling = existing.enabled !== false && !draft.enabled
      await ElMessageBox.confirm(
        `${disabling ? '将停用该策略。' : ''}将把「${existing.name || existing.id}」设为拒接 ${draft.rejection_threshold} 次后停用整张卡，并在 Asia/Shanghai 次日 00:00 恢复。`,
        `更新策略：${existing.name || existing.id}`,
        { confirmButtonText: '确认更新', cancelButtonText: '取消', type: disabling ? 'warning' : 'info', closeOnClickModal: false }
      )
    } catch (error) {
      if (error !== 'cancel' && error !== 'close') ElMessage.error(toAppError(error).message)
      return
    }
  }
  busy.value = true
  formError.value = ''
  try {
    await adminResourcesService.upsertDispatchPolicy({
      id: existing?.id,
      number_pool_id: draft.number_pool_id,
      name: draft.name.trim(),
      strategy: draft.strategy,
      priority: Number(draft.priority),
      enabled: draft.enabled,
      config: {
        rejection_threshold: Number(draft.rejection_threshold),
        disable_scope: DEFAULT_DISABLE_SCOPE,
        timezone: DEFAULT_POLICY_TIMEZONE,
        restore_time: DEFAULT_RESTORE_TIME
      }
    })
    await Promise.all([state.load(), poolsState.load()])
    drawerOpen.value = false
    ElMessage.success(existing ? '调度策略已更新' : '调度策略已创建')
  } catch (error) {
    formError.value = toAppError(error).message
    ElMessage.error(formError.value)
  } finally {
    busy.value = false
  }
}

async function refreshAll() {
  await Promise.all([state.load(), poolsState.load()])
}
</script>

<template>
  <ResourceCollectionView
    :title="t('resources.policies.title')"
    :subtitle="t('resources.policies.subtitle')"
    :count-label="t('resources.policies.count', { count: rows.length })"
    :rows="rows"
    :columns="columns"
    :loading="state.loading.value"
    :error="state.error.value"
    :last-ok-at="state.lastOkAt.value"
    :empty-title="t('resources.policies.empty')"
    :empty-subtitle="t('resources.policies.emptyHint')"
    @refresh="refreshAll"
  >
    <template v-if="auth.isAdmin" #actions>
      <el-button type="primary" @click="openCreate">新建策略</el-button>
    </template>
    <template v-if="auth.isAdmin" #row-actions="{ row }">
      <el-button type="primary" plain @click="openEdit(policyByID.get(row.id))">编辑策略</el-button>
    </template>
  </ResourceCollectionView>

  <ResourceEditorDrawer
    v-if="auth.isAdmin"
    v-model="drawerOpen"
    :title="editingPolicy ? `编辑策略 · ${editingPolicy.name}` : '新建调度策略'"
    subtitle="拒接达阈值后停用整张卡，并在 Asia/Shanghai 次日 00:00 自动恢复。"
    :busy="busy"
  >
    <p v-if="formError" class="resource-form-error" role="alert">{{ formError }}</p>
    <el-form class="resource-editor-form" label-position="top">
      <el-form-item label="策略名称" required>
        <el-input v-model="draft.name" maxlength="128" show-word-limit autocomplete="off" />
      </el-form-item>
      <el-form-item label="适用号码池" required>
        <el-select v-model="draft.number_pool_id" placeholder="选择号码池">
          <el-option v-for="pool in poolsState.items.value" :key="pool.id" :label="pool.name" :value="String(pool.id || '')" :disabled="pool.enabled === false" />
        </el-select>
      </el-form-item>
      <el-form-item label="线路选择策略">
        <el-select v-model="draft.strategy">
          <el-option label="已注册、可用且使用最少" :value="DEFAULT_STRATEGY" />
        </el-select>
      </el-form-item>
      <div class="policy-number-grid">
        <el-form-item label="策略优先级">
          <el-input-number v-model="draft.priority" :min="0" :step="1" controls-position="right" />
        </el-form-item>
        <el-form-item label="默认拒接阈值" required>
          <el-input-number v-model="draft.rejection_threshold" :min="1" :step="1" controls-position="right" />
        </el-form-item>
      </div>
      <el-form-item label="策略状态">
        <el-switch v-model="draft.enabled" inline-prompt active-text="启用" inactive-text="停用" />
      </el-form-item>
    </el-form>

    <section class="resource-editor-section policy-invariants">
      <header><div><strong>固定保护规则</strong><p>避免单个 Profile 反复切换，也避免跨时区统计造成误停用。</p></div></header>
      <dl>
        <div><dt>停用范围</dt><dd>整张卡</dd></div>
        <div><dt>统计时区</dt><dd>Asia/Shanghai</dd></div>
        <div><dt>恢复时间</dt><dd>次日 00:00</dd></div>
      </dl>
    </section>
    <template #actions>
      <el-button type="primary" :loading="busy" @click="savePolicy">保存策略</el-button>
    </template>
  </ResourceEditorDrawer>
</template>

<style scoped>
.policy-number-grid { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); gap: 12px; }
.policy-invariants dl { margin: 0; display: grid; gap: 8px; }
.policy-invariants dl > div { padding: 10px 11px; display: flex; align-items: center; justify-content: space-between; gap: 16px; border: 1px solid var(--ui-border); border-radius: var(--ui-radius-md); background: var(--ui-surface); }
.policy-invariants dt { color: var(--ui-text-muted); font-size: 12px; }
.policy-invariants dd { margin: 0; color: var(--ui-text); font: 650 12px/1.4 var(--ui-font-mono); }
@media (max-width: 520px) {
  .policy-number-grid { grid-template-columns: 1fr; }
}
</style>
