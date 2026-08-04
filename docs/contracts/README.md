# Contracts

Contracts are the precise inputs to implementation. They define HTTP requests and responses, WebSocket messages, domain events, tables and indexes, error semantics, and idempotency behavior.

Accepted contracts:

- `websocket-protocol.md`: Protobuf WebSocket envelope, authentication, commands, responses, snapshots, patches, Push, reconnect and close behavior.
- `idempotency-and-errors.md`: request identity, retained results, retries, error codes and abnormal-recovery limits.
- `http-api.md`: registration, login, Session, one-time WS tickets, Gateway discovery and client-config bootstrap.
- `data-model.md`: Player checkpoint, state versions, ShardMap, fences, Dirty batches, idempotency persistence and Outbox storage.
- `event-contracts.md`: minimum reward-mail Outbox event, relay and consumer-deduplication behavior.

Chinese reading copies:

- `websocket-protocol.zh-CN.md`
- `idempotency-and-errors.zh-CN.md`
- `http-api.zh-CN.md`
- `data-model.zh-CN.md`
- `event-contracts.zh-CN.md`

Do not place unresolved alternatives in normative contracts.

Architecture explains why components collaborate. Module documents explain ownership and behavior. Contracts specify the exact formats code must follow.
