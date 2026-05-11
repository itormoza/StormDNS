# StormDNS Enhancement Plan

**Goal**: More aggressive, more stable, more robust, higher throughput — without stalling.
**Constraint**: Target network is Iran's censored DNS tunnel (slow, lossy, high-latency, ~250B payload per DNS query/response).

---

## Table of Contents

1. [Upload Path — Squeeze More Bytes Per Query](#1-upload-path)
2. [Download Path — Maximize Response Payload](#2-download-path)
3. [ARQ Aggressiveness — Faster Recovery, Less Stalling](#3-arq-aggressiveness)
4. [Dispatcher & Pipeline — Reduce Idle Time](#4-dispatcher--pipeline)
5. [Resolver Management — Smarter Multi-Path](#5-resolver-management)
6. [Server Scalability — Handle More Users Without Sticking](#6-server-scalability)
7. [Connection Warmup & Keepalive](#7-connection-warmup--keepalive)
8. [Compression Improvements](#8-compression-improvements)
9. [Observability & Auto-Tuning](#9-observability--auto-tuning)
10. [Robustness & Edge Cases](#10-robustness--edge-cases)

---

## 1. Upload Path

### 1A. Speculative Pipelining (Query Batching)

**Problem**: Currently each DNS query carries one VPN packet. The client waits for the dispatcher cycle to build and send each packet individually. On high-latency links (500ms+ RTT), this creates a hard throughput ceiling.

**Proposal**: Allow the client to fire N queries in parallel without waiting for responses. The server already handles concurrent queries from different goroutines. The client should:
- Maintain a configurable **in-flight query window** (e.g., 8–32 outstanding queries per resolver).
- Track in-flight count per resolver and throttle when approaching resolver rate limits.
- Use the existing `asyncWriterWorker` to burst-send without waiting for ACKs at the DNS layer.

**Impact**: Multiplies upload throughput by the parallelism factor. A window of 16 on a 500ms RTT link turns 2 queries/sec into 32 queries/sec per resolver.

**Files**: `dispatcher.go`, `async_runtime.go`, `stream_resolver.go`

---

### 1B. Upload Label Packing Optimization

**Problem**: Each DNS query encodes payload into subdomain labels. The current encoding uses one label style per query. Label overhead (dots, length bytes) eats into usable payload.

**Proposal**:
- Pack labels to exactly 63 chars (DNS label max) before adding a dot — minimize dot overhead.
- Consider using `lowerbase36` for resolvers that normalize case (currently available in `basecodec`) to recover ~12% more capacity on case-insensitive resolvers vs base32.
- Dynamically select encoding per-resolver based on MTU probe results: resolvers that pass case-sensitive probes use base64, others fall back to base36/base32.

**Impact**: 5–15% more payload per query depending on resolver behavior.

**Files**: `tunnel_query.go`, `basecodec/`, `mtu.go`

---

### 1C. Multi-Query Scatter for Large Uploads

**Problem**: Upload MTU is typically ~150–200 bytes. A single TCP segment (1460B) requires 8–10 DNS queries just for the raw data, plus ARQ overhead. Each query has its own RTT.

**Proposal**: When the send buffer has multiple chunks ready, scatter them across different resolvers simultaneously rather than sequentially through one resolver. The existing `GetUniqueConnections()` already supports this — extend the dispatcher to:
- Detect when `sndBuf` has ≥N pending items.
- Fan out data packets to different resolvers in parallel within the same dispatch cycle.
- This is distinct from duplication — each query carries **different** data.

**Impact**: Reduces upload serialization delay proportional to resolver count.

**Files**: `dispatcher.go`, `stream_resolver.go`

---

## 2. Download Path

### 2A. Response Payload Maximization

**Status**: Completed.

**Problem**: The server builds DNS responses but doesn't always fill them to capacity. If the server has data waiting for the client but the client hasn't asked (no query in flight), the data sits.

**Proposal**: **Piggyback download data on every response**, even responses to control/ACK queries. Currently:
- Client sends ACK → server responds with empty DNS response.
- Instead: Server should attach pending download data to ANY response going back to the client.

Implementation:
- Server's `buildDNSResponse()` checks the session's TX queue for pending data.
- If data exists, pack it into the TXT/CNAME response alongside the ACK confirmation.
- The client's response parser already handles multi-payload extraction.

**Impact**: Eliminates idle download slots. Every DNS round-trip becomes bidirectional data transfer.

**Files**: `server_session.go`, `server_postsession.go`, `dns_tunnel.go`, `dnsparser/response.go`

---

### 2B. Proactive Polling (Aggressive Download Fetch)

**Status**: Completed.

**Problem**: Download data only arrives when the client happens to send a query. If the client has nothing to upload, it relies on PING packets (200ms–15s interval) to poll for data. This creates download stalls.

**Proposal**: Add a **download poll** mechanism:
- When the client detects active streams with no pending upload data, send lightweight "poll" queries at a configurable aggressive interval (e.g., every 50–100ms per active stream).
- Poll queries should be minimal-overhead (empty or near-empty payload) specifically designed to fetch pending server data.
- Throttle polling based on whether recent polls returned data (adaptive: poll fast when data flows, slow down when idle).

**Config knobs**:
```toml
DOWNLOAD_POLL_INTERVAL_ACTIVE_MS = 50
DOWNLOAD_POLL_INTERVAL_IDLE_MS = 500
DOWNLOAD_POLL_MAX_OUTSTANDING = 4
```

**Impact**: Eliminates the "download stall" problem where the server has data but the client isn't asking fast enough.

**Files**: `async_runtime.go`, `ping_manager.go` (or new `poll_manager.go`)

---

### 2C. Multi-Record Response Packing

**Problem**: A single TXT record in DNS has a practical limit. The carrier abstraction (`tunnelcarrier`) supports different record types but currently uses one type per response.

**Proposal**: For resolvers that support it, use multiple answer records in a single DNS response:
- Pack data across multiple TXT records in one response (many resolvers pass through 2-4 TXT records).
- The `DNS_RECORD_CARRIER_SUPPORT_PLAN.md` already documents CNAME/AAAA/A carriers — prioritize implementing CNAME carrier as it often survives resolver manipulation better than TXT.

**Impact**: 2–4x download MTU increase on supporting resolvers.

**Files**: `tunnelcarrier/`, `dnsparser/response.go`, `server_session.go`

---

## 3. ARQ Aggressiveness

### 3A. Faster NACK-Triggered Retransmit

**Status**: Completed.

**Problem**: Current NACK flow: client detects gap → waits `ARQ_DATA_NACK_INITIAL_DELAY_SECONDS` (0.4s) → sends NACK → server retransmits. That's 0.4s + RTT of wasted time per gap.

**Proposal**:
- Reduce `ARQ_DATA_NACK_INITIAL_DELAY_SECONDS` to 0.1s (or make it RTT-adaptive: `max(100ms, 1.5 × SRTT)`).
- Allow **immediate NACK** for sequence numbers that are ≥3 behind the highest received (classic TCP fast-retransmit heuristic).
- On server side: prioritize retransmit packets (PACKET_STREAM_RESEND) over new data in the TX queue — they're already higher priority but verify the MLQ ordering is correct.

**Impact**: Reduces gap recovery from ~1s to ~200ms.

**Files**: `arq.go` (NACK logic around line 1480+), config defaults

---

### 3B. Proactive Retransmit (Tail Loss Probe)

**Status**: Completed.

**Problem**: If the last packet(s) in a burst are lost, there's no incoming ACK to trigger retransmit. The sender must wait for the full RTO timer to expire.

**Proposal**: Implement **Tail Loss Probe (TLP)**:
- If no ACK arrives within `2 × SRTT` after the last sent packet, retransmit the last unacknowledged packet immediately.
- This is cheaper than waiting for full RTO (which can be 600ms–3s).
- Add a `tlpTimer` in `retransmitLoop()` that fires at `2 × SRTT` after the most recent send if `sndBuf` is non-empty and no ACK has arrived.

**Impact**: Recovers tail losses in ~2×RTT instead of full RTO.

**Files**: `arq.go` (`retransmitLoop`, `checkRetransmits`)

---

### 3C. Adaptive Window Sizing

**Problem**: `ARQ_WINDOW_SIZE = 1000` is static. On a link with 500ms RTT and 200B payload, the bandwidth-delay product is tiny — a window of 1000 is never utilized. But the large window allows excessive buffering that delays loss detection.

**Proposal**: Implement simple **congestion window** (cwnd):
- Start with cwnd = 4.
- On each ACK: cwnd = min(cwnd + 1, max_window).
- On loss/NACK: cwnd = max(cwnd / 2, 4).
- The `limit` field (currently `0.8 × windowSize`) should track cwnd instead.
- Keep `windowSize` as the absolute maximum but let `limit` float with congestion signals.

**Impact**: Prevents buffer bloat, improves loss detection speed, auto-adapts to link capacity.

**Files**: `arq.go` (flow control section)

---

### 3D. RTO Floor Reduction

**Problem**: Current minimum RTO defaults (`ARQ_INITIAL_RTO_SECONDS = 0.6`) are conservative. On resolvers with 100–200ms RTT, this means retransmits wait 3× longer than necessary.

**Proposal**:
- Lower `ARQ_INITIAL_RTO_SECONDS` default to 0.3.
- Make `rto` floor configurable: add `ARQ_MIN_RTO_SECONDS` (default 0.15).
- The adaptive RTO (`updateAdaptiveRTO`) already tracks SRTT/RTTVAR — let it drive down to the floor when conditions permit.

**Impact**: Faster retransmission on good resolvers.

**Files**: `arq.go`, config

---

## 4. Dispatcher & Pipeline

### 4A. Batch Dispatch (Multiple Packets Per Cycle)

**Problem**: The dispatcher (`asyncStreamDispatcher`) processes one stream per cycle. With many active streams, each gets served slowly.

**Proposal**: Per dispatch cycle, serve **up to K packets** across all streams before blocking:
- After finding work, continue scanning for more ready streams up to a batch limit (e.g., K=8).
- Build all DNS queries for the batch, then submit them to the encode pipeline together.
- This amortizes the select/signal overhead across multiple packets.

**Impact**: Higher throughput under multi-stream load, reduced per-packet dispatch overhead.

**Files**: `dispatcher.go`

---

### 4B. Zero-Copy Encode Pipeline

**Problem**: The encode worker (`asyncEncodeWorker`) allocates `[]encodedOutboundDatagram` per task with `append([]encodedOutboundDatagram(nil), frames...)`. Under high throughput this creates GC pressure.

**Proposal**:
- Pool the `encodedOutboundTask` structs and their `frames` slices.
- Pre-allocate frames slices to the typical resolver count to avoid grow-copy.
- Reuse `preparedDomainByName` maps across encode iterations (already partially done but `clear()` + reuse pattern can be tightened).

**Impact**: Reduced GC pauses, more consistent throughput.

**Files**: `async_runtime.go` (`asyncEncodeWorker`)

---

### 4C. TX Channel Backpressure Improvement

**Problem**: When `txChannel` is full, the dispatcher blocks on `waitForTxCapacity()`. Meanwhile, retransmit timers fire and queue more packets, worsening the congestion.

**Proposal**:
- When TX backpressure is detected, **pause ARQ retransmit scheduling** for that session. Resume when capacity returns.
- Signal from `waitForTxCapacity` to `retransmitLoop` via a shared backpressure flag.
- This prevents retransmit storms from crowding out new data.

**Impact**: Prevents cascading congestion under load.

**Files**: `arq.go`, `dispatcher.go`, `async_runtime.go`

---

## 5. Resolver Management

### 5A. Top-K Weighted Distribution

**Problem**: The `LeastLoss` balancer picks the single best resolver. If one resolver is excellent, it gets ALL traffic, overloading it while others sit idle.

**Proposal**: Implement **weighted random** based on loss scores:
- Score each resolver: `weight = 1000 - lossScore`.
- Select resolver with probability proportional to weight.
- This naturally distributes traffic: good resolvers get more, bad ones get less, but no resolver is starved.

**Impact**: More even load distribution, higher aggregate throughput, more resilient to single-resolver failure.

**Files**: `balancer.go`

---

### 5B. Resolver Warmup After Reactivation

**Problem**: When a resolver is reactivated after a recheck, it's seeded with conservative stats (80% delivery). But the first few packets might still fail because the resolver's DNS cache or NAT mapping is cold.

**Proposal**:
- After reactivation, send 2-3 **warmup pings** through the resolver before directing real traffic.
- Only mark the resolver as fully valid after warmup pings succeed.
- The existing `SeedConservativeStats()` handles the balancer side — add a warmup phase to the recheck flow.

**Impact**: Prevents brief disruptions when resolvers come back online.

**Files**: `resolver_health.go`

---

### 5C. Per-Resolver RTT Tracking for Timeout Calibration

**Problem**: The client uses a single global timeout for MTU probes and tunnel packets. Some resolvers are consistently fast (100ms), others slow (800ms). A global timeout either misses fast-resolver failures or prematurely times out slow resolvers.

**Proposal**:
- Track per-resolver EWMA RTT (already partially done in `connectionStats.rttMicrosSum`).
- Use `3 × resolver_SRTT` as the per-resolver timeout for health checks and retransmit decisions.
- Feed per-resolver RTT into the adaptive RTO when a packet's resolver is known.

**Impact**: Faster failure detection on fast resolvers, fewer false timeouts on slow ones.

**Files**: `balancer.go`, `resolver_health.go`, `resolver_stats.go`

---

## 6. Server Scalability

### 6A. Sharded Session Locks

**Status**: Completed.

**Problem**: The `sessionStore` uses a single `sync.RWMutex` for all 255 sessions. Under high user count, `ValidateAndTouch()` contention causes the "sticking" behavior reported.

**Proposal**: Shard the session store:
- Instead of one `mu` for all sessions, use per-session `sync.RWMutex` or a fixed array of N shards (e.g., 16 shards, session ID modulo 16).
- `ValidateAndTouch()` only needs the shard lock, not the global lock.
- Keep global lock only for `findOrCreate()` and `allocateSlotLocked()`.

**Impact**: Dramatically reduces lock contention under multi-user load.

**Files**: `session.go`

---

### 6B. Server Response Queue Per-Session Batching

**Problem**: The server builds and sends one DNS response per inbound query. Under load, each `WriteToUDP` is a separate syscall.

**Proposal**:
- Batch multiple DNS responses destined for the same client address using `sendmmsg` (Linux) or a userspace write-combining buffer.
- Group responses by destination address within a small time window (1–5ms).

**Impact**: Reduces syscall overhead, improves server throughput under multi-user load.

**Files**: `server_runtime.go`, `server_ingress.go`

---

### 6C. Deferred Session Worker Pool Sizing

**Problem**: DNS deferred workers are hardcoded to 1 (`dnsWorkers = 1` in `splitDeferredSessionPools`). Under many concurrent DNS queries, this single worker becomes a bottleneck.

**Proposal**:
- Make DNS deferred worker count scale with total workers: `dnsWorkers = max(1, totalWorkers / 4)`.
- Add config knob `DEFERRED_DNS_WORKERS` for explicit control.

**Impact**: Prevents DNS query processing from becoming a single-threaded bottleneck.

**Files**: `server.go` (`splitDeferredSessionPools`)

---

## 7. Connection Warmup & Keepalive

### 7A. Resolver Pre-Warming

**Problem**: First query to a resolver after idle period often fails due to stale NAT mappings or cold DNS cache paths.

**Proposal**:
- Periodically send no-op queries through each active resolver (every 60s if idle) to keep NAT mappings alive.
- Use the existing `pooledConnMaxAge = 90s` but add active keepalive before connections age out.

**Impact**: Fewer first-packet failures after idle periods.

**Files**: `tunnel_runtime.go`, `resolver_health.go`

---

### 7B. Session Reconnect Speed

**Problem**: When session expires or server restarts, the client goes through full MTU re-probing. In log-based mode this is faster, but still takes 10-30 seconds.

**Proposal**:
- Cache the last known good MTU values and session parameters in memory.
- On reconnect, try the cached values first with a single verification probe per resolver (not full binary search).
- Fall back to full scan only if cached values fail.

**Impact**: Sub-5-second reconnection in most cases.

**Files**: `mtu.go`, `session.go`, `client.go`

---

## 8. Compression Improvements

### 8A. Stream-Aware Compression Selection

**Problem**: Compression type is global (`UPLOAD_COMPRESSION_TYPE`). Some streams carry compressible data (HTML, JSON), others carry incompressible data (images, encrypted content). Compressing incompressible data wastes CPU and can increase size.

**Proposal**:
- Track per-stream compression ratio over the first N packets.
- If compression ratio > 0.95 (barely any savings), disable compression for that stream.
- This is a local client-side optimization — no protocol change needed since `compressionType` is per-packet.

**Impact**: Saves CPU on incompressible streams, frees cycles for compressible ones.

**Files**: `arq.go` (ioLoop where `compressionType` is set), `stream_client.go`

---

### 8B. Dictionary-Based ZSTD Compression

**Problem**: ZSTD is used in "fastest" mode without a dictionary. DNS tunnel payloads are typically HTTP headers which have very predictable patterns.

**Proposal**:
- Pre-train a ZSTD dictionary on common HTTP request/response headers.
- Ship the dictionary with both client and server.
- Use `zstd.WithEncoderDict()` — this can improve compression ratio by 30-50% on small payloads (which is exactly our use case: ~200B chunks).

**Impact**: Significant compression improvement on HTTP traffic, directly translating to more data per DNS query.

**Files**: `compression/types.go`

---

## 9. Observability & Auto-Tuning

### 9A. Runtime Throughput Metrics

**Problem**: The stats reporter shows TX/RX bytes but doesn't show effective throughput, loss rate, or retransmit ratio.

**Proposal**: Add to stats output:
- **Goodput** (unique bytes delivered / time) vs **rawput** (total bytes including retransmits).
- **Retransmit ratio** (resends / total sends).
- **Per-resolver loss rate** and RTT.
- **Active stream count** and queue depths.

**Impact**: Operators can diagnose bottlenecks without debug logging.

**Files**: `traffic_stats.go`, `resolver_stats.go`

---

### 9B. Auto-Tuning Duplication Count

**Problem**: Duplication counts are static config values. On a low-loss link, 7× download duplication wastes bandwidth. On a high-loss link, 3× upload duplication might not be enough.

**Proposal**:
- Track per-resolver delivery rate over sliding windows.
- If delivery rate > 90%, reduce duplication by 1 (min 1).
- If delivery rate < 70%, increase duplication by 1 (max 8).
- Re-evaluate every 30 seconds.
- Add config `AUTO_TUNE_DUPLICATION = true/false`.

**Impact**: Self-optimizing duplication that adapts to changing network conditions.

**Files**: `stream_resolver.go`, `resolver_stats.go`, config

---

## 10. Robustness & Edge Cases

### 10A. Graceful Degradation Under Total Resolver Loss

**Problem**: If all resolvers go down simultaneously, the client enters a tight retry loop that consumes CPU and produces error spam.

**Proposal**:
- Detect "all resolvers invalid" state.
- Enter a **backoff scan mode**: probe resolvers at 5s intervals with exponential backoff up to 60s.
- Reduce logging to one summary line per scan cycle.
- Resume normal operation immediately when any resolver responds.

**Files**: `resolver_health.go`, `client.go`

---

### 10B. Memory Bounded Receive Buffer

**Problem**: `rcvBuf` (ARQ receive buffer) is bounded by `windowSize` (1000 entries) but each entry can be up to MTU bytes. Under adversarial conditions, this can consume significant memory per stream.

**Proposal**:
- Add a byte-level cap on `rcvBuf` (e.g., `maxRcvBufBytes = windowSize × MTU`).
- Track cumulative byte size of `rcvBuf` entries.
- Drop out-of-window packets when byte cap is exceeded.

**Impact**: Bounded memory usage per stream even under attack or extreme reordering.

**Files**: `arq.go` (`processReceivedDataBatch`)

---

### 10C. Stale Stream Reaper Enhancement

**Problem**: `asyncStreamCleanupWorker` runs every 1 second and checks all streams. With many streams, this can be slow.

**Proposal**:
- Use a **min-heap** ordered by expected expiry time instead of scanning all streams.
- Only check streams whose expiry time has passed.
- Reduces cleanup from O(N) per tick to O(K) where K = expired streams.

**Impact**: Lower CPU usage under high stream count.

**Files**: `async_runtime.go` (`asyncStreamCleanupWorker`)

---

## Priority Matrix

| Enhancement | Throughput | Stability | Complexity | Priority |
|---|---|---|---|---|
| 2A. Piggyback download data | ★★★★★ | ★★★ | Medium | **P0** |
| 2B. Proactive download polling | ★★★★★ | ★★★ | Medium | **P0** |
| 3A. Faster NACK retransmit | ★★★★ | ★★★★ | Low | **P0** |
| 3B. Tail Loss Probe | ★★★★ | ★★★★ | Medium | **P0** |
| 6A. Sharded session locks | ★★★ | ★★★★★ | Medium | **P0** |
| 1A. Speculative pipelining | ★★★★★ | ★★★ | High | **P1** |
| 3D. RTO floor reduction | ★★★ | ★★★★ | Low | **P1** |
| 4A. Batch dispatch | ★★★ | ★★★ | Medium | **P1** |
| 5A. Weighted distribution | ★★★ | ★★★★ | Low | **P1** |
| 9B. Auto-tune duplication | ★★★ | ★★★★ | Medium | **P1** |
| 6C. DNS deferred workers | ★★ | ★★★★★ | Low | **P1** |
| 3C. Adaptive window | ★★★ | ★★★★ | High | **P2** |
| 1B. Label packing | ★★ | ★★ | Medium | **P2** |
| 8B. ZSTD dictionary | ★★★ | ★★ | Medium | **P2** |
| 7B. Fast reconnect | ★★ | ★★★★ | Medium | **P2** |
| Others | ★–★★ | ★★–★★★ | Varies | **P3** |

---

## Quick Wins (Config-Only, No Code Changes)

**Status**: Completed.

These tuning changes can be applied immediately to existing builds:

```toml
# More aggressive NACK (existing config knobs)
ARQ_DATA_NACK_INITIAL_DELAY_SECONDS = 0.15
ARQ_DATA_NACK_REPEAT_SECONDS = 0.4
ARQ_DATA_NACK_MAX_GAP = 128

# Lower RTO floor
ARQ_INITIAL_RTO_SECONDS = 0.3
ARQ_MAX_RTO_SECONDS = 2.0
ARQ_CONTROL_INITIAL_RTO_SECONDS = 0.25
ARQ_CONTROL_MAX_RTO_SECONDS = 1.5

# More aggressive pinging (polls for download data)
PING_AGGRESSIVE_INTERVAL_SECONDS = 0.100
PING_LAZY_INTERVAL_SECONDS = 0.300
PING_WARM_THRESHOLD_SECONDS = 3.0

# Faster dispatcher
DISPATCHER_IDLE_POLL_INTERVAL_SECONDS = 0.005

# Larger queues
TX_CHANNEL_SIZE = 4096
RX_CHANNEL_SIZE = 4096
STREAM_QUEUE_INITIAL_CAPACITY = 1024
```

---

*Document created: 2026-05-10*
*Based on: Full codebase scan of StormDNS Go implementation*
