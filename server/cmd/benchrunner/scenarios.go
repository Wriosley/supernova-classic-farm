package main

import (
	"errors"
	"fmt"
	"math/rand"
	"sync"
	"time"

	wsv1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/ws"
	plotv1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/ws/plot"
)

// ============================================================
// 场景分发
// ============================================================

func runScenario(opts options, concurrency int) (result, []sample, error) {
	switch opts.scenario {
	case "snapshot":
		return runSnapshot(opts, concurrency)
	case "player_loop":
		return runPlayerLoop(opts, concurrency)
	case "connect_hold":
		return runConnectHold(opts, concurrency)
	case "friend_interaction":
		return runFriendInteraction(opts, concurrency)
	case "mail_operations":
		return runMailOperations(opts, concurrency)
	case "mixed":
		return runMixed(opts, concurrency)
	default:
		return result{}, nil, fmt.Errorf("unsupported scenario %q", opts.scenario)
	}
}

func validScenario(name string) bool {
	switch name {
	case "snapshot", "player_loop", "connect_hold", "friend_interaction", "mail_operations", "mixed":
		return true
	}
	return false
}

// ============================================================
// 通用请求 / 错误
// ============================================================

func (c *benchClient) sendRequest(action wsv1.Action, targetPlayerID uint64, payload any) (*wsv1.WsEnvelope, time.Duration, error) {
	requestID := newUUID()
	start := time.Now()
	envelope := &wsv1.WsEnvelope{
		ProtocolVersion: 1,
		MessageKind:     wsv1.MessageKind_REQUEST,
		Action:          action,
		RequestId:       requestID,
		TargetPlayerId:  targetPlayerID,
	}
	if err := assignPayload(envelope, payload); err != nil {
		return nil, 0, err
	}
	if err := c.write(envelope); err != nil {
		return nil, 0, err
	}
	// Loop reading: skip PUSH messages until we get the RESPONSE matching request_id.
	for {
		response, err := c.read()
		if err != nil {
			return nil, 0, err
		}
		if response.GetError() != nil {
			return response, time.Since(start), &businessError{code: response.GetError().GetCode(), action: action}
		}
		// Skip PUSH messages (e.g. RED_DOT_CHANGED, FARM_VIEW_PATCH, etc.)
		if response.GetMessageKind() == wsv1.MessageKind_PUSH {
			continue
		}
		if response.GetAction() == action && response.GetRequestId() == requestID {
			return response, time.Since(start), nil
		}
		// Mismatched response_id or action; skip and keep reading.
	}
}

type businessError struct {
	code   wsv1.ErrorCode
	action wsv1.Action
}

func (e *businessError) Error() string {
	return fmt.Sprintf("business_reject: action=%v code=%v", e.action, e.code)
}

func isBusinessError(err error) bool {
	var be *businessError
	return errors.As(err, &be)
}

// runWorkers runs one stepFn per virtual user until duration elapses.
// Business rejects are recorded but do not abort the worker; only hard errors stop it.
func runWorkers(opts options, concurrency, workers int, stepFn func(worker int) (time.Duration, error)) (result, []sample, error) {
	start := time.Now()
	deadline := start.Add(opts.duration)
	var mu sync.Mutex
	latencies := make([]int64, 0)
	samples := make([]sample, 0)
	var successes, failures, dropped int64
	errorKinds := make(map[string]int64)
	var wg sync.WaitGroup
	for worker := 0; worker < workers; worker++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			for time.Now().Before(deadline) {
				latency, err := stepFn(worker)
				mu.Lock()
				switch {
				case err != nil && isBusinessError(err):
					errorKinds["business_reject"]++
				case err != nil:
					failures++
					errorKinds[classifyError(err)]++
					mu.Unlock()
					return
				default:
					successes++
					if latency > 0 && len(latencies) < opts.maxSamples {
						us := latency.Microseconds()
						latencies = append(latencies, us)
						samples = append(samples, sample{Concurrency: concurrency, LatencyUS: us})
					} else if latency > 0 {
						dropped++
					}
				}
				mu.Unlock()
			}
		}(worker)
	}
	wg.Wait()
	elapsed := time.Since(start)
	sortLatencies(latencies)
	return result{
		Concurrency: concurrency, DurationMS: elapsed.Milliseconds(), SuccessCount: successes,
		ErrorCount: failures, ErrorKinds: errorKinds, DroppedSampleCount: dropped,
		QPS:   float64(successes) / elapsed.Seconds(),
		P50US: percentile(latencies, 50), P95US: percentile(latencies, 95),
		P99US: percentile(latencies, 99), MaxUS: percentile(latencies, 100),
	}, samples, nil
}

func loadSnapshot(client *benchClient) (*wsv1.PlayerSnapshot, time.Duration, error) {
	resp, lat, err := client.sendRequest(wsv1.Action_GET_PLAYER_SNAPSHOT, client.playerID, &wsv1.WsEnvelope_GetPlayerSnapshotRequest{
		GetPlayerSnapshotRequest: &wsv1.GetPlayerSnapshotRequest{},
	})
	if err != nil {
		return nil, lat, err
	}
	return resp.GetGetPlayerSnapshotResponse().GetSnapshot(), lat, nil
}

// ============================================================
// 场景 2: player_loop — 可持续写路径
// ============================================================

type playerLoopState struct {
	client       *benchClient
	seedItemID   uint32
	cropItemID   uint32
	shopEntryID  uint32
	priceVersion uint64
	sellVersion  uint64
	seedPrice    int64
	maturityWait time.Duration
}

// initPlayerLoopStates resolves shop and crop metadata for every client in
// parallel so setup scales with -connect-workers.
func initPlayerLoopStates(opts options, clients []*benchClient) ([]*playerLoopState, error) {
	states := make([]*playerLoopState, len(clients))
	limit := opts.connectWorkers
	if limit <= 0 {
		limit = 1
	}
	semaphore := make(chan struct{}, limit)
	var group sync.WaitGroup
	var mu sync.Mutex
	var firstErr error
	for index, client := range clients {
		group.Add(1)
		go func(index int, client *benchClient) {
			defer group.Done()
			semaphore <- struct{}{}
			defer func() { <-semaphore }()
			state, err := initPlayerLoopState(client)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				if firstErr == nil {
					firstErr = fmt.Errorf("init state for player %d: %w", client.playerID, err)
				}
				return
			}
			states[index] = state
		}(index, client)
	}
	group.Wait()
	if firstErr != nil {
		return nil, firstErr
	}
	return states, nil
}

func runPlayerLoop(opts options, concurrency int) (result, []sample, error) {
	clients, err := authenticateAll(opts, benchAccountNames(opts.runID, concurrency))
	if err != nil {
		return result{}, nil, err
	}
	defer closeClients(clients)
	states, err := initPlayerLoopStates(opts, clients)
	if err != nil {
		return result{}, nil, err
	}

	fmt.Printf("player_loop: planting + waiting for maturity (up to ~%s)\n", states[0].maturityWait)
	if err := preparePlayerFarms(states); err != nil {
		return result{}, nil, err
	}

	warmupUntil := time.Now().Add(opts.warmup)
	for time.Now().Before(warmupUntil) {
		for _, state := range states {
			_, _ = playerLoopStep(state)
		}
	}
	return runWorkers(opts, concurrency, len(states), func(worker int) (time.Duration, error) {
		return playerLoopStep(states[worker])
	})
}

func initPlayerLoopState(client *benchClient) (*playerLoopState, error) {
	state := &playerLoopState{client: client}
	resp, _, err := client.sendRequest(wsv1.Action_GET_SHOP, client.playerID, &wsv1.WsEnvelope_GetShopRequest{
		GetShopRequest: &wsv1.GetShopRequest{},
	})
	if err != nil {
		return nil, err
	}
	shop := resp.GetGetShopResponse()
	for _, entry := range shop.GetEntries() {
		if entry.GetItemId() == 1001 && entry.GetEnabled() {
			state.shopEntryID = entry.GetShopEntryId()
			state.seedItemID = entry.GetItemId()
			state.priceVersion = entry.GetPriceVersion()
			state.seedPrice = entry.GetUnitPrice()
			break
		}
	}
	if state.shopEntryID == 0 {
		return nil, fmt.Errorf("no seed shop entry found")
	}
	for _, crop := range shop.GetCrops() {
		if crop.GetSeedItemId() != state.seedItemID {
			continue
		}
		state.cropItemID = crop.GetCropItemId()
		state.sellVersion = crop.GetSellPriceVersion()
		sec := crop.GetMaturitySeconds()
		if sec == 0 {
			sec = 100
		}
		state.maturityWait = time.Duration(sec)*time.Second + 5*time.Second
		break
	}
	if state.cropItemID == 0 {
		state.cropItemID = 1002
		state.sellVersion = 9
		state.maturityWait = 105 * time.Second
	}
	return state, nil
}

func preparePlayerFarms(states []*playerLoopState) error {
	var wg sync.WaitGroup
	errCh := make(chan error, len(states))
	for _, state := range states {
		wg.Add(1)
		go func(state *playerLoopState) {
			defer wg.Done()
			if err := plantEmptyPlots(state); err != nil {
				errCh <- err
			}
		}(state)
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		if err != nil {
			return err
		}
	}
	wait := states[0].maturityWait
	deadline := time.Now().Add(wait)
	for time.Now().Before(deadline) {
		ready := 0
		for _, state := range states {
			snap, _, err := loadSnapshot(state.client)
			if err != nil {
				continue
			}
			for _, plot := range snap.GetPlots() {
				if plot.GetPlotState() == plotv1.PlotState_MATURE {
					ready++
					break
				}
			}
		}
		if ready == len(states) {
			return nil
		}
		time.Sleep(2 * time.Second)
	}
	return nil
}

func plantEmptyPlots(state *playerLoopState) error {
	snap, _, err := loadSnapshot(state.client)
	if err != nil {
		return err
	}
	coins := snap.GetCoinBalance()
	for _, plot := range snap.GetPlots() {
		switch plot.GetPlotState() {
		case plotv1.PlotState_NEED_CLEANUP:
			_, _, err := state.client.sendRequest(wsv1.Action_CLEAN_PLOT, state.client.playerID, &wsv1.WsEnvelope_CleanPlotRequest{
				CleanPlotRequest: &wsv1.CleanPlotRequest{PlotId: plot.GetPlotId()},
			})
			if err != nil && !isBusinessError(err) {
				return err
			}
		case plotv1.PlotState_EMPTY:
			if coins < state.seedPrice {
				return nil
			}
			_, _, err := state.client.sendRequest(wsv1.Action_BUY_SEEDS, state.client.playerID, &wsv1.WsEnvelope_BuySeedsRequest{
				BuySeedsRequest: &wsv1.BuySeedsRequest{
					ShopEntryId: state.shopEntryID, Quantity: 1, ExpectedPriceVersion: state.priceVersion,
				},
			})
			if err != nil && !isBusinessError(err) {
				return err
			}
			if err == nil {
				coins -= state.seedPrice
			}
			_, _, err = state.client.sendRequest(wsv1.Action_PLANT, state.client.playerID, &wsv1.WsEnvelope_PlantRequest{
				PlantRequest: &wsv1.PlantRequest{PlotId: plot.GetPlotId(), SeedItemId: state.seedItemID},
			})
			if err != nil && !isBusinessError(err) {
				return err
			}
		}
	}
	return nil
}

func playerLoopStep(state *playerLoopState) (time.Duration, error) {
	var total time.Duration
	wrote := false

	snap, lat, err := loadSnapshot(state.client)
	total += lat
	if err != nil {
		return total, err
	}

	for _, plot := range snap.GetPlots() {
		if plot.GetPlotState() != plotv1.PlotState_NEED_CLEANUP {
			continue
		}
		_, cleanLat, cleanErr := state.client.sendRequest(wsv1.Action_CLEAN_PLOT, state.client.playerID, &wsv1.WsEnvelope_CleanPlotRequest{
			CleanPlotRequest: &wsv1.CleanPlotRequest{PlotId: plot.GetPlotId()},
		})
		total += cleanLat
		if cleanErr != nil && !isBusinessError(cleanErr) {
			return total, cleanErr
		}
		if cleanErr == nil {
			wrote = true
		}
	}

	for _, plot := range snap.GetPlots() {
		if plot.GetPlotState() != plotv1.PlotState_MATURE {
			continue
		}
		_, harvestLat, harvestErr := state.client.sendRequest(wsv1.Action_HARVEST, state.client.playerID, &wsv1.WsEnvelope_HarvestRequest{
			HarvestRequest: &wsv1.HarvestRequest{PlotId: plot.GetPlotId()},
		})
		total += harvestLat
		if harvestErr != nil && !isBusinessError(harvestErr) {
			return total, harvestErr
		}
		if harvestErr == nil {
			wrote = true
		}
	}

	_, sellLat, sellErr := state.client.sendRequest(wsv1.Action_SELL_CROP, state.client.playerID, &wsv1.WsEnvelope_SellCropRequest{
		SellCropRequest: &wsv1.SellCropRequest{
			CropItemId:           state.cropItemID,
			ExpectedPriceVersion: state.sellVersion,
			Amount:               &wsv1.SellCropRequest_SellAll{SellAll: true},
		},
	})
	total += sellLat
	if sellErr != nil && !isBusinessError(sellErr) {
		return total, sellErr
	}
	if sellErr == nil {
		wrote = true
	}

	snap, lat, err = loadSnapshot(state.client)
	total += lat
	if err != nil {
		return total, err
	}
	coins := snap.GetCoinBalance()
	for _, plot := range snap.GetPlots() {
		if plot.GetPlotState() != plotv1.PlotState_EMPTY || coins < state.seedPrice {
			continue
		}
		_, buyLat, buyErr := state.client.sendRequest(wsv1.Action_BUY_SEEDS, state.client.playerID, &wsv1.WsEnvelope_BuySeedsRequest{
			BuySeedsRequest: &wsv1.BuySeedsRequest{
				ShopEntryId: state.shopEntryID, Quantity: 1, ExpectedPriceVersion: state.priceVersion,
			},
		})
		total += buyLat
		if buyErr != nil && !isBusinessError(buyErr) {
			return total, buyErr
		}
		if buyErr != nil {
			break
		}
		coins -= state.seedPrice
		wrote = true
		_, plantLat, plantErr := state.client.sendRequest(wsv1.Action_PLANT, state.client.playerID, &wsv1.WsEnvelope_PlantRequest{
			PlantRequest: &wsv1.PlantRequest{PlotId: plot.GetPlotId(), SeedItemId: state.seedItemID},
		})
		total += plantLat
		if plantErr != nil && !isBusinessError(plantErr) {
			return total, plantErr
		}
		if plantErr == nil {
			wrote = true
		}
	}

	if !wrote {
		time.Sleep(500 * time.Millisecond)
		return total, &businessError{code: wsv1.ErrorCode_CROP_NOT_MATURE, action: wsv1.Action_HARVEST}
	}
	return total, nil
}

// ============================================================
// 场景 3: connect_hold
// ============================================================

func runConnectHold(opts options, concurrency int) (result, []sample, error) {
	clients, err := authenticateAll(opts, benchAccountNames(opts.runID, concurrency))
	if err != nil {
		return result{}, nil, err
	}
	defer closeClients(clients)

	pingInterval := opts.pingInterval
	if pingInterval <= 0 {
		pingInterval = 30 * time.Second
	}
	return runWorkers(opts, concurrency, len(clients), func(worker int) (time.Duration, error) {
		latency, err := clients[worker].ping(uint64(time.Now().UnixNano()))
		if err != nil {
			return latency, err
		}
		wait := pingInterval
		time.Sleep(wait)
		return latency, nil
	})
}

func (c *benchClient) ping(pingID uint64) (time.Duration, error) {
	_, latency, err := c.sendRequest(wsv1.Action_PING, 0, &wsv1.WsEnvelope_PingRequest{
		PingRequest: &wsv1.PingRequest{PingId: pingID, ClientSentAtMs: time.Now().UnixMilli()},
	})
	return latency, err
}

// ============================================================
// 场景 4: friend_interaction — 进农场 + 心跳（可报告 QPS）
// ============================================================

type friendPair struct {
	visitor *benchClient
	owner   *benchClient
	visitID []byte
}

func runFriendInteraction(opts options, concurrency int) (result, []sample, error) {
	if concurrency%2 != 0 {
		return result{}, nil, fmt.Errorf("friend_interaction requires even concurrency (got %d)", concurrency)
	}
	pairCount := concurrency / 2
	pairs := make([]*friendPair, pairCount)
	limit := opts.connectWorkers
	if limit <= 0 {
		limit = 1
	}
	semaphore := make(chan struct{}, limit)
	var group sync.WaitGroup
	var mu sync.Mutex
	var firstErr error
	for index := 0; index < pairCount; index++ {
		group.Add(1)
		go func(index int) {
			defer group.Done()
			semaphore <- struct{}{}
			defer func() { <-semaphore }()
			mu.Lock()
			aborted := firstErr != nil
			mu.Unlock()
			if aborted {
				return
			}
			fail := func(err error) {
				mu.Lock()
				if firstErr == nil {
					firstErr = err
				}
				mu.Unlock()
			}
			visitorName := fmt.Sprintf("bench_%s_v%03d", opts.runID, index+1)
			ownerName := fmt.Sprintf("bench_%s_o%03d", opts.runID, index+1)
			visitor, err := authenticate(opts, visitorName)
			if err != nil {
				fail(fmt.Errorf("authenticate visitor %s: %w", visitorName, err))
				return
			}
			owner, err := authenticate(opts, ownerName)
			if err != nil {
				_ = visitor.conn.CloseNow()
				fail(fmt.Errorf("authenticate owner %s: %w", ownerName, err))
				return
			}
			if err := establishFriendship(visitor, owner); err != nil {
				_ = visitor.conn.CloseNow()
				_ = owner.conn.CloseNow()
				fail(fmt.Errorf("establish friendship %d: %w", index, err))
				return
			}
			visitID, err := enterFriendFarm(visitor, owner.playerID)
			if err != nil {
				_ = visitor.conn.CloseNow()
				_ = owner.conn.CloseNow()
				fail(fmt.Errorf("enter farm %d: %w", index, err))
				return
			}
			mu.Lock()
			pairs[index] = &friendPair{visitor: visitor, owner: owner, visitID: visitID}
			mu.Unlock()
		}(index)
	}
	group.Wait()
	if firstErr != nil {
		closeFriendPairs(pairs)
		return result{}, nil, firstErr
	}
	defer closeFriendPairs(pairs)

	return runWorkers(opts, concurrency, len(pairs), func(worker int) (time.Duration, error) {
		return friendVisitStep(pairs[worker])
	})
}

func friendVisitStep(pair *friendPair) (time.Duration, error) {
	var total time.Duration
	visitID, enterLat, err := enterFriendFarmTimed(pair.visitor, pair.owner.playerID)
	total += enterLat
	if err != nil {
		return total, err
	}
	pair.visitID = visitID
	hbLat, err := farmHeartbeat(pair.visitor, pair.owner.playerID, visitID)
	total += hbLat
	if err != nil {
		return total, err
	}
	return total, nil
}

func establishFriendship(visitor, owner *benchClient) error {
	resp, _, err := owner.sendRequest(wsv1.Action_CREATE_FRIEND_CODE, owner.playerID, &wsv1.WsEnvelope_CreateFriendCodeRequest{
		CreateFriendCodeRequest: &wsv1.CreateFriendCodeRequest{},
	})
	if err != nil {
		return fmt.Errorf("create friend code: %w", err)
	}
	code := resp.GetCreateFriendCodeResponse().GetCode()
	if code == "" {
		return fmt.Errorf("empty friend code")
	}
	_, _, err = visitor.sendRequest(wsv1.Action_REDEEM_FRIEND_CODE, visitor.playerID, &wsv1.WsEnvelope_RedeemFriendCodeRequest{
		RedeemFriendCodeRequest: &wsv1.RedeemFriendCodeRequest{Code: code},
	})
	if err != nil && !isBusinessError(err) {
		return fmt.Errorf("redeem friend code: %w", err)
	}
	return nil
}

func enterFriendFarm(visitor *benchClient, ownerPlayerID uint64) ([]byte, error) {
	id, _, err := enterFriendFarmTimed(visitor, ownerPlayerID)
	return id, err
}

func enterFriendFarmTimed(visitor *benchClient, ownerPlayerID uint64) ([]byte, time.Duration, error) {
	// TargetPlayerId must be the caller (visitor), not the owner.
	// OwnerPlayerId is passed in the payload.
	resp, lat, err := visitor.sendRequest(wsv1.Action_ENTER_FRIEND_FARM, visitor.playerID, &wsv1.WsEnvelope_EnterFriendFarmRequest{
		EnterFriendFarmRequest: &wsv1.EnterFriendFarmRequest{OwnerPlayerId: ownerPlayerID},
	})
	if err != nil {
		return nil, lat, err
	}
	return resp.GetEnterFriendFarmResponse().GetVisitId(), lat, nil
}

func farmHeartbeat(visitor *benchClient, ownerPlayerID uint64, visitID []byte) (time.Duration, error) {
	_, latency, err := visitor.sendRequest(wsv1.Action_FARM_HEARTBEAT, visitor.playerID, &wsv1.WsEnvelope_FarmHeartbeatRequest{
		FarmHeartbeatRequest: &wsv1.FarmHeartbeatRequest{OwnerPlayerId: ownerPlayerID, VisitId: visitID},
	})
	return latency, err
}

// ============================================================
// 场景 5: mail_operations — 预热送礼后 OPEN/CLAIM/CHECK
// ============================================================

type mailBenchState struct {
	receiver *benchClient
	sender   *benchClient
	cropID   uint32
}

func runMailOperations(opts options, concurrency int) (result, []sample, error) {
	if concurrency%2 != 0 {
		return result{}, nil, fmt.Errorf("mail_operations requires even concurrency (got %d)", concurrency)
	}
	pairCount := concurrency / 2
	states := make([]*mailBenchState, pairCount)
	type prepResult struct {
		index  int
		state  *mailBenchState
		err    error
	}
	results := make(chan prepResult, pairCount)
	var prepWG sync.WaitGroup
	fmt.Printf("mail_operations: warming %d gift pairs in parallel (maturity wait ~100s)\n", pairCount)
	for index := 0; index < pairCount; index++ {
		prepWG.Add(1)
		go func(index int) {
			defer prepWG.Done()
			receiverName := fmt.Sprintf("bench_%s_mr%03d", opts.runID, index+1)
			senderName := fmt.Sprintf("bench_%s_ms%03d", opts.runID, index+1)
			receiver, err := authenticate(opts, receiverName)
			if err != nil {
				results <- prepResult{index: index, err: fmt.Errorf("authenticate receiver %s: %w", receiverName, err)}
				return
			}
			sender, err := authenticate(opts, senderName)
			if err != nil {
				_ = receiver.conn.CloseNow()
				results <- prepResult{index: index, err: fmt.Errorf("authenticate sender %s: %w", senderName, err)}
				return
			}
			if err := establishFriendship(receiver, sender); err != nil {
				_ = receiver.conn.CloseNow()
				_ = sender.conn.CloseNow()
				results <- prepResult{index: index, err: fmt.Errorf("friendship %d: %w", index, err)}
				return
			}
			cropID, err := warmupGiftMail(sender, receiver)
			if err != nil {
				_ = receiver.conn.CloseNow()
				_ = sender.conn.CloseNow()
				results <- prepResult{index: index, err: fmt.Errorf("warmup gift %d: %w", index, err)}
				return
			}
			results <- prepResult{index: index, state: &mailBenchState{receiver: receiver, sender: sender, cropID: cropID}}
		}(index)
	}
	prepWG.Wait()
	close(results)
	for item := range results {
		if item.err != nil {
			closeMailStates(states)
			return result{}, nil, item.err
		}
		states[item.index] = item.state
	}
	defer closeMailStates(states)

	return runWorkers(opts, concurrency, len(states), func(worker int) (time.Duration, error) {
		return mailStep(states[worker])
	})
}

func warmupGiftMail(sender, receiver *benchClient) (uint32, error) {
	farm, err := initPlayerLoopState(sender)
	if err != nil {
		return 0, err
	}
	if err := plantEmptyPlots(farm); err != nil {
		return 0, err
	}
	deadline := time.Now().Add(farm.maturityWait)
	for time.Now().Before(deadline) {
		snap, _, err := loadSnapshot(sender)
		if err != nil {
			return 0, err
		}
		mature := false
		for _, plot := range snap.GetPlots() {
			if plot.GetPlotState() == plotv1.PlotState_MATURE {
				mature = true
				_, _, err := sender.sendRequest(wsv1.Action_HARVEST, sender.playerID, &wsv1.WsEnvelope_HarvestRequest{
					HarvestRequest: &wsv1.HarvestRequest{PlotId: plot.GetPlotId()},
				})
				if err != nil && !isBusinessError(err) {
					return 0, err
				}
			}
		}
		if mature {
			break
		}
		time.Sleep(2 * time.Second)
	}
	// Keep at least one crop unit for gifting; sell the rest for coins if needed.
	_, _, _ = sender.sendRequest(wsv1.Action_SELL_CROP, sender.playerID, &wsv1.WsEnvelope_SellCropRequest{
		SellCropRequest: &wsv1.SellCropRequest{
			CropItemId:           farm.cropItemID,
			ExpectedPriceVersion: farm.sellVersion,
			Amount:               &wsv1.SellCropRequest_Quantity{Quantity: 1},
		},
	})
	_, _, err = sender.sendRequest(wsv1.Action_SEND_FRIEND_GIFT, sender.playerID, &wsv1.WsEnvelope_SendFriendGiftRequest{
		SendFriendGiftRequest: &wsv1.SendFriendGiftRequest{
			RecipientPlayerId: receiver.playerID,
			CropItemId:        farm.cropItemID,
			Quantity:          1,
		},
	})
	if err != nil && !isBusinessError(err) {
		return 0, err
	}
	// Allow async mail relay a moment.
	time.Sleep(2 * time.Second)
	return farm.cropItemID, nil
}

func mailStep(state *mailBenchState) (time.Duration, error) {
	var total time.Duration
	mails, lat, err := openMailbox(state.receiver, 50, "")
	total += lat
	if err != nil {
		return total, err
	}
	for _, mail := range mails {
		if mail.GetClaimed() || len(mail.GetAttachments()) == 0 {
			continue
		}
		_, claimLat, claimErr := state.receiver.sendRequest(wsv1.Action_CLAIM_MAIL, state.receiver.playerID, &wsv1.WsEnvelope_ClaimMailRequest{
			ClaimMailRequest: &wsv1.ClaimMailRequest{MailId: mail.GetMailId()},
		})
		total += claimLat
		if claimErr != nil && !isBusinessError(claimErr) {
			return total, claimErr
		}
	}
	// Top up a gift periodically so CLAIM keeps having work.
	if len(mails) == 0 {
		_, giftLat, giftErr := state.sender.sendRequest(wsv1.Action_SEND_FRIEND_GIFT, state.sender.playerID, &wsv1.WsEnvelope_SendFriendGiftRequest{
			SendFriendGiftRequest: &wsv1.SendFriendGiftRequest{
				RecipientPlayerId: state.receiver.playerID,
				CropItemId:        state.cropID,
				Quantity:          1,
			},
		})
		total += giftLat
		if giftErr != nil && !isBusinessError(giftErr) {
			return total, giftErr
		}
	}
	_, indLat, err := state.receiver.sendRequest(wsv1.Action_CHECK_MAILBOX_INDICATOR, state.receiver.playerID, &wsv1.WsEnvelope_CheckMailboxIndicatorRequest{
		CheckMailboxIndicatorRequest: &wsv1.CheckMailboxIndicatorRequest{},
	})
	total += indLat
	return total, err
}

func openMailbox(client *benchClient, pageSize uint32, pageToken string) ([]*wsv1.MailView, time.Duration, error) {
	resp, latency, err := client.sendRequest(wsv1.Action_OPEN_MAILBOX, client.playerID, &wsv1.WsEnvelope_OpenMailboxRequest{
		OpenMailboxRequest: &wsv1.OpenMailboxRequest{PageSize: pageSize, PageToken: pageToken},
	})
	if err != nil {
		return nil, latency, err
	}
	return resp.GetOpenMailboxResponse().GetMails(), latency, nil
}

// ============================================================
// 场景 6: mixed
// ============================================================

func runMixed(opts options, concurrency int) (result, []sample, error) {
	clients, err := authenticateAll(opts, benchAccountNames(opts.runID, concurrency))
	if err != nil {
		return result{}, nil, err
	}
	defer closeClients(clients)
	states, err := initPlayerLoopStates(opts, clients)
	if err != nil {
		return result{}, nil, err
	}

	warmupUntil := time.Now().Add(opts.warmup)
	for time.Now().Before(warmupUntil) {
		for _, state := range states {
			_, _ = state.client.snapshot()
		}
	}

	return runWorkers(opts, concurrency, len(states), func(worker int) (time.Duration, error) {
		state := states[worker]
		// Per-worker RNG avoids the shared-rand data race.
		r := rand.New(rand.NewSource(time.Now().UnixNano() + int64(worker)*997))
		switch r.Intn(10) {
		case 0, 1, 2, 3, 4, 5, 6, 7:
			return state.client.snapshot()
		case 8:
			_, lat, err := state.client.sendRequest(wsv1.Action_BUY_SEEDS, state.client.playerID, &wsv1.WsEnvelope_BuySeedsRequest{
				BuySeedsRequest: &wsv1.BuySeedsRequest{
					ShopEntryId: state.shopEntryID, Quantity: 1, ExpectedPriceVersion: state.priceVersion,
				},
			})
			return lat, err
		default:
			return state.client.ping(uint64(time.Now().UnixNano()))
		}
	})
}

// ============================================================
// Helpers
// ============================================================

func closeClients(clients []*benchClient) {
	for _, client := range clients {
		if client != nil {
			_ = client.conn.CloseNow()
		}
	}
}

func closeFriendPairs(pairs []*friendPair) {
	for _, pair := range pairs {
		if pair == nil {
			continue
		}
		if pair.visitor != nil {
			_ = pair.visitor.conn.CloseNow()
		}
		if pair.owner != nil {
			_ = pair.owner.conn.CloseNow()
		}
	}
}

func closeMailStates(states []*mailBenchState) {
	for _, state := range states {
		if state == nil {
			continue
		}
		if state.receiver != nil {
			_ = state.receiver.conn.CloseNow()
		}
		if state.sender != nil {
			_ = state.sender.conn.CloseNow()
		}
	}
}

func assignPayload(envelope *wsv1.WsEnvelope, payload any) error {
	switch p := payload.(type) {
	case *wsv1.WsEnvelope_GetShopRequest:
		envelope.Payload = p
	case *wsv1.WsEnvelope_GetPlayerSnapshotRequest:
		envelope.Payload = p
	case *wsv1.WsEnvelope_BuySeedsRequest:
		envelope.Payload = p
	case *wsv1.WsEnvelope_PlantRequest:
		envelope.Payload = p
	case *wsv1.WsEnvelope_HarvestRequest:
		envelope.Payload = p
	case *wsv1.WsEnvelope_SellCropRequest:
		envelope.Payload = p
	case *wsv1.WsEnvelope_CleanPlotRequest:
		envelope.Payload = p
	case *wsv1.WsEnvelope_PingRequest:
		envelope.Payload = p
	case *wsv1.WsEnvelope_CreateFriendCodeRequest:
		envelope.Payload = p
	case *wsv1.WsEnvelope_RedeemFriendCodeRequest:
		envelope.Payload = p
	case *wsv1.WsEnvelope_EnterFriendFarmRequest:
		envelope.Payload = p
	case *wsv1.WsEnvelope_FarmHeartbeatRequest:
		envelope.Payload = p
	case *wsv1.WsEnvelope_StealFriendCropRequest:
		envelope.Payload = p
	case *wsv1.WsEnvelope_SendFriendGiftRequest:
		envelope.Payload = p
	case *wsv1.WsEnvelope_OpenMailboxRequest:
		envelope.Payload = p
	case *wsv1.WsEnvelope_ClaimMailRequest:
		envelope.Payload = p
	case *wsv1.WsEnvelope_CheckMailboxIndicatorRequest:
		envelope.Payload = p
	default:
		return fmt.Errorf("unsupported payload type %T", payload)
	}
	return nil
}

func sortLatencies(latencies []int64) {
	n := len(latencies)
	for i := 1; i < n; i++ {
		for j := i; j > 0 && latencies[j] < latencies[j-1]; j-- {
			latencies[j], latencies[j-1] = latencies[j-1], latencies[j]
		}
	}
}
