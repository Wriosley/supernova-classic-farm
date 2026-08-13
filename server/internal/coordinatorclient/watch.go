package coordinatorclient

import (
	"context"
	"errors"
	"io"
	"time"

	coordinatorv1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/coordinator"
)

func (c *Client) syncAndOpen() error {
	response, err := c.rpc.GetRouteSnapshot(c.ctx, &coordinatorv1.GetRouteSnapshotRequest{})
	if err != nil {
		return err
	}
	if err := c.cache.applySnapshot(response.Snapshot); err != nil {
		return err
	}
	if c.streamCancel != nil {
		c.streamCancel()
	}
	streamCtx, streamCancel := context.WithCancel(c.ctx)
	stream, err := c.rpc.WatchRoutes(streamCtx)
	if err != nil {
		streamCancel()
		return err
	}
	snapshot := c.cache.getSnapshot()
	if err := stream.Send(&coordinatorv1.WatchRoutesRequest{Payload: &coordinatorv1.WatchRoutesRequest_Subscribe{Subscribe: &coordinatorv1.Subscribe{SubscriberId: c.cfg.SubscriberID, Kind: c.cfg.Kind, LastMapVersion: snapshot.MapVersion}}}); err != nil {
		streamCancel()
		return err
	}
	c.cache.markFresh()
	c.currentStream = stream
	c.streamCancel = streamCancel
	return nil
}
func (c *Client) watchLoop() {
	defer c.wg.Done()
	backoff := c.cfg.MinBackoff
	for {
		stream := c.currentStream
		err := c.consume(stream)
		if c.streamCancel != nil {
			c.streamCancel()
		}
		if c.ctx.Err() != nil {
			return
		}
		if err == nil {
			backoff = c.cfg.MinBackoff
		}
		timer := time.NewTimer(backoff)
		select {
		case <-c.ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
		if backoff < c.cfg.MaxBackoff {
			backoff *= 2
			if backoff > c.cfg.MaxBackoff {
				backoff = c.cfg.MaxBackoff
			}
		}
		if err := c.syncAndOpen(); err != nil {
			continue
		}
		backoff = c.cfg.MinBackoff
	}
}
func (c *Client) consume(stream coordinatorv1.CoordinatorService_WatchRoutesClient) error {
	for {
		type result struct {
			message *coordinatorv1.WatchRoutesResponse
			err     error
		}
		received := make(chan result, 1)
		go func() { message, err := stream.Recv(); received <- result{message, err} }()
		select {
		case <-c.ctx.Done():
			return c.ctx.Err()
		case <-c.resync:
			return ErrResyncRequired
		case item := <-received:
			if item.err != nil {
				if errors.Is(item.err, io.EOF) {
					return ErrResyncRequired
				}
				return item.err
			}
			message := item.message
			if snapshot := message.GetSnapshot(); snapshot != nil {
				if err := c.cache.applySnapshot(snapshot); err != nil {
					return err
				}
				c.cache.markFresh()
				_ = stream.Send(ack(c.cache.getSnapshot().MapVersion))
				continue
			}
			if batch := message.GetRouteBatch(); batch != nil {
				if err := c.cache.applyBatch(batch); err != nil {
					return err
				}
				c.cache.markFresh()
				if err := stream.Send(ack(batch.MapVersion)); err != nil {
					return err
				}
				continue
			}
			if ping := message.GetPing(); ping != nil {
				c.cache.markFresh()
				if err := stream.Send(&coordinatorv1.WatchRoutesRequest{Payload: &coordinatorv1.WatchRoutesRequest_Pong{Pong: &coordinatorv1.WatchPong{PingId: ping.PingId}}}); err != nil {
					return err
				}
				continue
			}
			if availability := message.GetAvailabilityBatch(); availability != nil {
				if err := c.cache.applyAvailability(availability); err != nil {
					return err
				}
				c.cache.markFresh()
				continue
			}
			if message.GetResyncRequired() != nil {
				return ErrResyncRequired
			}
		}
	}
}
func ack(version uint64) *coordinatorv1.WatchRoutesRequest {
	return &coordinatorv1.WatchRoutesRequest{Payload: &coordinatorv1.WatchRoutesRequest_Ack{Ack: &coordinatorv1.RouteAck{MapVersion: version}}}
}
