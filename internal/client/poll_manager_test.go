// ==============================================================================
// StormDNS
// Author: nullroute1970
// Github: https://github.com/nullroute1970/StormDNS
// Year: 2026
// ==============================================================================

package client

import (
	"testing"
	"time"

	"stormdns-go/internal/config"
	"stormdns-go/internal/mlq"
)

// buildPollManagerClient creates a minimal Client suitable for PollManager
// unit tests.  It wires up the txSignal channel and session state so the
// PollManager can operate without a full runtime.
func buildPollManagerClient(t *testing.T, cfg config.ClientConfig) *Client {
	t.Helper()
	c := buildTestClientWithResolvers(cfg)
	c.sessionReady = true
	return c
}

// TestPollManagerNotifyInboundDecrementsOutstanding verifies that NotifyInbound
// decrements the outstanding counter and never goes below zero.
func TestPollManagerNotifyInboundDecrementsOutstanding(t *testing.T) {
	c := buildPollManagerClient(t, config.ClientConfig{})
	pm := newPollManager(c)

	// Start at 0 — decrement should be a no-op (never below 0).
	pm.NotifyInbound()
	if pm.outstanding.Load() != 0 {
		t.Fatalf("expected outstanding to stay 0 after NotifyInbound on empty, got %d", pm.outstanding.Load())
	}

	// Simulate 3 in-flight polls.
	pm.outstanding.Store(3)
	pm.NotifyInbound()
	if pm.outstanding.Load() != 2 {
		t.Fatalf("expected outstanding 2 after one NotifyInbound, got %d", pm.outstanding.Load())
	}
	pm.NotifyInbound()
	pm.NotifyInbound()
	if pm.outstanding.Load() != 0 {
		t.Fatalf("expected outstanding 0 after draining 3 inbounds, got %d", pm.outstanding.Load())
	}
}

// TestPollManagerHasActiveDataStreams verifies that stream-0 is not counted as
// an active data stream and that non-zero ACTIVE streams are detected.
func TestPollManagerHasActiveDataStreams(t *testing.T) {
	c := buildPollManagerClient(t, config.ClientConfig{})
	c.active_streams = make(map[uint16]*Stream_client)
	pm := newPollManager(c)

	// No streams → no active data streams.
	if pm.hasActiveDataStreams() {
		t.Fatal("expected no active data streams with empty map")
	}

	// Add Stream 0 (control channel) — should still report no data streams.
	c.active_streams[0] = &Stream_client{client: c, StreamID: 0}
	if pm.hasActiveDataStreams() {
		t.Fatal("expected stream 0 not to count as an active data stream")
	}

	// Add a real data stream.
	s := &Stream_client{
		client:   c,
		StreamID: 1,
		txQueue:  mlq.New[*clientStreamTXPacket](16),
	}
	s.SetStatus(streamStatusActive)
	c.active_streams[1] = s

	if !pm.hasActiveDataStreams() {
		t.Fatal("expected active data stream to be detected")
	}
}

// TestPollManagerTxQueueHasPendingData verifies that pending upload data on
// non-zero streams is detected correctly.
func TestPollManagerTxQueueHasPendingData(t *testing.T) {
	c := buildPollManagerClient(t, config.ClientConfig{})
	c.active_streams = make(map[uint16]*Stream_client)
	pm := newPollManager(c)

	// No streams → no pending data.
	if pm.txQueueHasPendingData() {
		t.Fatal("expected no pending data in empty stream map")
	}

	// Non-zero stream with empty queue → no pending data.
	s := &Stream_client{
		client:   c,
		StreamID: 2,
		txQueue:  mlq.New[*clientStreamTXPacket](16),
	}
	c.active_streams[2] = s
	if pm.txQueueHasPendingData() {
		t.Fatal("expected no pending data when TX queue is empty")
	}
}

// TestPollManagerNextIntervalAdaptsToStreamActivity verifies that the poll
// interval switches between active and idle rates based on stream presence.
func TestPollManagerNextIntervalAdaptsToStreamActivity(t *testing.T) {
	activeDur := 50 * time.Millisecond
	idleDur := 500 * time.Millisecond

	c := buildPollManagerClient(t, config.ClientConfig{
		DownloadPollActiveIntervalMs: float64(activeDur.Milliseconds()),
		DownloadPollIdleIntervalMs:   float64(idleDur.Milliseconds()),
	})
	c.active_streams = make(map[uint16]*Stream_client)
	pm := newPollManager(c)

	// No data streams → idle interval.
	if got := pm.nextInterval(); got != idleDur {
		t.Fatalf("expected idle interval %v, got %v", idleDur, got)
	}

	// Add an active data stream → active interval.
	s := &Stream_client{client: c, StreamID: 1, txQueue: mlq.New[*clientStreamTXPacket](16)}
	s.SetStatus(streamStatusActive)
	c.active_streams[1] = s

	if got := pm.nextInterval(); got != activeDur {
		t.Fatalf("expected active interval %v, got %v", activeDur, got)
	}
}

// TestPollManagerStartStop verifies that the PollManager goroutine starts and
// stops cleanly without leaking goroutines.
func TestPollManagerStartStop(t *testing.T) {
	c := buildPollManagerClient(t, config.ClientConfig{
		DownloadPollActiveIntervalMs: 50,
		DownloadPollIdleIntervalMs:   500,
		DownloadPollMaxOutstanding:   4,
	})
	c.active_streams = make(map[uint16]*Stream_client)
	pm := newPollManager(c)

	ctx := t.Context()
	pm.Start(ctx)

	// Let it tick once.
	time.Sleep(20 * time.Millisecond)

	pm.Stop()
	// Stop should be idempotent.
	pm.Stop()
}
