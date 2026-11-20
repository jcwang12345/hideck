<script setup lang="ts">
import { computed, reactive, ref } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import ResourceCollectionView, { type ResourceColumn, type ResourceRow } from '../components/resources/ResourceCollectionView.vue'
import ResourceEditorDrawer from '../components/resources/ResourceEditorDrawer.vue'
import { useResourceCollection } from '../composables/useResourceCollection'
import { adminResourcesService } from '../services/admin-resources'
import { toAppError } from '../services/http'
import { useAuthStore } from '../stores/auth'
import type { ManagedUserResource } from '../types/admin-resources'
import { passwordError, usernameError } from '../utils/resourceManagement'
import { displayDate, displayValue } from '../utils/resourcePresentation'
import { t } from '../i18n'

const auth = useAuthStore()
const state = useResourceCollection(adminResourcesService.users)
const drawerOpen = ref(false)
const selectedUserID = ref('')
const busy = ref(false)
const pendingUserID = ref('')
const formError = ref('')
const draft = reactive({ username: '', password: '', role: 'member' as 'admin' | 'member', disabled: false })

const columns = computed<ResourceColumn[]>(() => [
  { key: 'role', label: t('resources.users.role') },
  { key: 'pools', label: t('resources.users.pools') },
  { key: 'calls', label: t('resources.users.calls') },
  { key: 'login', label: t('resources.users.login'), compact: true }
])
const rows = computed<ResourceRow[]>(() => state.items.value.map((user, index) => ({
  id: displayValue(user.id, `user-${index}`),
  title: displayValue(user.display_name || user.username, `${t('resources.users.title')} ${index + 1}`),
  subtitle: `${user.display_name ? `@${user.username} · ` : ''}ID ${displayValue(user.id)}${auth.user?.id === user.id ? ' · 当前账号' : ''}`,
  status: user.disabled ? t('resources.status.disabled') : t('resources.status.ready'),
  tone: user.disabled ? 'danger' : 'success',
  values: {
    role: user.role === 'admin' ? t('resources.common.administrator') : t('resources.common.member'),
    pools: t('resources.common.poolCount', { count: Number(user.pool_count || 0) }),
    calls: t('resources.common.callCount', { count: Number(user.active_call_count || 0) }),
    login: displayDate(user.last_login_at)
  }
})))

const userByID = computed(() => new Map(state.items.value.map(user => [user.id, user])))
const editingUser = computed(() => userByID.value.get(selectedUserID.value))

function displayName(user: ManagedUserResource | undefined) {
  return user?.display_name || user?.username || user?.id || '未知用户'
}

function openCreate() {
  selectedUserID.value = ''
  Object.assign(draft, { username: '', password: '', role: 'member', disabled: false })
  formError.value = ''
  drawerOpen.value = true
}

function openEdit(user: ManagedUserResource | undefined) {
  if (!user) return
  selectedUserID.value = user.id
  Object.assign(draft, {
    username: user.username,
    password: '',
    role: user.role === 'admin' ? 'admin' : 'member',
    disabled: user.disabled === true
  })
  formError.value = ''
  drawerOpen.value = true
}

function validateDraft() {
  const username = usernameError(draft.username)
  if (username) return username
  if (!['admin', 'member'].includes(draft.role)) return '请选择有效角色'
  if (!editingUser.value) return passwordError(draft.password, draft.username)
  return ''
}

async function saveUser() {
  if (!auth.isAdmin || busy.value) return
  formError.value = validateDraft()
  if (formError.value) return
  const existing = editingUser.value
  if (existing) {
    const disabling = existing.disabled !== true && draft.disabled
    const demoting = existing.role === 'admin' && draft.role === 'member'
    const current = auth.user?.id === existing.id
    const impact = [
      disabling ? '停用后该账号无法登录' : '',
      demoting ? '该账号将失去管理权限' : '',
      current && (disabling || demoting) ? '这是你当前使用的账号' : ''
    ].filter(Boolean).join('；')
    try {
      await ElMessageBox.confirm(
        `将更新「${displayName(existing)}」为${draft.role === 'admin' ? '管理员' : '普通用户'}、${draft.disabled ? '已停用' : '可登录'}。${impact ? `${impact}。` : ''}`,
        `更新用户：${displayName(existing)}`,
        { confirmButtonText: '确认更新', cancelButtonText: '取消', type: disabling || demoting ? 'warning' : 'info', closeOnClickModal: false }
      )
    } catch (error) {
      if (error !== 'cancel' && error !== 'close') ElMessage.error(toAppError(error).message)
      return
    }
  }
  busy.value = true
  formError.value = ''
  try {
    if (existing) {
      await adminResourcesService.updateUser(existing.id, {
        username: draft.username.trim(), role: draft.role, disabled: draft.disabled
      })
    } else {
      await adminResourcesService.createUser({
        username: draft.username.trim(), password: draft.password, role: draft.role
      })
    }
    await state.load()
    drawerOpen.value = false
    ElMessage.success(existing ? '用户已更新' : '用户已创建')
  } catch (error) {
    formError.value = toAppError(error).message
    ElMessage.error(formError.value)
  } finally {
    busy.value = false
  }
}

async function resetPassword(user: ManagedUserResource | undefined) {
  if (!user || pendingUserID.value) return
  try {
    const { value } = await ElMessageBox.prompt(
      `输入「${displayName(user)}」的新密码。保存后旧密码失效，该用户需使用新密码登录。`,
      `重置密码：${displayName(user)}`,
      {
        confirmButtonText: '确认重置', cancelButtonText: '取消', type: 'warning',
        inputType: 'password', inputPlaceholder: '至少 8 位；少于 12 位时须包含两类字符',
        inputValidator: value => passwordError(String(value || ''), user.username) || true,
        closeOnClickModal: false
      }
    )
    pendingUserID.value = user.id
    await adminResourcesService.resetUserPassword(user.id, String(value || ''))
    ElMessage.success(`已重置 ${displayName(user)} 的密码`)
  } catch (error) {
    if (error !== 'cancel' && error !== 'close') ElMessage.error(toAppError(error).message)
  } finally {
    pendingUserID.value = ''
  }
}

async function revokeSessions(user: ManagedUserResource | undefined) {
  if (!user || pendingUserID.value) return
  try {
    await ElMessageBox.confirm(
      `将撤销「${displayName(user)}」的全部登录会话，该用户需要重新登录。`,
      `撤销会话：${displayName(user)}`,
      { confirmButtonText: '确认撤销', cancelButtonText: '取消', type: 'warning', closeOnClickModal: false }
    )
    pendingUserID.value = user.id
    await adminResourcesService.revokeUserSessions(user.id)
    ElMessage.success(`已撤销 ${displayName(user)} 的登录会话`)
  } catch (error) {
    if (error !== 'cancel' && error !== 'close') ElMessage.error(toAppError(error).message)
  } finally {
    pendingUserID.value = ''
  }
}
</script>

<template>
  <ResourceCollectionView
    :title="t('resources.users.title')"
    :subtitle="t('resources.users.subtitle')"
    :count-label="t('resources.users.count', { count: rows.length })"
    :rows="rows"
    :columns="columns"
    :loading="state.loading.value"
    :error="state.error.value"
    :last-ok-at="state.lastOkAt.value"
    :empty-title="t('resources.users.empty')"
    :empty-subtitle="t('resources.users.emptyHint')"
    @refresh="state.load"
  >
    <template v-if="auth.isAdmin" #actions>
      <el-button type="primary" @click="openCreate">新建用户</el-button>
    </template>
    <template v-if="auth.isAdmin" #row-actions="{ row }">
      <el-button @click="openEdit(userByID.get(row.id))">编辑</el-button>
      <el-button :loading="pendingUserID === row.id" @click="resetPassword(userByID.get(row.id))">重置密码</el-button>
      <el-button type="danger" plain :loading="pendingUserID === row.id" @click="revokeSessions(userByID.get(row.id))">撤销会话</el-button>
    </template>
  </ResourceCollectionView>

  <ResourceEditorDrawer
    v-if="auth.isAdmin"
    v-model="drawerOpen"
    :title="editingUser ? `编辑用户 · ${displayName(editingUser)}` : '新建用户'"
    :subtitle="editingUser ? '角色或停用状态变更会立即影响该账号的下次请求。' : '创建后可在号码池中授权普通用户。'"
    :busy="busy"
  >
    <p v-if="formError" class="resource-form-error" role="alert">{{ formError }}</p>
    <el-form class="resource-editor-form" label-position="top">
      <el-form-item label="用户名" required>
        <el-input v-model="draft.username" maxlength="64" autocomplete="off" />
      </el-form-item>
      <el-form-item v-if="!editingUser" label="初始密码" required>
        <el-input v-model="draft.password" type="password" show-password autocomplete="new-password" />
        <p class="resource-form-hint">至少 8 位；少于 12 位时须包含至少两类字符，且不能与用户名相同。</p>
      </el-form-item>
      <el-form-item label="角色" required>
        <el-radio-group v-model="draft.role">
          <el-radio-button value="member">普通用户</el-radio-button>
          <el-radio-button value="admin">管理员</el-radio-button>
        </el-radio-group>
      </el-form-item>
      <el-form-item v-if="editingUser" label="账号状态">
        <el-radio-group v-model="draft.disabled">
          <el-radio-button :value="false">可登录</el-radio-button>
          <el-radio-button :value="true">已停用</el-radio-button>
        </el-radio-group>
      </el-form-item>
    </el-form>

    <section v-if="editingUser" class="resource-editor-section">
      <header><div><strong>账号安全操作</strong><p>密码不会在界面中显示或回显。</p></div></header>
      <div class="user-security-actions">
        <el-button :loading="pendingUserID === editingUser.id" @click="resetPassword(editingUser)">重置密码</el-button>
        <el-button type="danger" plain :loading="pendingUserID === editingUser.id" @click="revokeSessions(editingUser)">撤销全部会话</el-button>
      </div>
    </section>
    <template #actions>
      <el-button type="primary" :loading="busy" @click="saveUser">保存用户</el-button>
    </template>
  </ResourceEditorDrawer>
</template>

<style scoped>
.user-security-actions { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); gap: 10px; }
.user-security-actions .el-button { min-height: 44px; margin: 0; }
@media (max-width: 480px) {
  .user-security-actions { grid-template-columns: 1fr; }
}
</style>
