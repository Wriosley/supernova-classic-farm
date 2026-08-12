<script setup lang="ts">
import { ref } from 'vue'
import type { FriendView } from '../gen/classicfarm/v1/ws/ws_pb'

defineProps<{
  friends: FriendView[]
  connected: boolean
  busy: boolean
  error: string
  generatedCode: string
  shareUrl: string
  redeemBusy: boolean
  redeemMessage: string
  redeemError: string
  visitOwnerId?: bigint
  visitBusy: boolean
  hasFarmRedDot: (ownerId: bigint) => boolean
}>()

const emit = defineEmits<{
  refresh: []
  generate: []
  redeem: [code: string]
  enter: [ownerId: bigint]
  gift: [friend: FriendView]
}>()

const redeemInput = ref('')
const copyMessage = ref('')

function submitRedeem(): void {
  const code = redeemInput.value.trim()
  if (!code) {
    return
  }
  emit('redeem', code)
  redeemInput.value = ''
}

async function copyShareUrl(url: string): Promise<void> {
  if (!url) {
    return
  }
  try {
    await navigator.clipboard.writeText(url)
    copyMessage.value = '分享链接已复制'
  } catch {
    copyMessage.value = '复制失败，请手动选中链接'
  }
}
</script>

<template>
  <div class="friends-panel">
    <div class="friends-panel__code">
      <div class="friends-panel__row">
        <button type="button" :disabled="!connected || busy" @click="emit('generate')">
          生成好友码
        </button>
        <button type="button" :disabled="!connected || busy" @click="emit('refresh')">
          刷新列表
        </button>
      </div>
      <p v-if="generatedCode" class="generated-code">好友码：{{ generatedCode }}</p>
      <p v-if="shareUrl" class="share-url">
        <span>{{ shareUrl }}</span>
        <button type="button" @click="copyShareUrl(shareUrl)">复制链接</button>
      </p>
      <p v-if="copyMessage" class="success-banner">{{ copyMessage }}</p>

      <form class="redeem-form" @submit.prevent="submitRedeem">
        <input
          v-model="redeemInput"
          placeholder="输入好友码兑换"
          maxlength="32"
          :disabled="!connected"
        />
        <button type="submit" :disabled="!connected || !redeemInput || redeemBusy">兑换</button>
      </form>

      <p v-if="redeemMessage" class="success-banner">{{ redeemMessage }}</p>
      <p v-if="redeemError" class="tool-feedback">{{ redeemError }}</p>
      <p v-if="error" class="tool-feedback">{{ error }}</p>
    </div>

    <h3>好友列表（{{ friends.length }}）</h3>
    <ul v-if="friends.length" class="friends-list">
      <li v-for="friend in friends" :key="friend.playerId.toString()">
        <span>{{ friend.accountName }}</span>
        <div class="friend-actions">
          <button
            type="button"
            class="friend-enter"
            :disabled="!connected || visitBusy || visitOwnerId === friend.playerId"
            @click="emit('enter', friend.playerId)"
          >
            {{ visitOwnerId === friend.playerId ? '正在访问' : '进入农场' }}
            <span
              v-if="hasFarmRedDot(friend.playerId)"
              class="red-dot"
              aria-label="好友农场有成熟作物"
            />
          </button>
          <button type="button" :disabled="!connected" @click="emit('gift', friend)">
            赠送礼物
          </button>
        </div>
      </li>
    </ul>
    <p v-else class="empty-state">暂无好友，先生成好友码分享给朋友吧。</p>
  </div>
</template>

<style scoped>
.friends-panel {
  display: grid;
  gap: 0.7rem;
}

.friends-panel h3 {
  margin: 0;
  color: #24361f;
  font-size: 0.95rem;
}

.friends-panel__code {
  display: grid;
  gap: 0.5rem;
}

.friends-panel__row {
  display: flex;
  gap: 0.45rem;
}

.friends-panel__row button,
.redeem-form button {
  min-height: 2.5rem;
  padding: 0.4rem 0.8rem;
  font-size: 0.82rem;
}

.generated-code,
.share-url {
  margin: 0;
  color: #3d4f36;
  font-size: 0.82rem;
  word-break: break-all;
}

.share-url {
  display: flex;
  flex-wrap: wrap;
  gap: 0.45rem;
  align-items: center;
}

.share-url button {
  flex: none;
  min-height: 2rem;
  padding: 0.25rem 0.55rem;
  font-size: 0.75rem;
}

.friends-panel .empty-state {
  margin: 0;
}
</style>
