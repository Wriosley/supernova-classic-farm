<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from 'vue'

type Tier = 'safe' | 'service' | 'mysql' | 'destructive'

interface Command {
  kind: string
  workdir: string
  script?: string
  args: string[]
}

interface TestItem {
  id: string
  name: string
  type: string
  purpose: string
  tiers: Tier[]
  preconditions: string[]
  estimatedDurationSec: number
  impact: string
  files: string[]
  command: Command
  ports: number[]
  needsMysql: boolean
  clearMysqlDsn: boolean
  destructive: boolean
  repeatable: boolean
  runnable: boolean
  postRunWarning: string
}

interface Catalog {
  version: number
  tests: TestItem[]
}

interface LogLine {
  time: string
  stream: string
  message: string
}

interface RunSnapshot {
  runId?: string
  testId?: string
  name?: string
  status: 'idle' | 'running' | 'succeeded' | 'failed' | 'cancelled'
  startedAt?: string
  finishedAt?: string
  elapsedMs: number
  exitCode?: number | null
  postRunWarning?: string
  logs?: LogLine[]
  destructive?: boolean
  repeatable?: boolean
}

interface StatusResponse {
  mysqlConfigured: boolean
  busy: boolean
  current: RunSnapshot
  destructiveConfirmToken: string
}

const catalog = ref<Catalog | null>(null)
const status = ref<StatusResponse | null>(null)
const current = ref<RunSnapshot>({ status: 'idle', elapsedMs: 0, logs: [] })
const errorMessage = ref('')
const selectedId = ref('')
const confirmText = ref('')
const copyState = ref<'idle' | 'copied' | 'failed'>('idle')
const logBox = ref<HTMLElement | null>(null)
let copyResetTimer: number | undefined

let source: EventSource | null = null
let pollTimer: number | undefined

const selected = computed(() => catalog.value?.tests.find((t) => t.id === selectedId.value) ?? null)

const grouped = computed(() => {
  const tests = catalog.value?.tests ?? []
  const order: Tier[] = ['safe', 'service', 'mysql', 'destructive']
  return order
    .map((tier) => ({
      tier,
      items: tests.filter((t) => t.tiers.includes(tier) && primaryTier(t) === tier),
    }))
    .filter((group) => group.items.length > 0)
})

function primaryTier(item: TestItem): Tier {
  if (item.tiers.includes('destructive')) return 'destructive'
  if (item.tiers.includes('mysql')) return 'mysql'
  if (item.tiers.includes('service')) return 'service'
  return 'safe'
}

function formatDuration(sec: number): string {
  if (sec < 60) return `${sec}s`
  return `${Math.round(sec / 60)}m`
}

function canRun(item: TestItem): boolean {
  if (!item.runnable) return false
  if (status.value?.busy) return false
  if (item.needsMysql && !status.value?.mysqlConfigured) return false
  if (item.destructive) {
    return confirmText.value === status.value?.destructiveConfirmToken
  }
  return true
}

async function refreshStatus() {
  const res = await fetch('/api/status')
  if (!res.ok) throw new Error(await res.text())
  status.value = await res.json()
  if (status.value?.current) {
    current.value = {
      ...status.value.current,
      logs: status.value.current.logs ?? current.value.logs ?? [],
    }
  }
}

async function loadCatalog() {
  const res = await fetch('/api/catalog')
  if (!res.ok) throw new Error(await res.text())
  catalog.value = await res.json()
  if (!selectedId.value && catalog.value?.tests.length) {
    selectedId.value = catalog.value.tests[0].id
  }
}

async function runSelected() {
  const item = selected.value
  if (!item) return
  errorMessage.value = ''
  confirmText.value = item.destructive ? confirmText.value : ''
  const body: { confirmToken?: string } = {}
  if (item.destructive) {
    body.confirmToken = confirmText.value
  }
  const res = await fetch(`/api/tests/${encodeURIComponent(item.id)}/run`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
  })
  if (!res.ok) {
    errorMessage.value = await res.text()
    return
  }
  current.value = { ...(await res.json()), logs: [] }
  await refreshStatus()
}

async function cancelRun() {
  errorMessage.value = ''
  const res = await fetch('/api/tests/cancel', { method: 'POST' })
  if (!res.ok) {
    errorMessage.value = await res.text()
  }
}

function formatLogsForCopy(): string {
  const lines = current.value.logs ?? []
  return lines.map((line) => `[${line.stream}] ${line.message}`).join('\n')
}

async function copyLogs() {
  const text = formatLogsForCopy()
  if (!text) {
    copyState.value = 'failed'
    scheduleCopyReset()
    return
  }
  try {
    await navigator.clipboard.writeText(text)
    copyState.value = 'copied'
  } catch {
    copyState.value = 'failed'
  }
  scheduleCopyReset()
}

function scheduleCopyReset() {
  if (copyResetTimer) window.clearTimeout(copyResetTimer)
  copyResetTimer = window.setTimeout(() => {
    copyState.value = 'idle'
  }, 1500)
}

function connectStream() {
  source?.close()
  source = new EventSource('/api/tests/current/stream')
  const onPayload = (event: MessageEvent) => {
    try {
      const payload = JSON.parse(event.data) as { type: string; run: RunSnapshot; line?: LogLine }
      if (payload.run) {
        const prevLogs = current.value.logs ?? []
        const nextLogs =
          payload.type === 'log' && payload.line
            ? [...prevLogs, payload.line]
            : payload.run.logs ?? prevLogs
        current.value = { ...payload.run, logs: nextLogs }
      }
      void refreshStatus()
      requestAnimationFrame(() => {
        if (logBox.value) {
          logBox.value.scrollTop = logBox.value.scrollHeight
        }
      })
    } catch {
      // ignore malformed events
    }
  }
  for (const name of ['snapshot', 'started', 'log', 'finished']) {
    source.addEventListener(name, onPayload as EventListener)
  }
}

onMounted(async () => {
  try {
    await loadCatalog()
    await refreshStatus()
    connectStream()
    pollTimer = window.setInterval(() => {
      void refreshStatus()
    }, 5000)
  } catch (err) {
    errorMessage.value = err instanceof Error ? err.message : String(err)
  }
})

onUnmounted(() => {
  source?.close()
  if (pollTimer) window.clearInterval(pollTimer)
  if (copyResetTimer) window.clearTimeout(copyResetTimer)
})
</script>

<template>
  <div class="page">
    <header class="hero">
      <div>
        <p class="eyebrow">Local development only</p>
        <h1>Classic Farm Test Platform</h1>
        <p class="lede">
          Whitelist runner for existing Go tests, vet, frontend checks and PowerShell E2E wrappers.
          Frontend never receives MySQL passwords.
        </p>
      </div>
      <div class="status-card">
        <div><span>Runner</span><strong>{{ status?.busy ? 'busy' : 'idle' }}</strong></div>
        <div>
          <span>MySQL configured</span>
          <strong :class="status?.mysqlConfigured ? 'ok' : 'warn'">
            {{ status?.mysqlConfigured ? 'yes' : 'no' }}
          </strong>
        </div>
        <div>
          <span>Current</span>
          <strong>{{ current.testId || '—' }}</strong>
        </div>
      </div>
    </header>

    <p class="banner">
      Platform run history is local convenience only. Formal verification still belongs in
      <code>docs/evidence/</code>.
    </p>

    <p v-if="errorMessage" class="error">{{ errorMessage }}</p>

    <div class="layout">
      <section class="panel list-panel">
        <div v-for="group in grouped" :key="group.tier" class="group">
          <h2 :class="['tier', group.tier]">{{ group.tier }}</h2>
          <button
            v-for="item in group.items"
            :key="item.id"
            type="button"
            class="test-card"
            :class="{ selected: item.id === selectedId, destructive: item.destructive }"
            @click="selectedId = item.id"
          >
            <div class="card-top">
              <strong>{{ item.name }}</strong>
              <span>{{ formatDuration(item.estimatedDurationSec) }}</span>
            </div>
            <p>{{ item.purpose }}</p>
            <div class="tags">
              <span v-for="tier in item.tiers" :key="tier" :class="['tag', tier]">{{ tier }}</span>
              <span v-if="!item.runnable" class="tag">manual</span>
              <span v-if="item.destructive" class="tag destructive">non-repeatable</span>
            </div>
          </button>
        </div>
      </section>

      <section class="panel detail-panel" v-if="selected">
        <h2>{{ selected.name }}</h2>
        <p>{{ selected.purpose }}</p>

        <dl>
          <div>
            <dt>Impact</dt>
            <dd>{{ selected.impact }}</dd>
          </div>
          <div>
            <dt>Preconditions</dt>
            <dd>
              <ul>
                <li v-for="item in selected.preconditions" :key="item">{{ item }}</li>
              </ul>
            </dd>
          </div>
          <div>
            <dt>Files</dt>
            <dd>
              <ul>
                <li v-for="file in selected.files" :key="file"><code>{{ file }}</code></li>
              </ul>
            </dd>
          </div>
          <div v-if="selected.ports.length">
            <dt>Ports</dt>
            <dd>{{ selected.ports.join(', ') }}</dd>
          </div>
        </dl>

        <div v-if="selected.destructive" class="danger-box">
          <strong>Destructive test</strong>
          <p>
            Active Shard MySQL Migration permanently advances at least one Fence epoch. After a
            successful run, this database must not be reused for epoch-one bootstrap tests or
            Coordinator restart-recovery claims.
          </p>
          <p v-if="selected.postRunWarning">{{ selected.postRunWarning }}</p>
          <label>
            Type <code>{{ status?.destructiveConfirmToken }}</code> to enable Run
            <input v-model="confirmText" autocomplete="off" spellcheck="false" />
          </label>
        </div>

        <div class="actions">
          <button type="button" class="primary" :disabled="!canRun(selected)" @click="runSelected">
            {{ selected.runnable ? 'Run' : 'Not runnable' }}
          </button>
          <button type="button" :disabled="current.status !== 'running'" @click="cancelRun">
            Cancel
          </button>
        </div>
      </section>

      <section class="panel log-panel">
        <div class="log-header">
          <h2>Run output</h2>
          <div class="meta">
            <span>status: {{ current.status }}</span>
            <span>elapsed: {{ current.elapsedMs }} ms</span>
            <span>exit: {{ current.exitCode ?? '—' }}</span>
            <button
              type="button"
              class="copy-btn"
              :disabled="!(current.logs && current.logs.length)"
              @click="copyLogs"
            >
              {{
                copyState === 'copied'
                  ? 'Copied'
                  : copyState === 'failed'
                    ? 'Copy failed'
                    : 'Copy logs'
              }}
            </button>
          </div>
        </div>
        <p v-if="current.postRunWarning && current.status === 'succeeded'" class="danger-box compact">
          {{ current.postRunWarning }}
        </p>
        <pre ref="logBox" class="log">
<code v-for="(line, index) in current.logs || []" :key="index" :class="line.stream">[{{ line.stream }}] {{ line.message }}
</code></pre>
      </section>
    </div>
  </div>
</template>

<style scoped>
.page {
  max-width: 1400px;
  margin: 0 auto;
  padding: 24px;
}

.hero {
  display: flex;
  justify-content: space-between;
  gap: 24px;
  align-items: stretch;
  margin-bottom: 16px;
}

.eyebrow {
  margin: 0 0 8px;
  text-transform: uppercase;
  letter-spacing: 0.08em;
  font-size: 12px;
  color: var(--muted);
}

h1 {
  margin: 0 0 8px;
  font-size: 2rem;
}

.lede {
  margin: 0;
  max-width: 52rem;
  color: var(--muted);
  line-height: 1.5;
}

.status-card {
  min-width: 220px;
  background: var(--panel);
  border: 1px solid var(--line);
  border-radius: 12px;
  padding: 16px;
  display: grid;
  gap: 10px;
}

.status-card div {
  display: flex;
  justify-content: space-between;
  gap: 12px;
}

.status-card span {
  color: var(--muted);
}

.ok {
  color: var(--ok);
}

.warn {
  color: var(--warn);
}

.banner,
.error {
  border-radius: 10px;
  padding: 12px 14px;
  margin: 0 0 16px;
}

.banner {
  background: rgba(31, 107, 74, 0.08);
  border: 1px solid rgba(31, 107, 74, 0.2);
}

.error {
  background: var(--danger-bg);
  border: 1px solid rgba(161, 38, 34, 0.3);
  color: var(--danger);
}

.layout {
  display: grid;
  grid-template-columns: 320px 1fr 1fr;
  gap: 16px;
}

.panel {
  background: var(--panel);
  border: 1px solid var(--line);
  border-radius: 14px;
  padding: 16px;
  min-height: 420px;
}

.group + .group {
  margin-top: 18px;
}

.tier {
  margin: 0 0 8px;
  font-size: 12px;
  text-transform: uppercase;
  letter-spacing: 0.08em;
}

.tier.destructive,
.tag.destructive {
  color: var(--danger);
}

.test-card {
  width: 100%;
  text-align: left;
  background: #fff;
  border: 1px solid var(--line);
  border-radius: 10px;
  padding: 12px;
  margin-bottom: 8px;
}

.test-card.selected {
  border-color: var(--accent);
  box-shadow: inset 0 0 0 1px rgba(31, 107, 74, 0.35);
}

.test-card.destructive {
  border-color: rgba(161, 38, 34, 0.45);
  background: #fff8f7;
}

.card-top {
  display: flex;
  justify-content: space-between;
  gap: 8px;
  margin-bottom: 6px;
}

.test-card p {
  margin: 0 0 8px;
  color: var(--muted);
  font-size: 0.92rem;
  line-height: 1.4;
}

.tags {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
}

.tag {
  font-size: 11px;
  border-radius: 999px;
  padding: 2px 8px;
  border: 1px solid var(--line);
  color: var(--muted);
}

.detail-panel h2,
.log-panel h2 {
  margin: 0 0 8px;
}

.detail-panel p {
  color: var(--muted);
  line-height: 1.5;
}

dl {
  display: grid;
  gap: 12px;
  margin: 16px 0;
}

dt {
  font-weight: 650;
  margin-bottom: 4px;
}

dd {
  margin: 0;
  color: var(--muted);
}

ul {
  margin: 0;
  padding-left: 18px;
}

.danger-box {
  background: var(--danger-bg);
  border: 1px solid rgba(161, 38, 34, 0.35);
  color: var(--danger);
  border-radius: 10px;
  padding: 12px;
  margin: 12px 0;
}

.danger-box.compact {
  margin-bottom: 12px;
}

.danger-box label {
  display: grid;
  gap: 6px;
  margin-top: 10px;
}

.danger-box input {
  border: 1px solid rgba(161, 38, 34, 0.35);
  border-radius: 8px;
  padding: 8px 10px;
}

.actions {
  display: flex;
  gap: 8px;
}

.primary {
  background: var(--accent);
  color: white;
  border: none;
  border-radius: 8px;
  padding: 10px 16px;
}

.actions button:not(.primary) {
  background: white;
  border: 1px solid var(--line);
  border-radius: 8px;
  padding: 10px 16px;
}

.log-header {
  display: flex;
  justify-content: space-between;
  gap: 12px;
  align-items: baseline;
  margin-bottom: 8px;
}

.meta {
  display: flex;
  flex-wrap: wrap;
  gap: 10px;
  align-items: center;
  color: var(--muted);
  font-size: 0.9rem;
}

.copy-btn {
  background: white;
  border: 1px solid var(--line);
  border-radius: 8px;
  padding: 4px 10px;
  color: var(--ink);
}

.copy-btn:disabled {
  opacity: 0.45;
}

.log {
  margin: 0;
  height: calc(100% - 48px);
  min-height: 360px;
  max-height: 70vh;
  overflow: auto;
  background: #171b22;
  color: #d7deea;
  border-radius: 10px;
  padding: 12px;
  font-family: var(--mono);
  font-size: 12px;
  line-height: 1.45;
  white-space: pre-wrap;
}

.log code {
  display: block;
}

.log .stderr {
  color: #ffb4b0;
}

.log .system {
  color: #f0d48a;
}

@media (max-width: 1100px) {
  .layout {
    grid-template-columns: 1fr;
  }

  .hero {
    flex-direction: column;
  }
}
</style>
