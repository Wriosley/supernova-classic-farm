# 2026-08-18: Sync gift mail direct submission

## Change

- `server/internal/player/gift.go` no longer appends `PendingOutbox` records for
  `SEND_FRIEND_GIFT`.
- `zone` injects a direct `MailSvr.CreateGiftMail` client into the player
  runtime.
- `server/internal/player/runtime.go` now suspends the actor mailbox while the
  mail RPC is in flight, then resumes the same mailbox to commit the sender
  state.
- `server/cmd/zone/grpc_server.go` no longer forwards gift responses to a zone
  outbox notifier.

## Verification

Ran from `server/`:

- `GOCACHE=/tmp/classic-farm-go-cache go test -count=1 ./internal/player -run 'TestSendFriendGift'`
- `GOCACHE=/tmp/classic-farm-go-cache go test -count=1 ./cmd/zone`

Both packages passed.
