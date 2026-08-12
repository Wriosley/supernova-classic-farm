<script setup lang="ts">
import { computed } from 'vue'
import {
  MailKind,
  type MailView,
} from '../gen/classicfarm/v1/ws/ws_pb'

const props = defineProps<{
  open: boolean
  mails: MailView[]
  nextPageToken: string
  filter: 'all' | 'public' | 'private' | 'gift'
  loading: boolean
  loadingMore: boolean
  claimingMailId: string | null
  error: string
  message: string
  itemName: (itemId: number) => string
}>()

const emit = defineEmits<{
  filter: [value: 'all' | 'public' | 'private' | 'gift']
  refresh: []
  loadMore: []
  openMail: [mail: MailView]
  claim: [mail: MailView]
}>()

const filtered = computed(() => {
  switch (props.filter) {
    case 'public':
      return props.mails.filter((mail) => mail.kind === MailKind.PUBLIC)
    case 'private':
      return props.mails.filter((mail) => mail.kind === MailKind.PRIVATE)
    case 'gift':
      return props.mails.filter((mail) => mail.kind === MailKind.GIFT)
    default:
      return props.mails
  }
})

function kindLabel(kind: MailKind): string {
  switch (kind) {
    case MailKind.PUBLIC:
      return '公开'
    case MailKind.PRIVATE:
      return '私人'
    case MailKind.GIFT:
      return '好友礼物'
    default:
      return '未知'
  }
}

function statusLabel(mail: MailView): string {
  if (hasReward(mail) && mail.claimed) {
    return '已领取'
  }
  if (mail.read) {
    return '已读'
  }
  return '未读'
}

function formatTime(ms: bigint): string {
  if (ms <= 0n) {
    return '-'
  }
  return new Date(Number(ms)).toLocaleString()
}

function hasReward(mail: MailView): boolean {
  return mail.attachments.length > 0 || mail.coinAmount > 0n
}

function canClaim(mail: MailView): boolean {
  return hasReward(mail) && !mail.claimed
}
</script>

<template>
  <div v-if="open" class="mailbox-body">
    <div class="mailbox-actions">
      <button type="button" class="ghost" :disabled="loading" @click="emit('refresh')">刷新</button>
    </div>

    <div class="mailbox-tabs" role="tablist">
      <button type="button" :class="{ selected: filter === 'all' }" @click="emit('filter', 'all')">
        全部
      </button>
      <button
        type="button"
        :class="{ selected: filter === 'public' }"
        @click="emit('filter', 'public')"
      >
        公开
      </button>
      <button
        type="button"
        :class="{ selected: filter === 'private' }"
        @click="emit('filter', 'private')"
      >
        私人
      </button>
      <button
        type="button"
        :class="{ selected: filter === 'gift' }"
        @click="emit('filter', 'gift')"
      >
        好友礼物
      </button>
    </div>

    <p v-if="error" class="mailbox-error">{{ error }}</p>
    <p v-else-if="message" class="mailbox-message">{{ message }}</p>
    <p v-if="loading" class="mailbox-hint">查询中…</p>

    <ul v-else-if="filtered.length" class="mailbox-list">
      <li v-for="mail in filtered" :key="mail.mailId" class="mailbox-item">
        <button type="button" class="mailbox-item__open" @click="emit('openMail', mail)">
          <strong class="mailbox-item__title">{{ mail.title || '(无标题)' }}</strong>
          <span class="mailbox-item__meta">
            {{ kindLabel(mail.kind) }} · {{ statusLabel(mail) }} · {{ formatTime(mail.createdAtMs) }}
          </span>
          <span class="mailbox-item__meta">
            寄信人：{{ mail.senderDisplayName || (mail.senderPlayerId ? `#${mail.senderPlayerId}` : '系统') }}
          </span>
          <span class="mailbox-item__meta">
            收信人：{{ mail.recipientPlayerId ? `#${mail.recipientPlayerId}` : '全体可见' }}
          </span>
          <p class="mailbox-item__content">{{ mail.content }}</p>
          <ul v-if="hasReward(mail)" class="mailbox-attachments">
            <li v-if="mail.coinAmount > 0n">金币 ×{{ mail.coinAmount.toString() }}</li>
            <li v-for="att in mail.attachments" :key="`${mail.mailId}-${att.itemId}`">
              {{ itemName(att.itemId) }} ×{{ att.quantity }}
            </li>
          </ul>
        </button>
        <button
          v-if="canClaim(mail)"
          type="button"
          class="mailbox-claim"
          :disabled="claimingMailId !== null"
          @click="emit('claim', mail)"
        >
          {{ claimingMailId === mail.mailId ? '领取中…' : '领取' }}
        </button>
      </li>
    </ul>
    <p v-else class="mailbox-hint">暂无邮件</p>

    <button
      v-if="nextPageToken"
      type="button"
      class="mailbox-more"
      :disabled="loadingMore || loading"
      @click="emit('loadMore')"
    >
      {{ loadingMore ? '加载中…' : '加载更多' }}
    </button>
  </div>
</template>

<style scoped>
.mailbox-body {
  display: grid;
  gap: 0.75rem;
  color: #1f2937;
}

.mailbox-actions {
  display: flex;
  justify-content: flex-end;
}

p {
  margin: 0;
}

.mailbox-tabs {
  display: grid;
  grid-template-columns: repeat(4, minmax(0, 1fr));
  gap: 0.35rem;
}

.mailbox-tabs button {
  min-height: 2.5rem;
  font-size: 0.85rem;
}

.mailbox-tabs button.selected {
  font-weight: 700;
}

.mailbox-list {
  list-style: none;
  margin: 0;
  padding: 0;
  display: grid;
  gap: 0.65rem;
}

.mailbox-item {
  display: grid;
  gap: 0.45rem;
  padding: 0.55rem 0.2rem;
  border-bottom: 1px solid color-mix(in srgb, currentColor 12%, transparent);
}

.mailbox-item__open {
  display: grid;
  gap: 0.25rem;
  text-align: left;
  border: none;
  background: transparent;
  color: inherit;
  cursor: pointer;
  padding: 0;
  min-height: 2.75rem;
}

.mailbox-item__title {
  overflow-wrap: anywhere;
}

.mailbox-item__meta {
  font-size: 0.8rem;
  color: #667085;
}

.mailbox-item__content {
  white-space: pre-wrap;
  overflow-wrap: anywhere;
  font-size: 0.9rem;
}

.mailbox-attachments {
  list-style: none;
  margin: 0;
  padding: 0;
  display: grid;
  gap: 0.2rem;
  max-height: 6rem;
  overflow: auto;
  font-size: 0.85rem;
}

.mailbox-claim,
.mailbox-more {
  min-height: 2.75rem;
}

.mailbox-error {
  color: #b42318;
}

.mailbox-message {
  color: #027a48;
}

.mailbox-hint {
  color: #667085;
}

button.ghost {
  border: none;
  background: transparent;
  text-decoration: underline;
  cursor: pointer;
  min-height: 2.5rem;
}
</style>
