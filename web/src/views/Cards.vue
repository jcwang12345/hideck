<script setup lang="ts">
import { computed, ref } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import ResourceCollectionView, { type ResourceColumn, type ResourceRow } from '../components/resources/ResourceCollectionView.vue'
import ResourceEditorDrawer from '../components/resources/ResourceEditorDrawer.vue'
import { useResourceCollection } from '../composables/useResourceCollection'
import { adminResourcesService } from '../services/admin-resources'
import { toAppError } from '../services/http'
import { useAuthStore } from '../stores/auth'
import type { CardResource, SubscriptionProfileResource } from '../types/admin-resources'
import { cardIsTransitioning, cardTypePresentation } from '../utils/resourceManagement'
import { displayStatus, displayValue, maskIdentifier, statusTone } from '../utils/resourcePresentation'
import { t } from '../i18n'

const auth = useAuthStore()
const state = useResourceCollection(adminResourcesService.cards)
const pending = ref<string[]>([])
const selectedCardID = ref('')
const profileDrawerOpen = ref(false)
const mutationError = ref('')

const selectedCard = computed(() => state.items.value.find(card => cardKey(card) === selectedCardID.value))
const cardByID = computed(() => new Map(state.items.value.map(card => [cardKey(card), card])))
const columns = computed<ResourceColumn[]>(() => [
  { key: 'device', label: t('resources.cards.device') },
  { key: 'profile', label: t('resources.cards.profile') },
  { key: 'number', label: t('resources.cards.number') },
  { key: 'today', label: t('resources.cards.today') }
])
const rows = computed<ResourceRow[]>(() => state.items.value.map((card, index) => {
  const id = cardKey(card) || `card-${index}`
  const status = displayStatus(card.status, t('resources.common.pending'))
  const type = cardTypePresentation(card)
  return {
    id,
    title: displayValue(card.active_number || card.active_profile_name || card.device_name, `${t('resources.cards.title')} ${index + 1}`),
    subtitle: `ICCID ${maskIdentifier(card.iccid)} · ${type.label} · ${type.detail}`,
    status,
    tone: statusTone(card.status),
    values: {
      device: displayValue(card.device_name || card.device_id, t('resources.common.unbound')),
      profile: displayValue(card.active_profile_name || activeProfileLabel(card), t('resources.common.inactive')),
      number: displayValue(card.active_number),
      today: t('resources.common.callsAndRejects', {
        calls: Number(card.daily_calls || 0),
        rejects: Number(card.daily_rejections || 0)
      })
    }
  }
}))

function cardKey(card: CardResource | undefined) {
  return String(card?.id || card?.card_id || '').trim()
}

function cardLabel(card: CardResource | undefined) {
  return String(card?.active_number || card?.device_name || card?.device_id || cardKey(card) || '卡资源')
}

function activeProfileLabel(card: CardResource | undefined) {
  return profileLabel(card?.profiles?.find(item => item.active))
}

function profileLabel(profile: SubscriptionProfileResource | undefined) {
  if (!profile) return ''
  return String(profile.name || profile.service_provider || (profile.iccid ? `ICCID ${maskIdentifier(profile.iccid)}` : profile.id))
}

function isPending(key: string) {
  return pending.value.includes(key)
}

function cardActionLocked(card: CardResource | undefined) {
  return !card || cardIsTransitioning(card) || isPending(cardKey(card))
}

async function mutateCard(card: CardResource, task: () => Promise<void>, message: string) {
  const id = cardKey(card)
  if (!id || cardActionLocked(card)) return
  pending.value = [...pending.value, id]
  mutationError.value = ''
  try {
    await task()
    ElMessage.success(message)
    await state.load()
  } catch (error) {
    mutationError.value = toAppError(error).message
    ElMessage.error(mutationError.value)
  } finally {
    pending.value = pending.value.filter(value => value !== id)
  }
}

async function disableCard(card: CardResource | undefined) {
  if (!card || cardActionLocked(card)) return
  const label = cardLabel(card)
  try {
    const { value } = await ElMessageBox.prompt(
      `停用后「${label}」今天不再参与号码池调度。可填写原因，最多 256 个字符。`,
      `停用 ${label}`,
      {
        confirmButtonText: '确认停用', cancelButtonText: '取消', type: 'warning',
        inputPlaceholder: '例如：连续拒接，需要人工检查',
        inputValidator: value => [...String(value || '').trim()].length <= 256 || '原因不能超过 256 个字符'
      }
    )
    await mutateCard(card, () => adminResourcesService.disableCard(cardKey(card), String(value || '').trim()), `已停用 ${label}`)
  } catch (error) {
    if (error !== 'cancel' && error !== 'close') ElMessage.error(toAppError(error).message)
  }
}

async function enableCard(card: CardResource | undefined) {
  if (!card || cardActionLocked(card)) return
  const label = cardLabel(card)
  try {
    await ElMessageBox.confirm(
      `将恢复「${label}」今天参与号码池调度。`,
      `恢复 ${label}`,
      { confirmButtonText: '确认恢复', cancelButtonText: '取消', type: 'warning' }
    )
    await mutateCard(card, () => adminResourcesService.enableCard(cardKey(card)), `已恢复 ${label}`)
  } catch (error) {
    if (error !== 'cancel' && error !== 'close') ElMessage.error(toAppError(error).message)
  }
}

function openProfiles(card: CardResource | undefined) {
  if (!card) return
  selectedCardID.value = cardKey(card)
  mutationError.value = ''
  profileDrawerOpen.value = true
}

async function activateProfile(card: CardResource | undefined, profile: SubscriptionProfileResource) {
  if (!card || profile.active || !profile.present || !card.can_switch_profile || cardActionLocked(card)) return
  const cardName = cardLabel(card)
  const profileName = profileLabel(profile)
  try {
    await ElMessageBox.confirm(
      `将把「${cardName}」切换到「${profileName}」。切换期间该卡不能发起通话，完成后还需等待 IMS 注册。`,
      `切换 Profile：${profileName}`,
      { confirmButtonText: '确认切换', cancelButtonText: '取消', type: 'warning', closeOnClickModal: false }
    )
    await mutateCard(
      card,
      () => adminResourcesService.activateCardProfile(cardKey(card), profile.id),
      `已提交切换：${profileName}`
    )
  } catch (error) {
    if (error !== 'cancel' && error !== 'close') ElMessage.error(toAppError(error).message)
  }
}
</script>

<template>
  <ResourceCollectionView
    :title="t('resources.cards.title')"
    :subtitle="t('resources.cards.subtitle')"
    :count-label="t('resources.cards.count', { count: rows.length })"
    :rows="rows"
    :columns="columns"
    :loading="state.loading.value"
    :error="state.error.value"
    :last-ok-at="state.lastOkAt.value"
    :empty-title="t('resources.cards.empty')"
    :empty-subtitle="t('resources.cards.emptyHint')"
    @refresh="state.load"
  >
    <template v-if="auth.isAdmin" #row-actions="{ row }">
      <el-button @click="openProfiles(cardByID.get(row.id))">Profile</el-button>
      <el-button
        v-if="cardByID.get(row.id)?.status === 'disabled'"
        type="success"
        plain
        :loading="isPending(row.id)"
        :disabled="cardActionLocked(cardByID.get(row.id))"
        @click="enableCard(cardByID.get(row.id))"
      >恢复</el-button>
      <el-button
        v-else
        type="danger"
        plain
        :loading="isPending(row.id)"
        :disabled="cardActionLocked(cardByID.get(row.id))"
        @click="disableCard(cardByID.get(row.id))"
      >停用</el-button>
    </template>
  </ResourceCollectionView>

  <ResourceEditorDrawer
    v-if="auth.isAdmin"
    v-model="profileDrawerOpen"
    :title="`Profile · ${cardLabel(selectedCard)}`"
    :subtitle="cardTypePresentation(selectedCard || {}).detail"
    :busy="selectedCard ? isPending(cardKey(selectedCard)) : false"
  >
    <p v-if="mutationError" class="resource-form-error" role="alert">{{ mutationError }}</p>
    <section class="resource-editor-section">
      <header>
        <div>
          <strong>卡型证据</strong>
          <p>{{ cardTypePresentation(selectedCard || {}).label }} · {{ cardTypePresentation(selectedCard || {}).detail }}</p>
        </div>
      </header>
      <p class="resource-form-hint">EID：{{ selectedCard?.eid || '未发现' }}</p>
    </section>

    <section class="resource-editor-section">
      <header>
        <div>
          <strong>已发现 Profile</strong>
          <p>仅可切换当前仍在卡上的 Profile；切换中或 IMS 注册中会锁定重复操作。</p>
        </div>
      </header>
      <div v-if="selectedCard?.profiles?.length" class="resource-choice-list">
        <article v-for="profile in selectedCard.profiles" :key="profile.id" class="resource-choice-row">
          <div class="resource-choice-row-main">
            <div>
              <strong>{{ profileLabel(profile) }}</strong>
              <small>ICCID {{ maskIdentifier(profile.iccid) }} · {{ profile.present ? '当前存在' : '历史记录' }}</small>
            </div>
            <el-tag :type="profile.active ? 'success' : profile.present ? 'info' : 'warning'">
              {{ profile.active ? '当前使用' : profile.present ? '可切换' : '不在卡上' }}
            </el-tag>
          </div>
          <el-button
            v-if="!profile.active"
            type="primary"
            plain
            :loading="selectedCard ? isPending(cardKey(selectedCard)) : false"
            :disabled="!profile.present || !selectedCard?.can_switch_profile || cardActionLocked(selectedCard)"
            @click="activateProfile(selectedCard, profile)"
          >切换到此 Profile</el-button>
        </article>
      </div>
      <p v-else class="resource-form-hint">尚未发现 Profile。</p>
    </section>
  </ResourceEditorDrawer>
</template>
