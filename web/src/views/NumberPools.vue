<script setup lang="ts">
import { computed, reactive, ref } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import ResourceCollectionView, { type ResourceColumn, type ResourceRow } from '../components/resources/ResourceCollectionView.vue'
import ResourceEditorDrawer from '../components/resources/ResourceEditorDrawer.vue'
import { useResourceCollection } from '../composables/useResourceCollection'
import { adminResourcesService } from '../services/admin-resources'
import { toAppError } from '../services/http'
import { useAuthStore } from '../stores/auth'
import type {
  ManagedUserResource,
  NumberPoolMemberResource,
  NumberPoolResource,
  NumberPoolUserResource,
  PhoneNumberResource,
  SubscriptionProfileResource
} from '../types/admin-resources'
import {
  e164Error,
  poolDescriptionError,
  poolNameError,
  validatePoolMembers,
  validatePoolUsers
} from '../utils/resourceManagement'
import { displayStatus, displayValue, maskIdentifier, statusTone } from '../utils/resourcePresentation'
import { t } from '../i18n'

type PoolMemberDraft = Required<Pick<NumberPoolMemberResource, 'phone_number_id' | 'priority' | 'weight' | 'enabled'>>
type PoolUserDraft = Required<Pick<NumberPoolUserResource, 'user_id' | 'role' | 'enabled'>>

const auth = useAuthStore()
const poolsState = useResourceCollection(adminResourcesService.numberPools)
const numbersState = useResourceCollection(adminResourcesService.phoneNumbers)
const usersState = useResourceCollection(adminResourcesService.users)
const cardsState = useResourceCollection(adminResourcesService.cards)

const poolDrawerOpen = ref(false)
const numberDrawerOpen = ref(false)
const busy = ref(false)
const formError = ref('')
const selectedPoolID = ref('')
const selectedNumberID = ref('')

const poolDraft = reactive({
  name: '',
  description: '',
  enabled: true,
  members: [] as PoolMemberDraft[],
  users: [] as PoolUserDraft[]
})

const numberDraft = reactive({
  e164: '',
  display_number: '',
  label: '',
  subscription_profile_id: '',
  status: 'available'
})

const columns = computed<ResourceColumn[]>(() => [
  { key: 'members', label: t('resources.pools.members') },
  { key: 'available', label: t('resources.pools.availability') },
  { key: 'users', label: t('resources.pools.users') },
  { key: 'updated', label: t('resources.pools.updated'), compact: true }
])
const rows = computed<ResourceRow[]>(() => poolsState.items.value.map((pool, index) => {
  const status = pool.enabled === false
    ? t('resources.common.disabled')
    : displayStatus(pool.status, t('resources.common.pending'))
  return {
    id: displayValue(pool.id, `pool-${index}`),
    title: displayValue(pool.name, `${t('resources.pools.title')} ${index + 1}`),
    subtitle: displayValue(pool.description, t('resources.pools.subtitle')),
    status,
    tone: pool.enabled === false ? 'danger' : statusTone(pool.status),
    values: {
      members: t('resources.common.numberCount', { count: Number(pool.member_count ?? pool.members?.length ?? 0) }),
      available: t('resources.common.availableBusy', {
        available: Number(pool.available_count || 0),
        busy: Number(pool.occupied_count || 0)
      }),
      users: t('resources.common.personCount', { count: Number(pool.authorized_user_count ?? pool.users?.length ?? 0) }),
      updated: displayValue(pool.updated_at)
    }
  }
}))

const poolByID = computed(() => new Map(poolsState.items.value.map(pool => [String(pool.id || ''), pool])))
const numberByID = computed(() => new Map(numbersState.items.value.map(number => [number.id, number])))
const userByID = computed(() => new Map(usersState.items.value.map(user => [user.id, user])))
const profileOptions = computed(() => {
  const result: Array<{ profile: SubscriptionProfileResource; label: string }> = []
  for (const card of cardsState.items.value) {
    const cardName = card.device_name || card.device_id || card.active_number || card.id || card.card_id || '未命名卡'
    for (const profile of card.profiles || []) {
      const profileName = profile.name || profile.service_provider || `ICCID ${maskIdentifier(profile.iccid)}`
      result.push({ profile, label: `${cardName} · ${profileName}${profile.present ? '' : ' · 历史'}` })
    }
  }
  return result
})
const editingPool = computed(() => poolByID.value.get(selectedPoolID.value))
const editingNumber = computed(() => numberByID.value.get(selectedNumberID.value))

function numberLabel(number: PhoneNumberResource | undefined) {
  if (!number) return '未知号码'
  return [number.e164, number.label || number.display_number].filter(Boolean).join(' · ')
}

function userLabel(user: ManagedUserResource | undefined) {
  if (!user) return '未知用户'
  return `${user.display_name || user.username} · ${user.role === 'admin' ? '管理员' : '普通用户'}`
}

function openCreatePool() {
  selectedPoolID.value = ''
  Object.assign(poolDraft, { name: '', description: '', enabled: true, members: [], users: [] })
  formError.value = ''
  poolDrawerOpen.value = true
}

function openEditPool(pool: NumberPoolResource | undefined) {
  if (!pool) return
  selectedPoolID.value = String(pool.id || '')
  Object.assign(poolDraft, {
    name: String(pool.name || ''),
    description: String(pool.description || ''),
    enabled: pool.enabled !== false,
    members: (pool.members || []).map(member => ({
      phone_number_id: member.phone_number_id,
      priority: Number(member.priority || 0),
      weight: Number(member.weight || 1),
      enabled: member.enabled !== false
    })),
    users: (pool.users || []).map(grant => ({
      user_id: grant.user_id,
      role: String(grant.role || userByID.value.get(grant.user_id)?.role || 'member'),
      enabled: grant.enabled !== false
    }))
  })
  formError.value = ''
  poolDrawerOpen.value = true
}

function memberSelected(id: string) {
  return poolDraft.members.some(item => item.phone_number_id === id)
}

function setMemberSelected(number: PhoneNumberResource, value: unknown) {
  const selected = value === true
  const index = poolDraft.members.findIndex(item => item.phone_number_id === number.id)
  if (selected && index < 0) poolDraft.members.push({ phone_number_id: number.id, priority: 0, weight: 1, enabled: true })
  if (!selected && index >= 0) poolDraft.members.splice(index, 1)
}

function memberDraft(id: string) {
  return poolDraft.members.find(item => item.phone_number_id === id)
}

function userSelected(id: string) {
  return poolDraft.users.some(item => item.user_id === id)
}

function setUserSelected(user: ManagedUserResource, value: unknown) {
  const selected = value === true
  const index = poolDraft.users.findIndex(item => item.user_id === user.id)
  if (selected && index < 0) poolDraft.users.push({ user_id: user.id, role: user.role || 'member', enabled: true })
  if (!selected && index >= 0) poolDraft.users.splice(index, 1)
}

function userDraft(id: string) {
  return poolDraft.users.find(item => item.user_id === id)
}

function validatePoolDraft() {
  return poolNameError(poolDraft.name) ||
    poolDescriptionError(poolDraft.description) ||
    validatePoolMembers(poolDraft.members) ||
    validatePoolUsers(poolDraft.users)
}

async function savePool() {
  if (!auth.isAdmin || busy.value) return
  formError.value = validatePoolDraft()
  if (formError.value) return
  const existing = editingPool.value
  if (existing) {
    const disabling = existing.enabled !== false && !poolDraft.enabled
    try {
      await ElMessageBox.confirm(
        `${disabling ? '将停用该号码池。' : ''}将把「${existing.name || existing.id}」的成员替换为 ${poolDraft.members.length} 个号码，授权替换为 ${poolDraft.users.length} 个用户。`,
        `更新号码池：${existing.name || existing.id}`,
        { confirmButtonText: '确认替换', cancelButtonText: '取消', type: disabling ? 'warning' : 'info', closeOnClickModal: false }
      )
    } catch (error) {
      if (error !== 'cancel' && error !== 'close') ElMessage.error(toAppError(error).message)
      return
    }
  }
  busy.value = true
  formError.value = ''
  try {
    const pool = existing
      ? await adminResourcesService.updateNumberPool(String(existing.id), {
        name: poolDraft.name.trim(), description: poolDraft.description.trim(), enabled: poolDraft.enabled
      })
      : await adminResourcesService.createNumberPool({
        name: poolDraft.name.trim(), description: poolDraft.description.trim(), enabled: poolDraft.enabled
      })
    const poolID = String(pool.id || existing?.id || '')
    if (!poolID) throw new Error('服务端未返回号码池 ID')
    await adminResourcesService.replaceNumberPoolMembers(poolID, poolDraft.members.map(item => ({ ...item })))
    await adminResourcesService.replaceNumberPoolUsers(poolID, poolDraft.users.map(item => ({ ...item })))
    await refreshAll()
    poolDrawerOpen.value = false
    ElMessage.success(existing ? '号码池已更新' : '号码池已创建')
  } catch (error) {
    formError.value = toAppError(error).message
    ElMessage.error(formError.value)
    await refreshAll()
  } finally {
    busy.value = false
  }
}

function openCreateNumber() {
  selectedNumberID.value = ''
  Object.assign(numberDraft, {
    e164: '', display_number: '', label: '', subscription_profile_id: '', status: 'available'
  })
  formError.value = ''
  numberDrawerOpen.value = true
}

function openEditNumber(number: PhoneNumberResource) {
  selectedNumberID.value = number.id
  Object.assign(numberDraft, {
    e164: number.e164 || '',
    display_number: number.display_number || '',
    label: number.label || '',
    subscription_profile_id: number.subscription_profile_id || '',
    status: number.status || 'available'
  })
  formError.value = ''
  numberDrawerOpen.value = true
}

function validateNumberDraft() {
  const e164 = e164Error(numberDraft.e164)
  if (e164) return e164
  if ([...numberDraft.display_number.trim()].length > 64) return '显示号码不能超过 64 个字符'
  if ([...numberDraft.label.trim()].length > 128) return '标签不能超过 128 个字符'
  if (!['available', 'disabled', 'suspended'].includes(numberDraft.status)) return '请选择有效号码状态'
  return ''
}

async function saveNumber() {
  if (!auth.isAdmin || busy.value) return
  formError.value = validateNumberDraft()
  if (formError.value) return
  const existing = editingNumber.value
  if (existing) {
    try {
      await ElMessageBox.confirm(
        `将覆盖号码资源「${numberLabel(existing)}」的归属 Profile、标签与状态。`,
        `更新号码：${existing.e164}`,
        { confirmButtonText: '确认更新', cancelButtonText: '取消', type: 'warning', closeOnClickModal: false }
      )
    } catch (error) {
      if (error !== 'cancel' && error !== 'close') ElMessage.error(toAppError(error).message)
      return
    }
  }
  busy.value = true
  try {
    await adminResourcesService.upsertPhoneNumber({
      id: existing?.id,
      e164: numberDraft.e164.trim(),
      display_number: numberDraft.display_number.trim(),
      label: numberDraft.label.trim(),
      subscription_profile_id: numberDraft.subscription_profile_id || undefined,
      status: numberDraft.status
    })
    await refreshAll()
    numberDrawerOpen.value = false
    ElMessage.success(existing ? '号码资源已更新' : '号码资源已创建')
  } catch (error) {
    formError.value = toAppError(error).message
    ElMessage.error(formError.value)
  } finally {
    busy.value = false
  }
}

async function refreshAll() {
  await Promise.all([poolsState.load(), numbersState.load(), usersState.load(), cardsState.load()])
}
</script>

<template>
  <div class="pool-management-page">
    <ResourceCollectionView
      :title="t('resources.pools.title')"
      :subtitle="t('resources.pools.subtitle')"
      :count-label="t('resources.pools.count', { count: rows.length })"
      :rows="rows"
      :columns="columns"
      :loading="poolsState.loading.value"
      :error="poolsState.error.value"
      :last-ok-at="poolsState.lastOkAt.value"
      :empty-title="t('resources.pools.empty')"
      :empty-subtitle="t('resources.pools.emptyHint')"
      @refresh="refreshAll"
    >
      <template v-if="auth.isAdmin" #actions>
        <el-button @click="openCreateNumber">新增号码</el-button>
        <el-button type="primary" @click="openCreatePool">新建号码池</el-button>
      </template>
      <template v-if="auth.isAdmin" #row-actions="{ row }">
        <el-button type="primary" plain @click="openEditPool(poolByID.get(row.id))">编辑与授权</el-button>
      </template>
    </ResourceCollectionView>

    <section class="number-catalog ui-card" aria-label="号码资源">
      <header>
        <div>
          <span>PHONE NUMBER INVENTORY</span>
          <strong>号码资源</strong>
          <p>绑定 Profile 后可加入号码池；号码严格按 E.164 格式保存。</p>
        </div>
        <el-button v-if="auth.isAdmin" type="primary" plain @click="openCreateNumber">新增号码</el-button>
      </header>
      <p v-if="numbersState.error.value" class="catalog-error" role="alert">{{ numbersState.error.value.message }}</p>
      <div v-else-if="numbersState.loading.value && !numbersState.items.value.length" class="catalog-empty">正在读取号码资源……</div>
      <div v-else-if="!numbersState.items.value.length" class="catalog-empty">暂无号码资源，请先新增 E.164 号码。</div>
      <div v-else class="number-grid">
        <article v-for="number in numbersState.items.value" :key="number.id">
          <div>
            <strong>{{ number.e164 }}</strong>
            <small>{{ number.label || number.display_number || '未设标签' }}</small>
          </div>
          <el-tag :type="number.status === 'available' ? 'success' : 'warning'">{{ number.status || '状态待同步' }}</el-tag>
          <p>Profile：{{ number.subscription_profile_id ? maskIdentifier(number.subscription_profile_id) : '未绑定' }}</p>
          <el-button v-if="auth.isAdmin" @click="openEditNumber(number)">编辑号码</el-button>
        </article>
      </div>
    </section>
  </div>

  <ResourceEditorDrawer
    v-if="auth.isAdmin"
    v-model="poolDrawerOpen"
    :title="editingPool ? `编辑号码池 · ${editingPool.name}` : '新建号码池'"
    subtitle="保存时会用当前选择替换该池的全部成员和用户授权。"
    :busy="busy"
  >
    <p v-if="formError" class="resource-form-error" role="alert">{{ formError }}</p>
    <el-form class="resource-editor-form" label-position="top">
      <el-form-item label="号码池名称" required>
        <el-input v-model="poolDraft.name" maxlength="128" show-word-limit autocomplete="off" />
      </el-form-item>
      <el-form-item label="说明">
        <el-input v-model="poolDraft.description" type="textarea" :rows="3" maxlength="512" show-word-limit />
      </el-form-item>
      <el-form-item label="号码池状态">
        <el-switch v-model="poolDraft.enabled" inline-prompt active-text="启用" inactive-text="停用" />
      </el-form-item>
    </el-form>

    <section class="resource-editor-section">
      <header>
        <div><strong>号码成员</strong><p>已选 {{ poolDraft.members.length }} 个。优先级为非负整数，权重至少为 1。</p></div>
      </header>
      <p v-if="numbersState.error.value" class="resource-form-error">{{ numbersState.error.value.message }}</p>
      <div v-else-if="numbersState.items.value.length" class="resource-choice-list">
        <article v-for="number in numbersState.items.value" :key="number.id" class="resource-choice-row">
          <el-checkbox :model-value="memberSelected(number.id)" @change="setMemberSelected(number, $event)">
            {{ numberLabel(number) }}
          </el-checkbox>
          <div v-if="memberSelected(number.id)" class="resource-choice-options">
            <el-input-number v-model="memberDraft(number.id)!.priority" :min="0" :step="1" controls-position="right" />
            <el-input-number v-model="memberDraft(number.id)!.weight" :min="1" :step="1" controls-position="right" />
            <small>优先级</small><small>权重</small>
          </div>
        </article>
      </div>
      <p v-else class="resource-form-hint">暂无可选号码。请先保存号码资源。</p>
    </section>

    <section class="resource-editor-section">
      <header>
        <div><strong>用户授权</strong><p>已选 {{ poolDraft.users.length }} 人。普通用户只能使用被授权的号码池。</p></div>
      </header>
      <p v-if="usersState.error.value" class="resource-form-error">{{ usersState.error.value.message }}</p>
      <div v-else-if="usersState.items.value.length" class="resource-choice-list">
        <article v-for="user in usersState.items.value" :key="user.id" class="resource-choice-row">
          <el-checkbox :model-value="userSelected(user.id)" :disabled="user.disabled" @change="setUserSelected(user, $event)">
            {{ userLabel(user) }}{{ user.disabled ? ' · 已停用' : '' }}
          </el-checkbox>
          <el-select v-if="userSelected(user.id)" v-model="userDraft(user.id)!.role" aria-label="授权角色">
            <el-option label="普通用户" value="member" />
            <el-option label="管理员" value="admin" />
          </el-select>
        </article>
      </div>
      <p v-else class="resource-form-hint">暂无可授权用户。</p>
    </section>
    <template #actions>
      <el-button type="primary" :loading="busy" @click="savePool">保存号码池</el-button>
    </template>
  </ResourceEditorDrawer>

  <ResourceEditorDrawer
    v-if="auth.isAdmin"
    v-model="numberDrawerOpen"
    :title="editingNumber ? `编辑号码 · ${editingNumber.e164}` : '新增号码'"
    subtitle="请使用带 + 和国家码的 E.164 号码，例如 +14155550100。"
    :busy="busy"
  >
    <p v-if="formError" class="resource-form-error" role="alert">{{ formError }}</p>
    <el-form class="resource-editor-form" label-position="top">
      <el-form-item label="E.164 号码" required>
        <el-input v-model="numberDraft.e164" inputmode="tel" placeholder="+14155550100" autocomplete="off" />
      </el-form-item>
      <el-form-item label="显示号码">
        <el-input v-model="numberDraft.display_number" maxlength="64" placeholder="可选" />
      </el-form-item>
      <el-form-item label="标签">
        <el-input v-model="numberDraft.label" maxlength="128" placeholder="例如：美国客服线" />
      </el-form-item>
      <el-form-item label="归属 Profile">
        <el-select v-model="numberDraft.subscription_profile_id" clearable filterable placeholder="可稍后绑定">
          <el-option v-for="option in profileOptions" :key="option.profile.id" :label="option.label" :value="option.profile.id" />
        </el-select>
      </el-form-item>
      <el-form-item label="号码状态" required>
        <el-select v-model="numberDraft.status">
          <el-option label="可用" value="available" />
          <el-option label="已停用" value="disabled" />
          <el-option label="已暂停" value="suspended" />
        </el-select>
      </el-form-item>
    </el-form>
    <template #actions>
      <el-button type="primary" :loading="busy" @click="saveNumber">保存号码</el-button>
    </template>
  </ResourceEditorDrawer>
</template>

<style scoped>
.pool-management-page { width: 100%; max-width: 1500px; margin: 0 auto; display: grid; gap: 18px; }
.number-catalog { overflow: hidden; }
.number-catalog > header { min-height: 88px; padding: 17px 20px; display: flex; align-items: center; justify-content: space-between; gap: 16px; border-bottom: 1px solid var(--ui-border); }
.number-catalog > header > div { display: grid; gap: 3px; }
.number-catalog > header span { color: var(--ui-primary); font: 500 var(--ui-font-caption)/1.2 var(--ui-font-mono); letter-spacing: .08em; }
.number-catalog > header strong { color: var(--ui-text); font-size: 17px; }
.number-catalog > header p { margin: 0; color: var(--ui-text-muted); font-size: 12px; }
.number-grid { padding: 14px; display: grid; grid-template-columns: repeat(auto-fit, minmax(260px, 1fr)); gap: 10px; }
.number-grid article { min-width: 0; padding: 14px; display: grid; grid-template-columns: minmax(0, 1fr) auto; gap: 10px; align-items: start; border: 1px solid var(--ui-border); border-radius: var(--ui-radius-lg); background: var(--ui-surface-muted); }
.number-grid article > div { min-width: 0; display: grid; gap: 3px; }
.number-grid strong { overflow-wrap: anywhere; color: var(--ui-text); font: 650 14px/1.3 var(--ui-font-mono); }
.number-grid small, .number-grid p { overflow-wrap: anywhere; color: var(--ui-text-muted); font-size: 12px; }
.number-grid p { margin: 0; grid-column: 1 / -1; }
.number-grid .el-button { grid-column: 1 / -1; min-height: 40px; margin: 0; }
.catalog-empty, .catalog-error { margin: 0; padding: 28px 20px; text-align: center; color: var(--ui-text-muted); font-size: 13px; }
.catalog-error { color: var(--ui-danger); }
.resource-choice-options { grid-template-columns: repeat(2, minmax(0, 1fr)); }
.resource-choice-options small { text-align: center; }

@media (max-width: 900px) {
  .number-catalog > header { align-items: flex-start; }
}
@media (max-width: 600px) {
  .number-catalog > header { padding: 15px; display: grid; }
  .number-catalog > header .el-button { width: 100%; min-height: 44px; }
  .number-grid { padding: 10px; grid-template-columns: 1fr; }
}
</style>
