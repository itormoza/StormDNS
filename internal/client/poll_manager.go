// ==============================================================================
// StormDNS
// Author: nullroute1970
// Github: https://github.com/nullroute1970/StormDNS
// Year: 2026
// ==============================================================================

package client

// PollManager implements proactive download polling (Enhancement 2B).
//
// Problem: Download data only arrives when the client happens to send a query.
// If the client has nothing to upload, it relies solely on the PingManager's
// relatively infrequent pings (200ms–15s) to elicit server responses. This
// creates download stalls on active but upload-idle sessions.
//
// Solution: When the client detects active data streams with no pending upload
// work, send lightweight PING packets at a configurable aggressive rate
// (DOWNLOAD_POLL_INTERVAL_ACTIVE_MS, default 50ms). Each such query gives the
// server an opportunity to attach piggybacked download data (see 2A). Polling
// slows to the idle interval when no data streams are active.
//
// Adaptive behaviour: the poll fires at the active interval whenever at least
// one non-zero stream exists. It steps back to the idle interval once all
// data streams are gone. An outstanding-count cap prevents flooding the TX
// queue ahead of real upstream data.
//
// Relationship with PingManager: PollManager is intentionally separate from
// PingManager. PingManager handles liveness/watchdog; PollManager is purely
// a download-throughput optimisation. Both write PING packets to stream 0, but
// the PollManager drives at a much tighter schedule based on stream activity,
// while PingManager backs off when the link is quiet.

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	Enums "stormdns-go/internal/enums"
)

// PollManager drives proactive download polling for active sessions.
type PollManager struct {
	client *Client

	// outstanding tracks how many poll packets are currently in-flight
	// (queued in stream 0's TX queue but not yet acknowledged by a response).
	// We approximate this with a counter that increments on each sent poll
	// and is decremented whenever any inbound packet is received (because any
	// inbound packet consumes one outstanding query slot).
	outstanding atomic.Int32

	// nextPollSeq is the PING sequence counter used for poll packets.
	// A separate counter avoids collision with PingManager's sequence space.
	nextPollSeq atomic.Uint32

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

func newPollManager(client *Client) *PollManager {
	return &PollManager{client: client}
}

// Start launches the poll loop under the given parent context.
func (pm *PollManager) Start(parentCtx context.Context) {
	pm.Stop() // ensure idempotent restart

	pm.outstanding.Store(0)
	pm.ctx, pm.cancel = context.WithCancel(parentCtx)
	pm.wg.Add(1)
	go pm.pollLoop()
}

// Stop cancels the poll loop and waits for it to exit.
func (pm *PollManager) Stop() {
	if pm.cancel != nil {
		pm.cancel()
		pm.wg.Wait()
		pm.cancel = nil
	}
}

// NotifyInbound should be called whenever any inbound packet arrives.
// It decrements the outstanding counter so the poll loop is free to send
// another query sooner.
func (pm *PollManager) NotifyInbound() {
	if pm == nil {
		return
	}
	if cur := pm.outstanding.Load(); cur > 0 {
		pm.outstanding.Add(-1)
	}
}

// hasActiveDataStreams returns true when there is at least one non-zero stream
// in a non-terminal state.  Stream 0 (the control/ping channel) is excluded
// because it is always present and not a data stream.
func (pm *PollManager) hasActiveDataStreams() bool {
	c := pm.client
	c.streamsMu.RLock()
	defer c.streamsMu.RUnlock()
	for id, s := range c.active_streams {
		if id == 0 || s == nil {
			continue
		}
		status := s.StatusValue()
		if status == streamStatusActive ||
			status == streamStatusConnecting ||
			status == streamStatusSocksConnecting ||
			status == streamStatusDraining {
			return true
		}
	}
	return false
}

// txQueueHasPendingData returns true when any non-zero stream currently has
// queued upload data (stream data or resend packets). When upload work is
// already queued the server will receive queries from the normal dispatch path,
// so the poll manager should hold off to avoid congesting the TX channel.
func (pm *PollManager) txQueueHasPendingData() bool {
	c := pm.client
	c.streamsMu.RLock()
	defer c.streamsMu.RUnlock()
	for id, s := range c.active_streams {
		if id == 0 || s == nil || s.txQueue == nil {
			continue
		}
		if s.txQueue.FastSize() > 0 {
			return true
		}
	}
	return false
}

func (pm *PollManager) nextInterval() time.Duration {
	if pm.hasActiveDataStreams() {
		return pm.client.cfg.DownloadPollActiveInterval()
	}
	return pm.client.cfg.DownloadPollIdleInterval()
}

func (pm *PollManager) nextSequence() uint16 {
	return uint16(pm.nextPollSeq.Add(1))
}

func (pm *PollManager) pollLoop() {
	defer pm.wg.Done()

	pm.client.log.Debugf("📡 <cyan>Poll Manager loop started</cyan>")

	interval := pm.nextInterval()
	timer := time.NewTimer(interval)
	defer timer.Stop()

	for {
		select {
		case <-pm.ctx.Done():
			return
		case <-timer.C:
		}

		interval = pm.nextInterval()

		if pm.client.SessionReady() && pm.hasActiveDataStreams() {
			maxOutstanding := pm.client.cfg.DownloadPollMaxOutstandingCount()
			cur := pm.outstanding.Load()

			// Only send a poll if:
			//  1. We haven't saturated the outstanding cap.
			//  2. There is no normal upload traffic already queued (the normal
			//     dispatch path will elicit server responses for free).
			//  3. The TX channel has capacity so we don't add backpressure.
			if int(cur) < maxOutstanding &&
				!pm.txQueueHasPendingData() &&
				pm.client.txChannelHasCapacity(1) {

				pm.client.streamsMu.RLock()
				s0 := pm.client.active_streams[0]
				pm.client.streamsMu.RUnlock()

				if s0 != nil {
					payload, err := buildClientPingPayload()
					if err == nil {
						if s0.PushTXPacket(
							Enums.DefaultPacketPriority(Enums.PACKET_PING),
							Enums.PACKET_PING,
							pm.nextSequence(),
							0,
							0,
							0,
							0,
							payload,
						) {
							pm.outstanding.Add(1)
						}
					}
				}
			}
		}

		timer.Reset(interval)
	}
}
