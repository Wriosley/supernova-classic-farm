<script setup lang="ts">
import { ref } from 'vue'

defineProps<{
  accountName: string
  playerId: string
  phaseLabel: string
  connected: boolean
  busy: boolean
  canReconnect: boolean
  errorMessage: string
  steps: Array<{ label: string; state: 'done' | 'active' | 'waiting' }>
  facts: Array<{ label: string; value: string }>
}>()

const emit = defineEmits<{
  reconnect: []
  disconnect: []
  logout: []
}>()

const diagnosticsOpen = ref(false)
</script>

<template>
  <div class="account-panel">
    <dl class="facts compact">
      <div><dt>账号</dt><dd>{{ accountName || '—' }}</dd></div>
      <div><dt>player_id</dt><dd>{{ playerId || '—' }}</dd></div>
    </dl>

    <p class="account-phase">
      连接状态：<strong>{{ phaseLabel }}</strong>
    </p>

    <div class="account-actions">
      <button type="button" :disabled="!canReconnect || busy" @click="emit('reconnect')">
        重新连接
      </button>
      <button type="button" :disabled="!connected" @click="emit('disconnect')">断开</button>
      <button type="button" class="account-logout" :disabled="busy" @click="emit('logout')">
        退出登录
      </button>
    </div>

    <p v-if="errorMessage" class="error-banner" role="alert">{{ errorMessage }}</p>

    <details class="account-diagnostics" :open="diagnosticsOpen">
      <summary @click.prevent="diagnosticsOpen = !diagnosticsOpen">诊断</summary>

      <ol class="account-timeline">
        <li v-for="step in steps" :key="step.label" :data-state="step.state">
          <span class="dot" aria-hidden="true"></span>
          <span>{{ step.label }}</span>
        </li>
      </ol>

      <dl class="facts">
        <div v-for="fact in facts" :key="fact.label">
          <dt>{{ fact.label }}</dt>
          <dd>{{ fact.value }}</dd>
        </div>
      </dl>
    </details>
  </div>
</template>

<style scoped>
.account-panel {
  display: grid;
  gap: 0.7rem;
}

.account-phase {
  margin: 0;
  color: #4b5a45;
  font-size: 0.85rem;
}

.account-actions {
  display: flex;
  flex-wrap: wrap;
  gap: 0.45rem;
}

.account-actions button {
  min-height: 2.5rem;
  padding: 0.4rem 0.8rem;
  font-size: 0.82rem;
}

.account-logout {
  margin-left: auto;
}

.account-diagnostics summary {
  cursor: pointer;
  color: #4b5a45;
  font-size: 0.82rem;
  font-weight: 750;
}

.account-timeline {
  display: grid;
  gap: 0.4rem;
  margin: 0.7rem 0 0;
  padding: 0;
  list-style: none;
}

.account-timeline li {
  display: grid;
  grid-template-columns: 0.8rem 1fr;
  align-items: center;
  gap: 0.5rem;
  color: #778071;
  font-size: 0.78rem;
}

.account-timeline .dot {
  width: 0.65rem;
  height: 0.65rem;
  border-radius: 50%;
  background: #cdd4c4;
}

.account-timeline li[data-state="active"] {
  color: #31552d;
  font-weight: 750;
}

.account-timeline li[data-state="active"] .dot {
  background: #e3a33f;
}

.account-timeline li[data-state="done"] .dot {
  background: #4e7c45;
}

.account-diagnostics .facts {
  grid-template-columns: 1fr;
  margin-top: 0.7rem;
}
</style>
