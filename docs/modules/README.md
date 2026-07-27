# Modules

Each module document owns one business boundary and must state its scope, data ownership, shard key, capabilities, invariants, normal flows, transaction boundaries, failure recovery, security, capacity, and tests.

Planned module documents are account, farm, asset-and-shop, friend, task, mail, pet, and collection. Create a module file only when its current design can be extracted from confirmed rules without inventing unresolved behavior.

Cross-module topology belongs in `../architecture/`. Exact HTTP, WebSocket, event, data, error, and idempotency formats belong in `../contracts/`.
