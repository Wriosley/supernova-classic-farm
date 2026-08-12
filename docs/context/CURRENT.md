---
status: active
updated: 2026-08-11
---

# Current Handoff

## Resume here

V3 is the only current production-target strategy. A new AI should read, in order:

1. `../../AGENTS.md` and `../README.md`;
2. `PROJECT.md` and this file;
3. `../architecture/stateful-zone-v3-architecture.md`;
4. `../architecture/single-player-vertical-loop-business-architecture.md`;
5. the accepted contracts under `../contracts/`;
6. only the additional requirement, ADR, plan, or evidence files needed for the task.

Do not resume V1 or V2 as the implementation target. Do not read every ADR as if all decisions were simultaneously active. The ADR directory preserves how the design evolved; current truth comes from this handoff, the current architecture, and the accepted ADRs that the current architecture explicitly references.

## Snapshot at handoff

- The single-player owner loop is complete through `player_seq=8`.
- Pure Tcaplus is green for auth, checkpoint, Fence, migration, Outbox and
  complete process restart.
- The fixed dual-Zone kind cluster now runs **eight** Ready Deployments
  (Login, Gate, Coordinator, zone-a, zone-b, FriendSvr, InfoSvr, MailSvr)
  after the 2026-08-11 rebuild/load/`kubectl apply -k` + rollout. Evidence:
  `../evidence/2026-08-11-k8s-redeploy-mail-info.md`. Earlier friend
  interaction / restart-recovery E2Es remain
  `../evidence/2026-08-07-friend-interaction-e2e.md`.
- Dynamic Zone discovery and automatic scaling are outside the prototype.
- Friend plan phases 0–7 are complete: contracts, gRPC+HMAC, FriendSvr,
  visit sessions, FarmViewPatch/Presence, steal Saga, pest/catch/help,
  H5 wiring, kind FriendSvr deploy, and multi-client WS E2E with full
  stack restart recovery.
- Final delivery sprint **01 Actor register-before-load** is complete:
  cold activation now creates a `Loading` Actor + mailbox under
  `Runtime.mu`, `Mailbox.Submit`s Load/init as the first job, then
  publishes the Actor so concurrent callers share one mailbox and one
  `Store.Load`. Failed activation is removed via `removeActorIfSame` and
  can retry; Loading actors participate in Drain/Close. Evidence:
  `../evidence/2026-08-10-actor-register-before-load.md`.
- Final delivery sprint **02 lazy farm init** is complete: LoginSvr
  registration creates account identity only (no PlayerCheckpoint /
  ShardFence). Owner Zone on first Actor activation treats clear
  `ErrCheckpointNotFound` as new-player init via fenced
  `CreateInitial`, and only then marks the Actor `Ready`. Evidence:
  `../evidence/2026-08-10-zone-initial-player-checkpoint.md`.
- Final delivery sprint **03 broadcast / business decoupling** is
  complete: plot commands report `DomainChanges`; the owner Actor
  builds ordered `FarmViewPatch` inside the mailbox; a bounded
  `farmview.Dispatcher` fans out via the existing Broadcaster. Broadcast
  remains online best-effort; H5 uses `decideFarmViewPatch` for
  contiguous apply / duplicate ignore / gap+epoch resync. Evidence:
  `../evidence/2026-08-10-farm-broadcast-separation.md`.
- Final delivery sprint **04-1 pet minimum loop** is complete: players
  start with no pets; buy/deploy 田园犬/牧羊犬, buy/feed dog food
  (24h stackable `food_active_until_ms`), and steal guard rolls once in
  `ApplyStealOnOwner` then freezes `StealGuardOutcome` into the Saga
  receipt; visitor commit deducts `min(coins, penalty)` without paying
  the owner. H5 `PetPanel.vue` is text-only. Evidence:
  `../evidence/2026-08-11-pet-guard-e2e.md`.
- Final delivery sprint **04-3A MailSvr** is complete: independent MailSvr
  (`:8087`) with Public/Private mail tables, mailbox cursor, read state,
  intranet Admin Bearer APIs, registration-time public-mail filtering, and
  fail-open InfoSvr red-dot notify on private create. Evidence:
  `../evidence/2026-08-12-mailsvr-query.md`.
- Final delivery sprint **04-3B friend gift Outbox** is complete: Gate checks
  mutual friendship, the sender Actor deducts crop inventory and appends a
  deterministic `CREATE_GIFT_MAIL` Outbox in one commit, and the Zone Relay
  delivers to MailSvr `CreateGiftMail` with `source_event_id` dedup +
  fail-open Info red-dot. Evidence:
  `../evidence/2026-08-12-friend-gift-outbox.md`.
- Final delivery sprint **04-3C mail claim Saga** is complete: MailSvr
  orchestrates `BeginClaim → Zone ApplyMailReward → CompleteClaim`; Player
  Actor grants attachments all-or-nothing with sync SaveCAS
  `MailClaimReceipt`; ClaimReconciler recovers the three crash windows.
  Evidence: `../evidence/2026-08-12-mail-claim-saga.md`.
- Final delivery sprint **04-3F H5 mailbox + red dots** is complete: Gate
  proxies `OPEN_MAILBOX`/`MARK_MAIL_READ`/`CLAIM_MAIL`; H5 shows mailbox
  modal, friend gift panel, mail/friend-farm red dots (local-only, cleared on
  click). Evidence: `../evidence/2026-08-12-h5-mail-red-dot.md`.
- Mail claim now carries the recipient version end to end: Zone
  `ApplyMailRewardResponse` reports `owner_epoch` beside `player_seq`, MailSvr
  `ClaimMailResponse` forwards a `state_version`, and Gate stamps it on the
  response envelope. Without it H5 rejected every successful claim with
  "写命令响应缺少 patch 或 state_version". The version is deliberately absent
  when an earlier attempt already applied the reward, and H5 then reloads a
  snapshot instead of sequencing the patch.
- `CHECK_MAILBOX_INDICATOR` (Action 328) closes the offline red-dot hole:
  `RED_DOT_CHANGED` only reaches players connected at delivery time and public
  mail never pushes at all, so H5 queries the indicator once after
  authentication. A failure there leaves the dot untouched and never blocks
  login. Evidence: `../evidence/2026-08-12-h5-mail-red-dot.md`.
- H5 is now a game shell instead of a stack of diagnostic cards: the login page
  is only `Grow!` + account/password (one button that logs in, or registers when
  the account is unknown), and after login a top nav (username/coins/账号·商店·
  宠物·好友·邮箱·任务·仓库) opens backdrop-less right drawers over a permanently
  visible farm, with a sticky tool bar (手/铲子/杀虫剂/肥料) and seed bar
  (all catalog seeds, unowned ones greyed, hover shows maturity time) below it.
  Connection timeline and Actor diagnostics moved into 账号 → 诊断. No server
  change. Evidence: `../evidence/2026-08-11-h5-shell-redesign.md`.
- H5 shell follow-ups after first live use: reactive `connected` + shell
  reconnect banner (dead sockets no longer look like "buttons do nothing");
  seed-bar `maturity_seconds` formatted via `Number(bigint)` (uint64 was
  crashing the whole render with "Cannot mix BigInt and other types");
  `MailKind.PUBLIC/PRIVATE/GIFT` enum names (same protobuf-es prefix trap as
  earlier red-dot bug); Vue `errorHandler` fatal banner outside the app;
  `npm run typecheck` now points at `tsconfig.app.json` (the root config was
  checking **zero** files). Evidence updated in the same redesign note.
- The starting farm is now **16 plots** (`InitialPlotCount`), and accounts
  created against the 4-plot build are backfilled lazily: `activateActor` calls
  `State.ensureInitialPlots()`, bumps `CheckpointRevision`, and lets the dirty
  flusher persist the new empty plots. H5 renders the plots frameless on one
  green lawn (`.plots-grid` paints the grass, `.plot-caption` overlays the text
  on the soil sprite) and the seed bar now lists only seeds the player owns.
  A fresh account on the restarted local stack returns `plots=16`. Evidence:
  `../evidence/2026-08-11-farm-16-plots-grass-ui.md` (backfill of pre-existing
  accounts and the visual pass remain owner checks).
- Mature plots now show the crop that actually grew there: ten new 16×16
  sprites (crops 2002–2011) come from the existing deterministic pixel script,
  `plot.mature` lost its baked-in generic crop, and `web/src/lib/crop-art.ts`
  maps `crop_id` to a sprite with a demo fallback. Evidence:
  `../evidence/2026-08-12-per-crop-mature-sprites.md`.
- The shop lists **every** seed as its own expandable row (per-crop quantity,
  total, and buy button) instead of a name picker that drove one shared buy
  form, and the deployed pet now sits beside the lawn: four new 32×32 dog
  sprites (田园犬/牧羊犬 × fed/hungry), breed above the head, and
  "xx护卫中（时间：hh:mm:ss）" / "xx现在很饿" driven by
  `food_active_until_ms`. Evidence:
  `../evidence/2026-08-12-shop-seed-rows-and-guard-dog.md`.
- Friend-farm visits now carry a public `FarmVisitSnapshot.pet` so visitors see
  the owner's deployed dog (or an empty "尚未获得宠物" slot). Evidence:
  `../evidence/2026-08-12-friend-farm-pet-badge.md`.
- Actor activation no longer treats "checkpoint revision ahead of persisted
  after Outbox prune" as corruption: `activateActor` only fails when
  `CheckpointRevision < persistedRevision`. That unblocked accounts that had
  sent friend gifts and then could not reload snapshots
  (`SERVICE_UNAVAILABLE`). Postmortem:
  `../bugs/2026-08-11-gift-outbox-activation-revision-mismatch.md`.
- Final delivery sprint **04-4 share-link auto friend** is implemented:
  FriendSvr returns `share_url` from `PUBLIC_WEB_BASE_URL`; H5 stores pending
  invite codes and auto-redeems after AUTH; `FirstFriendReward` + Saga steps
  grant both players a system mail (10 coins + 4 grape seeds) only on the
  invitee's first successful friendship. Evidence:
  `../evidence/2026-08-12-local-friend-invite-link.md` (unit-tested; dual-
  browser E2E and Tcaplus `FirstFriendReward` table creation remain owner).
- Final delivery sprint **04-5 multi-crop steal** is complete: all 11 crops
  freeze steal limits from `ceil(base_yield/2)`; steal requests carry
  `expected_crop_item_id` + farm-view version; FriendInteraction persists
  crop/qty; same visitor once per crop round; H5 sends plot `crop_item_id`.
  Evidence: `../evidence/2026-08-12-multi-crop-steal.md`.
- Farm visits return a public snapshot on `ENTER_FRIEND_FARM` and now also
  receive incremental `FarmViewPatch` pushes: public plot mutations (plant,
  fertilize, harvest, clean, natural maturity) report `DomainChanges`, bump
  an Actor-local `farm_view_seq` inside the same mailbox call, and
  `farmview.Dispatcher` → `Broadcaster` fans the resulting patch out through
  Gate to the owner plus every currently registered visitor. H5 replaces the
  full snapshot on an epoch change or a seq gap and merges in place on
  `seq == local + 1`.
- The cross-Actor `FriendInteraction` Saga is live for `STEAL_FRIEND_CROP`
  (pest/catch-pest/help-clean remain Phase 6, on the same infrastructure):
  `PLANT` freezes `steal_quantity`/`max_steal_times`/`protected_owner_yield`
  alongside `base_yield`; `player.CanSteal` gates the public `can_steal`
  flag; `player.Runtime` exposes synchronous, mailbox-serialized
  `ReserveSteal`/`ApplyStealOnOwner`/`CommitSteal`/`ReleaseSteal` steps that
  flush immediately instead of joining the async Dirty batch; a new
  `server/internal/interaction` package drives the
  `INIT -> VISITOR_RESERVED -> OWNER_APPLIED -> VISITOR_COMMITTED ->
  COMPLETED` (or `-> RELEASING -> ABORTED`) state machine against a Tcaplus
  (or in-memory dev) `FriendInteraction` store with per-step CAS, and each
  Zone runs a 5-second reconciler ticker that resumes any interaction whose
  `retry_at_ms` is due, recovering all three crash windows. Gate and H5
  route `STEAL_FRIEND_CROP` the same way as the other visit actions; H5
  merges the returned `visitor_patch` optimistically (no `state_version` on
  `FriendActionResponse` by frozen contract) and relies on the independent
  `FarmViewPatch` push for the owner's plot update.

## Current accepted direction

- The 30-million-DAU production target uses stateful Player Actors in Zone processes.
- One logical shard has exactly one write-authorized Active Zone Owner at a time; one Zone owns many logical shards.
- Player IDs map to 4096 versioned logical shards. Placement may use Rendezvous Hashing and load correction, but only the production Coordinator's majority-committed route grants ownership.
- GateSvr routes from a local cache of committed `ACTIVE` routes; ordinary commands do not call the Coordinator.
- The current prototype uses exactly two static Owners, `zone-a` and `zone-b`.
  Coordinator materializes versioned Rendezvous candidates into 4096 committed
  routes; Gate warms a complete immutable Snapshot and Zones atomically refresh
  read-only authorization Snapshots.
- Pure Tcaplus is the current persistence target. Account, Session,
  PlayerCheckpoint, ShardFence, MigrationProgress and PlayerOutbox adapters
  pass unit, live owner-loop, migration and restart checks without MySQL.
  MySQL remains a tested rollback adapter.
- The prototype implements Coordinator-compatible route, lease, epoch,
  state-transition and fencing semantics with one Coordinator process.
  Production consensus and dynamic Zone membership are not implemented.
- Commands for one player enter one Actor mailbox and execute serially.
- A successful ordinary write follows `validate -> apply Actor memory -> update task if matched -> player_seq++ -> save idempotency result/outbox -> checkpoint_revision++ -> mark Dirty -> reply`.
- `checkpoint_revision` orders persistence CAS and is not client-visible. Saving a terminal business failure, pruning idempotency results, or reconciling Outbox increments it without incrementing `player_seq`.
- A shared Zone flusher asynchronously persists Dirty checkpoints through
  `CheckpointStore`. Tcaplus is the current recovery store; active Actor memory
  remains online truth.
- V3 accepts that an abnormal Zone exit may roll back the latest unflushed ordinary game state. It does not use V2's Kafka Journal or MySQL `journal_events` path.
- Normal shutdown, Actor eviction, and controlled migration must drain the mailbox and flush Dirty state before ownership changes.
- WebSocket is established between Client and GateSvr and carries game commands, responses, snapshots, and pushes. The client does not connect directly to Zone.
- Current chapter-task state belongs to the Player Actor and is updated by successful server-side business actions. Rewards are claimed manually.

## Current business-design state

The first accepted vertical loop is:

```text
buy seed -> plant -> fertilize -> grow/mature -> harvest -> sell
-> update task -> claim reward -> clean plot
```

Important rules already recorded in the business architecture:

- ConfigSvr is the configuration authority; Zone uses an atomically replaced, versioned local snapshot.
- A command pins one configuration snapshot for its whole execution.
- Planting freezes the crop's maturity threshold, base growth rate, and base yield.
- Growth is derived from elapsed server time and the effective rate; it is not persisted by ticking every second.
- Shop price versions prevent purchases or sales from silently using stale prices.
- Farm, inventory, coins, current chapter task, recent idempotency results, and pending Outbox belong to one Player Actor checkpoint.
- Full warehouse makes an ordinary harvest fail atomically; task reward items that do not fit use a mail Outbox fallback.
- Client-visible configuration is an immutable versioned Protobuf package delivered over HTTP and verified by SHA-256; it never becomes transaction authority.
- A pending reward-mail Outbox is recorded atomically in Actor state but becomes database-durable only after the asynchronous checkpoint/Outbox transaction commits.
- Friend phases 0–5 are accepted: contracts, internal gRPC/HMAC migration,
  FriendSvr share-code/relation/list Saga, Zone `ApplyFriendTaskCredit`,
  farm-visit sessions (`ENTER`/`HEARTBEAT`/`EXIT_FRIEND_FARM` plus a
  one-shot public snapshot), incremental public-Patch broadcast and the
  cross-Actor `FriendInteraction` Saga for `STEAL_FRIEND_CROP` are
  implemented. Pest/catch-pest/help-clean interactions remain unimplemented
  (Phase 6).

## Current architecture and decision map

Current architecture:

- `../architecture/stateful-zone-v3-architecture.md`: current distributed target.
- `../architecture/single-player-vertical-loop-business-architecture.md`: accepted first-slice business design.

Accepted first-stage contracts:

- `../contracts/http-api.md`: registration, Session, one-time WS ticket, Gateway discovery and client-config bootstrap.
- `../contracts/websocket-protocol.md`: Protobuf game connection, commands, responses, snapshots, patches, Push and reconnect.
- `../contracts/idempotency-and-errors.md`: request identity, retained results, retry and error behavior.
- `../contracts/data-model.md`: Player checkpoint, `checkpoint_revision`, ShardMap, fences, Dirty batches and Outbox storage.
- `../contracts/event-contracts.md`: reward-mail event, relay and consumer deduplication.
- `../contracts/internal-grpc.md`: internal unary services, HMAC identity,
  deadlines, errors and retry boundaries.
- Complete Chinese reading mirrors use the `.zh-CN.md` suffix.

Current supporting decisions referenced by V3:

- ADR-0003: stateful Player Actor Zone foundation. Its V2 Journal-specific text is historical where V3/ADR-0006 conflicts.
- ADR-0006: asynchronous Dirty writeback supersedes ADR-0005's Journal write path.
- ADR-0008: V3 retains majority-authorized Shard ownership, replacing ADR-0004 as the current V3 statement.
- ADR-0009: current chapter-task progress belongs to Player Actor.
- ADR-0010: local prototype keeps unused WS tickets and CSRF nonce records
  process-local; Login restart drops them even when MySQL Sessions survive.
- ADR-0011: Player Runtime depends on one `CheckpointStore` contract. Logical
  checkpoint revision and opaque physical Store Token are kept separate so
  MySQL and Tcaplus can expose the same Load/CAS semantics.

Historical design evidence:

- V1 and ADR-0002: stateless target.
- V2, ADR-0004, and ADR-0005: synchronous-Journal-era routing and persistence design.
- ADR-0001: earlier modular-monolith implementation decision; useful history, not the current distributed architecture definition.

## Capacity planning values

These remain planning assumptions, not measured claims:

- 30 million DAU; 1.25 million average online; 3.75 million normal peak online/WebSocket connections.
- 4.5 million connection/reconnection pressure capacity.
- About 5 million peak resident Actors.
- About 69,444 peak game application messages per second.
- 4096 logical player shards.
- About 60 Zone instances as a pre-benchmark midpoint.

All values must be revised from reproducible prototype measurements before being presented as capability.

## Completed historical milestone: first stage by 2026-08-02

Goal:

```text
freeze the minimum protocol and data model
-> establish the Go backend and Vue 3 H5 runnable skeleton
-> login from H5
-> authenticate one Protobuf WebSocket
-> send one command through GateSvr and Player Actor
-> receive the correlated response
```

Current milestone status:

- WebSocket connection, Protobuf envelope, commands, responses, Push, snapshot, state-patch and reconnect semantics: frozen.
- Error codes, `request_id`, idempotency retention and retry behavior: frozen.
- Client-facing minimum player, farm, inventory and chapter-task views: frozen.
- `(owner_epoch, player_seq)` state-version and resynchronization semantics: frozen.
- HTTP registration, login, Session, one-time WS ticket and Gateway discovery: frozen in `../contracts/http-api.md`.
- Versioned client-config delivery and publication: frozen in `../contracts/http-api.md`.
- Internal Player checkpoint, recent idempotency results and Outbox persistence shape: frozen in `../contracts/data-model.md`.
- ShardMap, Dirty batch, `checkpoint_revision` and database fence: frozen in `../contracts/data-model.md`.
- Reward-mail Outbox event, relay and consumer deduplication: frozen in `../contracts/event-contracts.md`.
- Cross-contract review aligned the HTTP bootstrap/WS AUTH field set and ticket boundary, and clarified replayed-error versions, committed-lineage sequence monotonicity, event-version scalar types, idempotency, Outbox and abnormal-recovery meanings.
- Bounded first-stage implementation plan: completed in `../plans/2026-07-31-v3-first-stage-implementation-plan.md`.
- Shared HTTP/WS/data/event Protobuf generates Go and TypeScript types; both round-trip smoke tests pass.
- Login, Gate, Zone and the single-node Coordinator-compatible process compile; the complete Go test suite and `go vet ./...` pass.
- The Vue 3 H5 implements register/login, CSRF, bootstrap/config hash, Ticket, WS AUTH, snapshot/Shop reads, all seven owner-loop commands, `PLAYER_STATE_CHANGED` patch application and version-gap snapshot recovery. It presents four authoritative plots in a responsive 2x2 farm, with seed/fertilizer/shovel/hand tool selection, per-tool desktop cursors, state-aware plot targets, 1–50 seed purchases, quantity/all crop sales, inventory and chapter tasks.
- A repeatable four-process protocol client proves `register -> ws_ticket -> AUTH -> PING -> GET_PLAYER_SNAPSHOT -> RESPONSE` and Ticket replay rejection. Browser UI automation and MySQL persistence are not part of that evidence.
- The owner manually completed the same H5 registration-to-snapshot flow in a browser after an authenticated-CSRF binding defect was found and fixed. This is a manual smoke result, not automated browser evidence.
- `start-servers.ps1` builds and starts Login, Zone, Coordinator and Gate in dependency order, checks readiness and stops all child processes on exit.
- An optional `MYSQL_DSN` path provisions the account, initial deterministic `PlayerCheckpointV1` and first HTTP Session in one MySQL transaction, and loads the checkpoint when Zone first activates the Actor. Mocked-SQL tests, live registration-to-snapshot and fresh-process login/checkpoint-recovery E2Es pass on MySQL 8.4.11.
- `BUY_SEEDS` is the first implemented Actor write command. It validates the local versioned quote, mutates coins/inventory/task progress atomically, increments `player_seq`, retains terminal idempotency results, marks Dirty and returns a state patch. Same-ID replay does not apply the purchase twice.
- Zone now holds an immutable versioned local configuration snapshot behind atomic replacement. `GET_SHOP` returns active buy and sell quotes in stable order without activating a Player Actor; `BUY_SEEDS` and `SELL_CROP` derive authoritative prices and versions from the same pinned snapshot. This is the Zone-side bootstrap boundary, not a standalone ConfigSvr.
- `PLANT` is implemented in the Player Actor. It validates plot/config/inventory state, consumes one seed, freezes crop identity, maturity/rate/yield and timestamps, changes the plot to `GROWING`, advances the planting task, retains idempotency results and marks Dirty.
- Base crop growth now uses checked fixed-point settlement. Actor activation, each Actor request and a local one-second online scan materialize due plots in stable `plot_id` order; each `GROWING -> MATURE` transition increments both versions once and marks Dirty.
- Online maturity emits one unsolicited `PLAYER_STATE_CHANGED/MATURED` Push per transitioned plot. Zone forwards it to Gate over a loopback-only internal Protobuf endpoint; Gate keeps authenticated per-player subscriptions, buffers while a snapshot is in flight, drops versions not newer than the snapshot and flushes newer Pushes in version order.
- `APPLY_FERTILIZER` is implemented. It settles the old rate, validates the active slot/config/inventory, consumes fertilizer, freezes a deterministic timed effect, recomputes maturity, advances the task and retains idempotency. Growth settlement splits exact intervals across effect boundaries.
- `HARVEST` is implemented as an all-or-nothing Actor command. It requires `MATURE`, checks the 100-type and 300-per-stack warehouse limits before mutation, adds the complete frozen yield, advances the harvest task, changes the plot to `NEED_CLEANUP`, retains the result for replay and marks Dirty. Live in-memory and MySQL four-process runs reached `player_seq=5`.
- The MySQL Zone path flushes Dirty checkpoints asynchronously, checks the exact local `shard_fences` owner and epoch, and updates `player_checkpoints` using checkpoint-revision CAS. An owner-run two-stack E2E recovered `player_seq=1`, 4 coins and three seed items after all four services restarted.
- The extended two-stack E2E coalesced `BUY_SEEDS` and `PLANT` into a checkpoint and recovered `player_seq=2`, 4 coins, two seeds and a `GROWING` crop after all four services restarted.
- The latest extension also persisted and recovered `APPLY_FERTILIZER` at `player_seq=3`, including empty fertilizer inventory and the timed effect identity.
- The owner-run MySQL harvest extension observed online maturity at `player_seq=4`, harvested three crop items at `player_seq=5`, stopped all four processes, and recovered two seeds, three crops and the `NEED_CLEANUP` plot from a fresh stack.
- `SELL_CROP` supports an explicit positive quantity or `sell_all`, validates the active sell rule and `price_version`, removes inventory, adds checked integer-price coins, advances the sell task and changes the chapter to `CLAIMABLE` when all five tasks are complete. Same-ID `sell_all` replay returns the first resolved quantity. A live in-memory run reached `player_seq=6`, 19 coins, no crop stack and `CLAIMABLE`.
- Chapter status is now preserved between the data checkpoint enum and client enum; previously checkpoint load/write forced `IN_PROGRESS`, which would have lost `CLAIMABLE` after restart.
- The owner-run MySQL sell extension stopped all four processes after `SELL_CROP` and recovered `player_seq=6`, 19 coins, two seeds, no crop stack, the `NEED_CLEANUP` plot and `CLAIMABLE` from a fresh stack.
- `CLAIM_CHAPTER_REWARD` now validates the frozen chapter identity and status, credits 10 coins, allocates one fertilizer and three next-chapter seeds under warehouse limits, activates development chapter two, retains the exact result and advances to `player_seq=7`. Same-ID replay does not grant twice.
- Reward overflow is deterministic by `item_id`. A full warehouse keeps the fitting quantities in Actor state and records one `CreateRewardMailV1` pending Outbox event for all remaining items; the response says `items_pending_mail`, not that a mail was delivered.
- `deploy/migrations/000004_player_outbox.up.sql` adds the relational relay table. A Dirty flush validates pending event payloads and atomically inserts or immutable-compares each `player_outbox` row in the same MySQL transaction as checkpoint CAS. The relay, Mail Service and delivered-event reconciliation are not implemented.
- Live in-memory and owner-run MySQL four-process flows completed claim at `player_seq=7`, 29 coins, one fertilizer, three next-chapter seeds and chapter two `IN_PROGRESS`. After all four MySQL-backed services stopped, a fresh stack recovered the same `player_id=9` checkpoint, including the `NEED_CLEANUP` plot.
- `CLEAN_PLOT` is implemented as an idempotent Actor command. It requires `NEED_CLEANUP`, consumes and grants nothing, advances no task, clears every frozen crop/growth/effect field and returns the plot to `EMPTY`. The H5 no longer blocks cleaning until the chapter-one reward is claimed.
- The development shop now returns three active entries in stable entry-ID order: seed sale (`1001`, 2 coins), crop buyback (`1002`, 5 coins), and basic fertilizer sale (`item_id=1`, 2 coins). `BUY_FERTILIZER` has its own idempotent Actor command, shares the 1–50 quantity and 300-stack rules, and does not advance the seed-purchase task.
- Live in-memory and owner-run MySQL four-process flows completed the server-side owner loop at `player_seq=8` and replayed cleanup without applying twice. After all four MySQL-backed services stopped, a fresh stack recovered the same `player_id=10`, 29 coins, expected inventory, chapter two and the `EMPTY` plot.
- A browser-driven in-memory H5 run registered `player_id=1`, bought, planted, fertilized, received one natural maturity Push, harvested, sold, claimed and cleaned through `state_version=1/8`. The final UI showed 29 coins, two old seeds, one fertilizer, three next-chapter seeds, chapter two and an empty plot; gap recovery remained zero. A 320 CSS-pixel viewport check reported no horizontal overflow.
- New development Player state and registration checkpoints now contain four stable `EMPTY` plots (`plot_id=1..4`). Commands still patch only their requested plot; snapshot and checkpoint ordering remain stable. Existing development checkpoints are not migrated online and must be reset/re-registered locally.
- A browser-driven four-plot run used plot 2 for plant, fertilizer, natural maturity Push, harvest and cleanup while plots 1/3/4 remained empty. It also exercised an explicit one-crop sale followed by `sell_all`, verified the 1/50 purchase boundaries and tool cursor URL, completed at 29 coins with all four plots empty, and reported no horizontal overflow at 320 CSS pixels.
- An owner-run MySQL 8.4 two-stack E2E registered `player_id=11`, completed the command loop to `player_seq=8`, stopped all four services, then recovered the same checkpoint from fresh processes. The updated snapshot assertions validated four ordered plots and kept plots 2–4 `EMPTY`.
- `start-servers.ps1 -DualZone` starts Coordinator, Login, Zone A on 8082,
  Zone B on 8084 and Gate in dependency order. With `MYSQL_DSN`, Coordinator
  requires explicit bootstrap authorization and aligns all Fences before Login
  accepts registrations.
- Linux now has executable `start-servers.sh`, `deploy/migrate.sh` and
  `tests/e2e/run-mysql-restart-recovery.sh`. On TencentOS Server 4.4 with
  Go 1.26.5 and Docker MySQL 8.4, the dual-Zone five-process stack completed
  the full owner loop to `player_seq=8`, stopped normally, restarted and
  recovered 29 coins, two old seeds and an `EMPTY` plot. A separate live
  dual-Zone check routed players to both Owners, migrated one active Shard
  from Zone A epoch one to Zone B epoch two, persisted the post-migration
  write and rejected a delayed old-Zone writer. See
  `../evidence/2026-08-04-linux-dual-zone-mysql-baseline.md`.
- The first ADR-0011 implementation is complete. Runtime no longer injects
  separate checkpoint Loader/Writer interfaces; it carries
  `PersistedRevision` plus an opaque `StoreToken`, and consumes normalized CAS
  outcomes. `MySQLCheckpointStore` preserves the existing Fence,
  revision-CAS and Checkpoint/Outbox transaction. Full Go regression and the
  live Linux five-process restart/active-migration/Fence E2E pass after the
  refactor.
- The Tcaplus `PlayerCheckpoint` POC is complete against a real PB table using
  the official Go SDK module `v0.2.3` (API 3.55). It proves Create, Load,
  record-version plus logical-revision CAS, duplicate-commit reconciliation,
  stale-write rejection and reload. This was the single-table checkpoint;
  the pure-Tcaplus runtime described below supersedes its earlier limitation. See
  `../evidence/2026-08-04-tcaplus-player-checkpoint-poc.md`.
- The owner selected immediate pure-Tcaplus runtime work on 2026-08-05.
  PlayerIdCounter, account provisioning Saga, durable Session generation,
  ShardFence, MigrationProgress, fenced Checkpoint CAS, PlayerOutbox and
  activation reconciliation now have adapters and hermetic tests. Login, Zone,
  Coordinator and `start-servers.sh --dual-zone --tcaplus` are wired to reject
  `MYSQL_DSN`. The live table group now has all eight runtime tables. The
  no-MySQL five-process gate registered players, routed both Owners, persisted
  gameplay, migrated inactive and active Shards, and passed a complete
  post-migration restart. Fence bootstrap uses one Traverse plus bounded
  parallel inserts and preserves advanced epochs for route hydration. See
  `../evidence/2026-08-05-pure-tcaplus-runtime-gate.md`.
- The owner explicitly excluded dynamic Zone discovery and selected exactly
  two static Kubernetes Zones. A kind cluster now runs Coordinator, Login,
  Gate, `zone-a` and `zone-b` as five Deployments with pure Tcaplus storage.
  All Pods reached Ready and the live dual-Zone owner loop plus inactive/active
  migration E2E passed. `INTERNAL_NETWORK_MODE=kubernetes` is an explicit
  non-production Pod-network exception; local mode remains loopback-only.
  Zone-level Drain/preStop, HPA, PDB and replica scaling are not implemented.
  See `../evidence/2026-08-05-k8s-fixed-dual-zone.md`.
- Friend functionality was reviewed on 2026-08-05 but has no product code yet.
  The accepted design uses an authoritative `FriendRelation`, repairable
  FriendList projections, Tcaplus Sagas, activation-scoped public-farm epochs,
  full game-internal gRPC migration and HMAC-authenticated Metadata. Chapter
  two will contain add-friend, steal-crop and apply-pest-to-friend tasks;
  successful friendship advances both players. See
  `../plans/friend_design_plan/01-FriendSvr详细设计.md` through
  `../plans/friend_design_plan/06-分阶段实施方案.md`.
- Assignment algorithm V1 uses deterministic SHA-256 Rendezvous scoring over
  `shard_id` and stable `zone_id`. Gate and Zone do not treat that calculation
  as authority; only the Coordinator's committed Route with Zone, endpoint,
  epoch, route version, state, Lease and map version is routable.
- Gate forwards trusted Shard/Zone/epoch/version metadata. Zone recomputes the
  target player's Shard and rejects wrong Shard, wrong Zone, stale epoch,
  non-`ACTIVE` state or expired Lease before Actor activation.
- A five-process dual-Zone E2E routed `player_id=2/shard=1631` to Zone A and
  `player_id=1/shard=2066` to Zone B, kept the other player isolated after one
  purchase, rejected a direct wrong-Zone command with `409 NOT_OWNER`, and
  observed zero Coordinator single-Shard lookups during ordinary commands.
- Gate cache tests cover immutable Snapshot warmup, concurrent miss collapse,
  conditional route-version invalidation and one same-`request_id` retry after
  `NOT_OWNER`.
- The dual-Zone prototype now supports loopback-triggered migration of an
  inactive Shard. Zone takes an exclusive per-Shard execution gate, blocks new
  commands, rejects migration if any Player Actor is active, and otherwise
  allows Coordinator to commit `PREPARING` and `ACTIVE` with epoch increment.
- Actor epoch is activation-scoped and now flows through snapshots, command
  results, idempotency records, pending Outbox, checkpoints and maturity Push.
  The migration E2E moved `player_id=7/shard=3552` from Zone A epoch 1 to Zone B
  epoch 2; Gate refreshed exactly one stale cached Route and the snapshot
  completed on B. An active Shard migration was rejected without changing its
  Route.
- MySQL mode now supports controlled active-Shard migration. Zone first blocks
  new commands; Coordinator commits `PREPARING`; the old Zone excludes
  command, maturity and background-flush races, settles and final-flushes every
  active Actor, returns a durable manifest and evicts only after all succeed.
  Coordinator then advances the exact transition-bound MySQL Fence, the target
  rewrites and validates those checkpoints at the new epoch, and only then
  commits `ACTIVE`.
- Fence CAS and target preparation are idempotent. MySQL mode persists
  per-Shard migration progress through drain, Fence and target preparation.
  Coordinator restart rebuilds `ACTIVE` routes from fences, overlays open
  `PREPARING` fail-closed, and exposes loopback inspect/continue/abandon.
  Abandon before Fence restores the source Owner and burns the prepared
  epoch; abandon after Fence is refused. Before `PREPARING`, failure resumes
  the old Owner; after it, failure remains non-routable and never reuses the
  epoch.
- R3 now has a first local protocol benchmark scaffold:
  `server/cmd/benchrunner` creates isolated `bench_` accounts, establishes the
  actual HTTP/CSRF/Ticket/Protobuf WebSocket path, and measures closed-loop
  `GET_PLAYER_SNAPSHOT` latency for configurable 1–100 virtual users. It
  writes JSON, CSV and Markdown under ignored `benchmark/results/`, persists
  each completed stage, classifies errors and stops a virtual user after its
  first failure.
- The MySQL-backed snapshot baseline used 10-second warmup and 60-second
  samples. Successful QPS for 1/10/25/50/100 virtual users was respectively
  3,094.83 / 13,250.00 / 15,046.84 / 16,080.84 / 13,846.43, with zero errors
  after the fix. P99 was 1.029 / 2.090 / 4.523 / 9.225 / 23.256 ms. The
  observed throughput knee was 50 users on this host.
- R3 exposed a Gate-to-Zone HTTP pool defect: Go's default client retained too
  few idle connections and could select connections after Zone's shorter
  server idle timeout, producing six repeatable `SERVICE_UNAVAILABLE` results.
  Gate now allows 64 idle connections per host and retires them at 20 seconds,
  before Zone's 30-second close. Local failure counters remained zero across
  the post-fix matrix. This remains a single-host read-path baseline, not a
  production or 30-million-DAU claim. Actor contention, Push, Dirty, CPU and
  memory measurement remain next. See
  `../evidence/2026-08-03-r3-snapshot-read-baseline.md`.

Work order for this milestone:

1. HTTP, WebSocket, error/idempotency, data-model and minimum reward-mail event contracts are frozen and have one bounded implementation baseline.
2. Materialize the contracts as shared Protobuf and generated Go/TypeScript types.
3. Build the smallest Go + Vue 3 skeleton and prove `register/login -> ws_ticket -> AUTH -> GET_PLAYER_SNAPSHOT -> RESPONSE`.
4. Record repeatable evidence; do not expand friends, multiplayer or full mail UI.

## Actual prototype data ownership

With neither `STORAGE_MODE=tcaplus` nor `MYSQL_DSN`, the runnable code
deliberately uses development-only in-memory adapters:

- LoginSvr stores accounts, Argon2id password hashes, Sessions, CSRF records and one-time tickets in process-local Go maps. Registration allocates a sequential `player_id`; restarting LoginSvr loses all of these records.
- Registration does not yet create a durable Player checkpoint and does not call Zone.
- ZoneSvr stores one lazily created Player Actor per `player_id` in a process-local map. The first player command creates the development state with 10 coins, one basic fertilizer, four empty plots and chapter-one tasks.
- `GET_PLAYER_SNAPSHOT` is routed by Gate through its locally cached committed
  Route, executes on the selected Zone's Player Actor mailbox and projects the
  snapshot from current Actor memory. The default mode still uses one local
  Zone; `static-dual-zone` uses two independent Actor runtimes, backed either
  by process memory alone or by assigned-Fence MySQL checkpoints.
- Gate keeps authenticated player subscriptions in process memory. Online
  maturity travels from Zone to Gate through HMAC-authenticated Unary gRPC and
  is forwarded as a Protobuf Push; reconnect or any detected version gap uses a
  fresh snapshot rather than replaying Push history.
- `GET_SHOP` is routed to Zone and reads the pinned global configuration snapshot without activating a Player Actor.
- Coordinator route state is also process-local. In this mode, Dirty
  writeback, database Fences and restart recovery are not implemented.
- `deploy/migrations/000001_platform.up.sql` creates the migration ledger.

With `STORAGE_MODE=tcaplus`:

- Login uses durable PlayerIdCounter CAS, account provisioning Saga and
  Session generation;
- Zone loads and saves Player checkpoints with physical-version plus logical
  revision CAS, exact ShardFence validation and PlayerOutbox reconciliation;
- Coordinator persists ShardFence and MigrationProgress and hydrates advanced
  routes after restart;
- no `MYSQL_DSN` is accepted;
- the live eight-table environment passes pure-Tcaplus five-process and kind
  fixed-dual-Zone owner-loop/migration checks.

With `MYSQL_DSN`, the new code path:

- uses `deploy/migrations/000002_auth_player_checkpoint.up.sql` for account, HTTP Session and Player checkpoint envelopes;
- uses `deploy/migrations/000003_local_shard_fences.up.sql` to bootstrap 4096
  epoch-one Fence rows; static dual-Zone startup can atomically align those
  untouched rows to the committed Zone A/B assignment;
- uses `deploy/migrations/000005_shard_migration_progress.up.sql` for open or
  abandoned migration progress; completed migrations delete the OPEN row;
- makes registration externally atomic by locking the player's assigned Fence
  and committing the account, first Session and initial checkpoint in one
  local MySQL transaction;
- stores only the Session digest, not the raw cookie value;
- validates the deterministic checkpoint blob, SHA-256 and relational envelope before activating a Zone Actor;
- fails Actor activation instead of silently creating default state when a configured checkpoint load fails;
- executes `BUY_SEEDS`, `PLANT`, `APPLY_FERTILIZER`, `HARVEST`, `SELL_CROP`, `CLAIM_CHAPTER_REWARD` and `CLEAN_PLOT` inside the Player Actor mailbox, retains their idempotency results and marks the aggregate Dirty;
- asynchronously writes the whole checkpoint under exact process-Zone and
  epoch Fence validation plus checkpoint-revision CAS, including atomic
  relational Outbox creation when a reward overflows inventory.

The single-Zone path has live MySQL registration-to-Actor-load and fresh-process
restart evidence for the complete server owner loop at `player_seq=8`. The
static dual-Zone path has live active migration evidence: `player_id=14`,
Shard 3371 moved from Zone A epoch one to Zone B epoch two, preserved its first
write and persisted a second write on B at `player_seq=2`. A direct old-Zone
command and a delayed Zone-A checkpoint write were rejected. Unused WS tickets
and CSRF nonce records remain process-local by ADR-0010 even when MySQL stores
accounts and Sessions; Login restart requires a new CSRF bootstrap and ticket
(or a fresh login). Online Actors remain process-local. Coordinator routes are
rebuilt from fences plus durable migration progress on MySQL restart. Live
reward-overflow Outbox insertion, abnormal termination inside the Dirty-loss
window, restart while still crossing an active effect/maturity boundary and
multi-Actor batching remain unverified.

The static bootstrap is deliberately mode-specific: after `zone-local` Fences
are converted to Zone A/B, that database must not be reused for local
single-Zone MySQL writes without a separately designed safe conversion.

The auth DDL and local values `AUTO_INCREMENT player_id`, `db_shard_id = 0`, initial `checkpoint_revision = 1`, `owner_epoch = 1`, four empty plots, seed quote `(shop_entry_id=5001, item_id=1001, unit_price=2, price_version=8)`, crop `(crop_id=2001, crop_item_id=1002, maturity=100, rate=1, base_yield=3)`, and fertilizer `(item_id=1, modifier=+0.5, duration=60s)` are proposed implementation conventions, not accepted contract decisions.

## Product code and evidence state

- The backend and H5 are complete for the bounded single-player/fixed-dual-Zone
  prototype, but are not production-complete.
- The architecture and first single-player business loop have initial accepted documents.
- `frontend/src/assets/art/` now contains an engine-neutral 16x16 pixel-art
  workspace, a business-mapped inventory, source/license ledger, 30
  project-owned runtime placeholder PNGs, a manifest, contact sheets, and a
  standard-library validator. These are development placeholders, not accepted
  final art.
- First-stage HTTP, WebSocket, idempotency/error, logical data-model and reward-mail event contracts are frozen under `../contracts/`, with complete `.zh-CN.md` reading mirrors.
- The bounded first-stage implementation plan and MT progress-sync outline are present.
- Shared `.proto`, generated Go/TypeScript types, platform helpers, four Go processes, the H5 snapshot client and initial local deployment files exist.
- `../evidence/2026-07-30-protobuf-toolchain.md` records generation/round-trip evidence. `../evidence/2026-07-31-authenticated-snapshot-e2e.md` records one passing loopback multi-process command path using explicit in-memory development adapters.
- `../evidence/2026-07-31-browser-manual-smoke.md` records the owner-confirmed browser smoke and the CSRF defect found during that run.
- `../evidence/2026-07-31-mysql-registration-checkpoint-unit.md` records mocked-SQL atomic-registration, rollback, checkpoint-integrity and Actor-activation tests.
- `../evidence/2026-07-31-mysql-authenticated-snapshot-e2e.md` records one live MySQL 8.4.11 registration transaction and cross-process checkpoint load.
- `../evidence/2026-07-31-mysql-restart-recovery-e2e.md` records passing command replays through `CLAIM_CHAPTER_REWARD`, online maturity, Dirty flush and fresh-process recovery at `player_seq=7`.
- `../evidence/2026-07-31-zone-config-get-shop-e2e.md` records atomic local snapshot replacement and a passing four-process `GET_SHOP` path.
- `../evidence/2026-07-31-growth-and-maturity-tests.md` records exact fixed-point, clock-rollback, large-intermediate, activation-time and online maturity tests.
- `../evidence/2026-07-31-maturity-push-e2e.md` records a passing 72-second four-process run through buy, plant, fertilizer and natural `MATURED` Push at `player_seq=4`, plus Gate snapshot-buffer unit coverage.
- `../evidence/2026-07-31-harvest-e2e.md` records all-or-nothing warehouse-limit tests, checkpoint serialization and a passing live `MATURED -> HARVEST` flow at `player_seq=5`.
- `../evidence/2026-07-31-sell-crop-e2e.md` records quantity/sell-all, stale-price, inventory, chapter-status checkpoint tests and fresh-process MySQL recovery at `player_seq=6`.
- `../evidence/2026-07-31-claim-chapter-reward-e2e.md` records normal reward claim, full-warehouse pending mail Outbox, atomic MySQL writer unit coverage and the live in-memory `player_seq=7` flow.
- `../evidence/2026-07-31-clean-plot-e2e.md` records cleanup preconditions, complete field reset, retained replay and live in-memory/MySQL server-loop completion with fresh-process recovery at `player_seq=8`.
- `../evidence/2026-07-31-h5-farm-loop-browser.md` records the browser-driven H5 owner loop, maturity Push, final state and 320-pixel layout check.
- `../evidence/2026-08-03-four-plot-tools.md` records the four-plot, tool-driven and quantity-control implementation plus its static and browser verification.
- `../evidence/2026-08-03-dual-zone-routing.md` records deterministic placement,
  Gate cache behavior, wrong-Owner rejection and the passing five-process
  memory-only dual-Zone E2E.
- `../evidence/2026-08-03-manual-inactive-shard-migration.md` records
  per-Shard drain exclusion, epoch-two stale-cache recovery and active-Shard
  migration refusal.
- `../evidence/2026-08-03-static-dual-zone-mysql-fence.md` records the
  verified epoch-one Fence alignment and one persisted write through each
  Zone.
- `../evidence/2026-08-03-active-shard-mysql-migration.md` records final Actor
  flush, epoch-two Fence transfer, Gate recovery, target persistence and stale
  old-Owner rejection.
- `../evidence/2026-08-03-coordinator-preparing-recovery.md` records durable
  migration progress, fail-closed PREPARING overlay, continue/abandon controls
  and post-migration Coordinator Fence hydration after restart.
- `../evidence/2026-08-03-ws-ticket-restart-boundary.md` and ADR-0010 freeze
  unused WS tickets/CSRF as process-local across Login restart.
- Automated browser behavior in MySQL mode, distributed/retriable Push delivery, abnormal Dirty-window loss, availability and performance remain unverified.
- A loopback-only local test platform exists under `tests/catalog.json` and
  `server/cmd/testrunner`. It wraps existing Go/PowerShell checks with tiered
  safety controls; platform history does not replace `docs/evidence/`.
- `../evidence/2026-08-05-pure-tcaplus-runtime-gate.md` records the no-MySQL
  account, checkpoint, migration and restart acceptance gate.
- `../evidence/2026-08-05-k8s-fixed-dual-zone.md` records the five-Deployment
  kind cluster and passing live dual-Zone owner-loop/migration E2E.
- `../evidence/2026-08-06-friend-phase-1-grpc.md` records HMAC interceptor
  rejection tests plus passing local and kind gRPC dual-Zone E2Es.
- `../evidence/2026-08-06-friend-phase-2-friendsvr.md` records FriendSvr,
  FriendLinkSaga, Zone task credit and friend-table deploy wiring.
- `../evidence/2026-08-06-friend-phase-3-visit.md` records Zone/Gate visit-
  session wiring, the new gRPC gateway adapters, `friend_rpc_test.go`
  RPC-argument/authorization coverage, gateway routing tests and the
  minimal H5 friends/visit panel.
- `../evidence/2026-08-06-friend-phase-4-farmview.md` records the
  `farmview.Broadcaster` fan-out, the Runtime `farm_view_seq` hook on public
  plot mutations only, the Gate `PublishFarmViewPatch`/`push_hub.go`
  validation and the H5 epoch/seq-gap recovery logic in `App.vue`.
- `../evidence/2026-08-06-friend-phase-5-steal-saga.md` records the frozen
  per-plot steal fields, `player.CanSteal`, the synchronous
  Reserve/Apply/Commit/Release Actor steps, the new
  `server/internal/interaction` Saga/Tcaplus-store/reconciler package (with
  all three crash-window recovery tests), the Zone
  `ExecuteFriendAction`/`ApplyVisitorAction` RPC wiring, Gate routing and
  the minimal H5 steal button.

## Next actions

Final delivery sprint **01**–**03**, **04-1**, **04-2**, **04-3A–F**, and
**04-4** (code + unit tests) are done. Remaining:

1. Create Tcaplus table `FirstFriendReward`, redeploy Friend/Mail, then run
   the dual-browser invite E2E checklist in
   `../plans/final_delivery_sprint/04-基础业务补齐/04-4-分享链接自动加好友.md`
   Task 5;
2. Stage E2E for mail/notification → `docs/evidence/2026-08-12-mail-notification-e2e.md`
   (read `../plans/final_delivery_sprint/04-基础业务补齐/04-3-邮件与通知总阶段.md`).

Friend prototype vertical (phases 0–7) remains complete for the frozen
scope in `../plans/friend_design_plan/06-分阶段实施方案.md`.

1. Owner: Tcaplus `FirstFriendReward` + dual-browser invite E2E;
2. Next sprint coding task: **04-3 stage E2E** (mail + gift + claim + red-dot)
   unless invite E2E surfaces bugs first;
3. Keep `go test ./...` green when touching Gate/Zone/Friend/Info/Mail paths;
4. Keep `cd web && npm run typecheck && npm test` green when touching H5
   (typecheck must use `tsconfig.app.json`).

Evidence:

- `../evidence/2026-08-12-friend-farm-pet-badge.md`
- `../evidence/2026-08-12-shop-seed-rows-and-guard-dog.md`
- `../evidence/2026-08-12-per-crop-mature-sprites.md`
- `../evidence/2026-08-11-farm-16-plots-grass-ui.md`
- `../evidence/2026-08-12-local-friend-invite-link.md`
- `../evidence/2026-08-11-k8s-redeploy-mail-info.md`
- `../bugs/2026-08-11-gift-outbox-activation-revision-mismatch.md`
- `../evidence/2026-08-12-multi-crop-steal.md`
- `../evidence/2026-08-12-h5-mail-red-dot.md`
- `../evidence/2026-08-12-mail-claim-saga.md`
- `../evidence/2026-08-12-friend-gift-outbox.md`
- `../evidence/2026-08-12-mailsvr-query.md`
- `../evidence/2026-08-12-infosvr-red-dot.md`
- `../evidence/2026-08-12-zone-connection-push.md`
- `../evidence/2026-08-11-h5-shell-redesign.md`
- `../evidence/2026-08-11-career-compendium-multi-crop.md`
- `../evidence/2026-08-11-pet-guard-e2e.md`
- `../evidence/2026-08-10-farm-broadcast-separation.md`
- `../evidence/2026-08-10-zone-initial-player-checkpoint.md`
- `../evidence/2026-08-10-actor-register-before-load.md`
- `../evidence/2026-08-07-friend-interaction-e2e.md`

Manual owner checkpoints:

- rebuild/redeploy **all eight** images into kind when exercising cluster
  demos (`login` `gate` `coordinator` `zone` `friend` `info` `mail`;
  `CLIENT_CONFIG_PUBLIC_URL` must stay aligned with Login's browser URL);
- ensure `classic-farm-internal-rpc` includes `MAIL_ADMIN_TOKEN` before Mail
  pods become Ready;
- run `./tests/e2e/run-friend-interaction.sh` after friend/Gate/Zone changes.

## AI memory and handoff rule

- This file is the short, authoritative mutable handoff. Update it only when current state or next actions materially change.
- `PROJECT.md` stores stable scope and constraints.
- `docs/decisions/` stores chronological decision evolution; it is not the current-state summary.
- `docs/ai-workflow/` stores traceability about what AI and the owner did; it cannot override current architecture, contracts, or evidence.
- Obsidian and `ai-context` may keep learning notes and pointers, but must not override repository truth.
- When switching among Codex, CodeBuddy, or Claude, provide the reading order above and only task-specific supporting files.
