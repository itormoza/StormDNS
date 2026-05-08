# DNS Record Carrier Support Plan

This document is a step-by-step implementation plan for adding StormDNS tunnel carrier support for:

1. TXT
2. CNAME
3. AAAA
4. A
5. MX
6. SRV
7. NULL
8. PRIVATE
9. CAA

The plan is written so separate AI agents can implement one record type at a time without needing to redesign the whole feature.

## Target Network Constraints

StormDNS is a censorship-circumvention tool for networks where national firewalls apply deep packet inspection, active probing, protocol whitelisting, blocking, and throttling. DNS may be the only usable channel. The target path is slow, lossy, high-latency, and unstable.

Every carrier design in this plan must follow these constraints:

- Bandwidth is the bottleneck. Do not add optional metadata, extra handshakes, or extra DNS exchanges unless they directly improve delivery probability or MTU selection.
- Packet loss is normal. The carrier must work with the existing ARQ, ACK/NACK, resend, duplication, and failover model; it must not assume reliable DNS request/response delivery.
- MTU is critical. Resolver limits can be near a few hundred bytes of useful payload. Every carrier must fail cleanly when the selected payload cannot fit and must let `internal/client/mtu.go` find the real limit.
- Latency is high and unpredictable. `AUTO` mode must use existing MTU probes as carrier detection and must persist results so future starts can skip expensive scans.
- Every byte of protocol overhead matters. Prefer compact binary encodings, reuse existing VPN frame headers, and avoid per-chunk fields that can be derived safely.
- DPI and active probing are expected. Do not add obvious probe-only flows. Carrier validation should look like normal tunnel MTU traffic as much as possible.

Design implication: record support is not about maximizing theoretical DNS capacity. It is about finding which record shape survives the real resolver/firewall path with the least overhead and the fewest retries.

## Scope And Assumptions

### What "record support" means here

StormDNS currently uses DNS `TXT` as the tunnel carrier:

- Client upload: encrypted and encoded VPN frame bytes are placed in the DNS question name labels.
- Client question type: `TXT`.
- Server response: VPN frame bytes are returned in `TXT` answer RDATA.
- Client receive path: `TXT` answer RDATA is extracted and parsed back into a VPN packet.

This plan assumes the requested record support means supporting these record types as tunnel carrier QTYPE/answer formats, not merely allowing ordinary local DNS proxy queries of those types.

The ordinary DNS-over-tunnel resolver path is separate. It is mostly controlled by `internal/dnsparser/policy.go`, `internal/client/dns_listener.go`, and `internal/udpserver/dns_tunnel.go`.

### Owner decisions

The owner clarified these requirements:

- The tunnel DNS record type must be adjustable from the TOML config files.
- The default carrier must remain `TXT`.
- There must be an `AUTO` mode.
- In `AUTO` mode, the client scans each resolver per domain for each supported DNS record carrier and picks the best working carrier.

### Remaining assumptions

These assumptions keep the first implementation bounded:

- Use the same record type for the tunnel query and tunnel answer. For example, an `AAAA` tunnel query receives an `AAAA` tunnel answer.
- Continue using QNAME labels for client-to-server upload. The selected record type controls QTYPE and response RDATA format.
- Treat `PRIVATE` as a configurable private-use RR type in the range `65280..65534`, defaulting to `65280`.
- In fixed mode, parse only answers matching the configured carrier.
- In `AUTO` mode, each `Connection` records the selected carrier, and response extraction uses that expected carrier.

## Current Code Map

The current TXT-specific implementation is concentrated in these files:

- `internal/client/tunnel_query.go`
  - `buildTunnelTXTQuestionBytes`
  - `buildTunnelTXTQuestionBytesPrepared`
  - `buildTunnelTXTQueryRaw`
  - `buildTunnelTXTQuery`
- `internal/client/async_runtime.go`
  - async encode workers call `buildTunnelTXTQuestionBytesPrepared`.
- `internal/client/mtu.go`
  - MTU probes call `buildMTUProbeQuery`, which calls `buildTunnelTXTQueryRaw`.
  - upload capacity calculations assume QNAME payload limits, which can stay true for first-phase multi-carrier support.
- `internal/client/tunnel_runtime.go`
  - response handling calls `dnsparser.ExtractVPNResponse`.
- `internal/client/client.go`
  - `Connection` currently has domain and resolver state, but no carrier type.
- `internal/domainmatcher/matcher.go`
  - rejects every tunnel QTYPE except `TXT`.
- `internal/dnsparser/transport.go`
  - DNS tunnel question builders are named for TXT.
  - `BuildVPNResponsePacket` hardcodes TXT answers.
  - `ExtractVPNResponse` extracts only TXT answers.
  - TXT answer chunking and VPN packet assembly are coupled to TXT.
- `internal/enums/dns.go`
  - constants already exist for most requested record types.
  - no dedicated `PRIVATE` default constant exists.
- `internal/enums/dns_names.go`
  - type names exist for common records but not `NULL` or a private-use alias.
- `internal/udpserver/server_session.go`
  - all tunnel responses use `DnsParser.BuildVPNResponsePacket`.

## Phase 0: Shared Carrier Foundation

Do this before implementing new record types. It creates the extension points that each record-specific agent can use.

### Goals

- Preserve existing TXT behavior exactly.
- Replace TXT-only naming with generic carrier APIs while leaving compatibility wrappers.
- Make carrier support configurable and testable.
- Keep each later record implementation small and isolated.

### Data model

Add a carrier description type in `internal/dnsparser`, for example:

```go
type TunnelCarrier struct {
    Type       uint16
    Name       string
    Binary     bool
    MaxPayload int
}
```

The exact shape can differ, but it must answer these questions:

- Is this QTYPE allowed as a tunnel carrier?
- How should response RDATA be built for this carrier?
- How should response RDATA be extracted for this carrier?
- What is the maximum practical response payload before chunking or rejection?

Add helpers:

- `NormalizeTunnelCarrierName(value string) (uint16, error)`
- `TunnelCarrierName(qType uint16, privateType uint16) string`
- `IsPrivateUseRRType(qType uint16) bool`
- `SupportedTunnelCarrierSet(types []uint16) map[uint16]struct{}`

### Configuration

Add client config:

- `TUNNEL_DNS_RECORD_TYPE = "TXT"`
- `TUNNEL_DNS_AUTO_RECORD_TYPES = ["TXT", "CNAME", "AAAA", "A", "MX", "SRV", "NULL", "PRIVATE", "CAA"]`
- `TUNNEL_PRIVATE_RECORD_TYPE = 65280`

Add server config:

- `TUNNEL_DNS_RECORD_TYPES = ["TXT"]`
- `TUNNEL_PRIVATE_RECORD_TYPE = 65280`

Validation rules:

- Client `TUNNEL_DNS_RECORD_TYPE` defaults to `"TXT"` when empty.
- Client `TUNNEL_DNS_RECORD_TYPE` accepts a fixed carrier name or `"AUTO"`.
- Client `TUNNEL_DNS_AUTO_RECORD_TYPES` is used only when `TUNNEL_DNS_RECORD_TYPE = "AUTO"`.
- Empty `TUNNEL_DNS_AUTO_RECORD_TYPES` means all implemented carriers.
- Server `TUNNEL_DNS_RECORD_TYPES` lists accepted tunnel carriers. Empty means `["TXT"]`.
- Server `TUNNEL_DNS_RECORD_TYPES = ["AUTO"]` means accept all implemented carriers.
- Unknown names fail config validation.
- `PRIVATE` resolves to `TUNNEL_PRIVATE_RECORD_TYPE`.
- `TUNNEL_PRIVATE_RECORD_TYPE` must be in `65280..65534`.
- Keep `TXT` in the default samples.
- During incremental rollout, `AUTO` must only probe carriers that are implemented in code. A record listed in config before its implementation should fail validation or be ignored with an explicit warning, not panic.

### Connection model

Extend `internal/client.Connection` with:

```go
TunnelRecordType uint16
TunnelRecordName string
```

Update the connection key so domain plus resolver plus carrier are unique:

```text
resolver|port|domain|qtype
```

This lets MTU discovery determine that one resolver works for `TXT` but not `CAA`, or works for `AAAA` with a smaller download MTU.

### AUTO mode

`AUTO` mode belongs to the client. It expands the resolver scan matrix from:

```text
domain x resolver
```

to:

```text
domain x resolver x carrier
```

Example:

```text
v.example.com x 1.1.1.1:53 x TXT
v.example.com x 1.1.1.1:53 x CNAME
v.example.com x 1.1.1.1:53 x AAAA
...
```

For each candidate, MTU discovery must build upload and download probes using that candidate's `TunnelRecordType`.

`AUTO` must not add a separate carrier-negotiation handshake. The upload/download MTU probes are the carrier viability tests. If a candidate cannot pass the existing MTU probe exchange, it is not usable.

Selection rules:

- A candidate is usable only if both upload and download MTU probes pass.
- For each `(domain, resolver)` pair, choose one best carrier from the passing candidates.
- Prefer the candidate with the highest accepted download MTU.
- If tied, prefer the higher upload MTU.
- If still tied, prefer lower MTU resolve time.
- If still tied, prefer the configured auto list order so behavior is deterministic.
- Only the selected best carrier should become the active `Connection` for that `(domain, resolver)`.
- Non-selected passing candidates may be logged as alternates, but they should not be used by the balancer in the first implementation.
- Expose the selected carrier per `(domain, resolver)` in logs/API so operators can understand what `AUTO` picked.

Bandwidth and startup rules:

- Run one tiny upload and one tiny download probe first for each candidate. Only candidates that pass both should enter full binary-search MTU discovery.
- Do not probe carriers that are not implemented or not accepted by config.
- Respect existing MTU retry and parallelism knobs. Do not introduce a second retry system for carrier scanning.
- Keep `TUNNEL_DNS_AUTO_RECORD_TYPES` ordered. Operators can put cheaper or more compatible carriers first.
- Cache successful carrier and MTU results in the resolver cache log. Log-based startup should restore them and avoid a full `AUTO` scan unless verification is explicitly requested.
- If log-based startup verifies cached results, verify only the cached carrier first. Fall back to a full carrier scan for that resolver only if the cached carrier fails.
- Avoid a global "carrier negotiation" round trip after session start. Session init should use the selected connection carrier from MTU discovery.

Operational rules:

- `AUTO` requires the server to accept the same carrier set. The simplest server config is `TUNNEL_DNS_RECORD_TYPES = ["AUTO"]`.
- `AUTO` should write carrier type into resolver cache logs.
- Log-based startup must restore carrier type and MTU values per connection.
- Background resolver recheck must retest the stored carrier for an existing connection. A later enhancement can periodically rescan all carriers for a resolver, but that is not required for the first implementation.
- Runtime resolver failover and balancing should continue to work on `Connection` values; the carrier is just part of the connection identity.

Update resolver cache logs:

Current:

```text
2026-04-20T15:04:05Z 8.8.8.8:53 v.domain.com UP=64 DOWN=120
```

New backward-compatible format:

```text
2026-04-20T15:04:05Z 8.8.8.8:53 v.domain.com TYPE=TXT UP=64 DOWN=120
```

The log scanner must treat missing `TYPE=` as `TXT`.

### Generic query builders

In `internal/dnsparser/transport.go`, add generic names while preserving current wrappers:

- `BuildTunnelQuestionPacket(domain string, encodedFrame []byte, qType uint16, ednsUDPSize uint16)`
- `BuildTunnelQuestionPacketPrepared(normalizedDomain string, domainQname []byte, encodedFrame []byte, qType uint16, ednsUDPSize uint16)`

Keep these wrappers for compatibility:

- `BuildTunnelTXTQuestionPacket`
- `BuildTunnelTXTQuestionPacketPrepared`
- `BuildTXTQuestionPacket`

Internally, they should call the generic builder with `DNS_RECORD_TYPE_TXT`.

### Generic response builders

Add a new response API:

```go
type BuildVPNResponseOptions struct {
    CarrierType uint16
    PrivateType uint16
    BaseEncode bool
}

func BuildVPNCarrierResponsePacket(questionPacket []byte, answerName string, packet VpnProto.Packet, opts BuildVPNResponseOptions) ([]byte, error)
```

Keep existing `BuildVPNResponsePacket` as a wrapper that uses TXT. This prevents all server code from needing to change in the same commit.

The generic builder should:

- Build the raw VPN frame with existing `VpnProto.BuildRawAuto`.
- Select carrier-specific answer encoding.
- Preserve request ID, question section, flags, and EDNS OPT behavior.
- Keep TTL `0` initially, matching current TXT behavior.
- Return a typed error such as `ErrCarrierPayloadTooLarge` when a carrier cannot represent the packet.

### Generic response extraction

Add:

```go
type ExtractVPNResponseOptions struct {
    CarrierType uint16
    PrivateType uint16
    BaseEncoded bool
}

func ExtractVPNCarrierResponse(packet []byte, opts ExtractVPNResponseOptions) (VpnProto.Packet, error)
```

Keep `ExtractVPNResponse(packet, baseEncoded)` as a TXT wrapper until all call sites are migrated.

### RDATA offsets for name-based records

CNAME, MX, and SRV RDATA contains DNS names. Those names can legally use DNS compression pointers. The current parser exposes `ResourceRecord.RData` but not the absolute packet offset of the RDATA.

Extend `ResourceRecord`:

```go
RDataOffset int
```

Set it in `parseResourceRecords` before slicing `RData`. This lets extractors call `parseName(packet, rr.RDataOffset+offset)` when a target name is encoded in RDATA.

### Domain matcher

Change `domainmatcher.Matcher` so allowed carrier QTYPEs are configurable.

Current behavior:

- Domain must match.
- QTYPE must be TXT.
- Labels must be present and long enough.

New behavior:

- Domain must match.
- QTYPE must be in the configured carrier set.
- Labels must be present and long enough.
- The decision must keep `QuestionType` so the server can respond using the same carrier.

Recommended constructor:

```go
func New(domains []string, minLabelLength int, carriers map[uint16]struct{}) *Matcher
```

Default to TXT when `carriers` is empty.

### Client call-site migration

Add generic client helpers:

- `buildTunnelQuestionBytes(domain string, encoded []byte, qType uint16)`
- `buildTunnelQuestionBytesPrepared(domain preparedTunnelDomain, encoded []byte, qType uint16)`
- `buildTunnelQueryRaw(domain string, qType uint16, options VpnProto.BuildOptions)`
- `buildTunnelQuery(domain string, qType uint16, options VpnProto.BuildOptions)`

Then migrate:

- session init
- session close notification
- async encode worker
- MTU upload probes
- MTU download probes
- one-way DNS query dispatch

In fixed mode, pass the configured `TUNNEL_DNS_RECORD_TYPE`. In `AUTO` mode, pass `conn.TunnelRecordType` from the selected `Connection`.

Do not remove TXT-named wrappers in this phase.

### Server call-site migration

Where the server currently calls `BuildVPNResponsePacket`, pass the carrier from the parsed question or domain matcher decision:

- invalid session responses
- session busy responses
- normal queued session responses
- session init response
- MTU upload response
- MTU download response

If carrier response building returns `ErrCarrierPayloadTooLarge`, return a no-data response and let MTU tests reject that resolver/carrier combination.

### Foundation tests

Add or update tests:

- TXT query builder wrappers produce byte-identical output compared with current behavior except transaction ID randomness.
- Domain matcher accepts TXT by default.
- Domain matcher rejects non-TXT by default.
- Domain matcher accepts configured carrier types.
- Config parses server `TUNNEL_DNS_RECORD_TYPES`.
- Config parses client `TUNNEL_DNS_RECORD_TYPE = "TXT"`.
- Config parses client `TUNNEL_DNS_RECORD_TYPE = "AUTO"`.
- `AUTO` expands the MTU scan candidate matrix by `domain x resolver x implemented-carrier`.
- `AUTO` stores one active best carrier per `domain x resolver`.
- Fixed mode expands the connection map by `domain x resolver` only.
- Config maps `PRIVATE` to default private type.
- Invalid private type fails config validation.
- Resolver cache log parser treats missing `TYPE=` as TXT.
- Connection keys differ by qtype.
- Resolver cache log parser preserves explicit `TYPE=<record>`.

Acceptance criteria:

- `go test ./internal/dnsparser ./internal/domainmatcher ./internal/config ./internal/client ./internal/udpserver` passes.
- A default config still uses TXT only.
- No new carrier is enabled unless configured.
- `AUTO` mode works with only TXT implemented, then automatically includes each later carrier when that carrier is implemented and registered.

## Shared Chunking Rules

Each carrier should reuse one common assembly path where possible.

### Raw VPN frame

The payload to encode into DNS answers is the raw VPN frame produced by:

```go
VpnProto.BuildRawAuto(...)
```

When `BaseEncodeData` or response-mode base64 is active, preserve the existing behavior:

- Upload label encoding still goes through the configured codec.
- Response extraction must use the same base-encoding mode the session negotiated.

### Multi-answer ordering

Do not rely on DNS answer order for carriers where RRsets are unordered. Each answer must contain enough metadata to reorder chunks.

Use one of these patterns:

- Existing TXT chunk envelope for TXT compatibility.
- Binary chunk envelope for NULL, PRIVATE, CAA value, A, AAAA.
- Name chunk envelope for MX and SRV target names.

Keep chunk metadata minimal:

- Do not add a per-chunk version byte unless the carrier cannot be parsed safely without it.
- Prefer one response-level length field over repeating lengths in every chunk.
- For fixed-size records such as A and AAAA, derive chunk length from total payload length whenever possible.
- Reject ambiguous duplicate chunks instead of adding extra sequence/auth fields.
- Do not add checksums inside carrier RDATA. The VPN/security layers and DNS transaction matching already provide the relevant validation for this project.

For lossy networks, lower overhead is usually better than richer self-description. A carrier-specific extractor can be strict and simple because malformed responses should be treated as resolver failures.

### Error handling

All builders should return typed errors:

- `ErrUnsupportedCarrier`
- `ErrCarrierPayloadTooLarge`
- `ErrCarrierAnswerMalformed`

Server behavior:

- For malformed input queries, keep existing no-data or format-error behavior.
- For an outgoing response too large for the selected carrier, return no-data. MTU discovery should classify the resolver/carrier as failed or lower the accepted MTU.

Client behavior:

- If extraction fails for the expected carrier, treat it as a failed resolver response.
- Do not silently parse unrelated answer types unless an explicit auto-detect compatibility path is implemented.

## Step 1: TXT Carrier

### Purpose

TXT is already implemented. This step refactors it into the new carrier abstraction and proves that compatibility is preserved.

### Wire format

Keep the current TXT answer format:

- RDATA is one or more TXT character strings.
- Each TXT string starts with a one-byte length.
- Single-answer responses contain the raw VPN frame or base64-encoded raw VPN frame inside the TXT payload.
- Multi-answer responses use the existing chunk envelope:
  - first chunk starts with `0x00`, then total chunk count, then the VPN frame header and first payload slice;
  - later chunks start with chunk ID, then the next payload slice.

Do not change existing TXT wire encoding in this step.

### Files to change

- `internal/dnsparser/transport.go`
- `internal/dnsparser/transport_test.go`
- `internal/client/tunnel_query.go`
- `internal/client/async_runtime.go`
- `internal/client/mtu.go`
- `internal/client/session.go`
- `internal/domainmatcher/matcher.go`
- `internal/udpserver/server_session.go`

### Implementation details

Move TXT-specific response code behind a carrier implementation:

- `buildTXTCarrierAnswers(rawFrame []byte, baseEncode bool) ([][]byte, error)`
- `writeTXTAnswers(...)`
- `extractTXTCarrierPayloads(parsed Packet) [][]byte`

Existing functions can delegate:

- `buildTXTAnswerChunks`
- `BuildTXTResponsePacket`
- `BuildVPNResponsePacket`
- `ExtractVPNResponse`

The generic path must be able to call the TXT implementation through `CarrierType: DNS_RECORD_TYPE_TXT`.

### Tests

Add regression tests:

- TXT single-answer response can be extracted by both old and new APIs.
- TXT chunked response can be extracted by both old and new APIs.
- TXT base64 response can be extracted by both old and new APIs.
- Existing query builder compatibility tests still pass.
- Server responds to TXT tunnel candidate exactly as before.

Acceptance criteria:

- No behavior change for default configs.
- Existing TXT tests pass.
- The new generic API works for TXT.
- `AUTO` mode with only TXT registered behaves like fixed TXT except the connection key includes the carrier.

## Step 2: CNAME Carrier

### Purpose

Add CNAME as a low-capacity tunnel response carrier. This is useful for resolver paths that allow CNAME responses but restrict TXT.

### DNS constraints

CNAME RDATA is a DNS name. A DNS name has:

- maximum 253 bytes in presentation form;
- maximum 255 bytes on the wire including length octets and root;
- maximum 63 bytes per label.

A DNS owner name should not have more than one CNAME record. Therefore, this carrier should initially support only one CNAME answer per DNS response.

This makes CNAME a small-payload carrier. It should pass only low download MTUs.

### Wire format

Use one CNAME answer:

```text
<answerName> 0 IN CNAME sd.v1.l<length-hex>.<payload-labels>.<base-domain>
```

Rules:

- `sd` identifies a StormDNS carrier name.
- `v1` is the carrier encoding version.
- `l<length-hex>` stores raw decoded payload length.
- `<payload-labels>` is lowercase unpadded base32 or the project's safest DNS label codec.
- `<base-domain>` can be `decision.BaseDomain` or the request name's base domain.
- The target name must be strictly encoded with `encodeDNSNameStrict`.
- If the encoded target would exceed DNS name limits, return `ErrCarrierPayloadTooLarge`.

Do not emit multiple CNAME answers for one owner in the first implementation.

CNAME is the most overhead-constrained carrier in this list. In `AUTO` mode it should be treated as a fallback candidate for very small responses, not as a likely high-throughput carrier.

### Files to change

- `internal/dnsparser/transport.go`
- `internal/dnsparser/parser.go`
- `internal/dnsparser/transport_test.go`
- `internal/enums/dns_names.go`
- `internal/domainmatcher/matcher.go`
- `internal/client/mtu.go`
- `internal/udpserver/server_session.go`

### Implementation details

Builder:

- Convert raw VPN frame to DNS-safe labels.
- Build one CNAME RDATA target name.
- Use answer type `DNS_RECORD_TYPE_CNAME`.
- Use class `IN`, TTL `0`.
- Preserve the request question and OPT record.

Extractor:

- Parse answers with type CNAME.
- Decode the target name from RDATA using packet-level offsets, not only the RDATA slice.
- Find the `sd.v1.l<length>` marker.
- Join payload labels.
- Decode payload.
- Trim/validate by decoded length.
- Parse with existing VPN frame assembly.

Validation:

- Reject names missing the `sd.v1` marker.
- Reject invalid base32/base label payload.
- Reject decoded length mismatch.
- Reject more than one CNAME payload answer unless a future chunked CNAME design is explicitly added.

### Tests

Add tests:

- Build and extract a small CNAME VPN response.
- CNAME builder rejects a payload that cannot fit in one target name.
- CNAME extractor handles compressed RDATA names if the parser supports them.
- Domain matcher accepts CNAME when configured and rejects it when not configured.
- MTU probe rejects CNAME when requested download payload exceeds capacity.

Acceptance criteria:

- CNAME works for small control packets.
- CNAME does not break TXT behavior.
- Oversized CNAME responses fail cleanly.
- CNAME is available to `AUTO` mode after it is implemented and registered.

## Step 3: AAAA Carrier

### Purpose

Add AAAA as a binary fixed-width tunnel response carrier. AAAA has much better capacity than A because every record carries 16 bytes of RDATA.

### DNS constraints

AAAA RDATA must be exactly 16 bytes. DNS answer order is not guaranteed, so each AAAA answer must carry chunk metadata.

### Wire format

Use one or more AAAA answers. Each answer has 16 bytes:

```text
byte 0   chunk_id
byte 1   total_chunks
byte 2-15 payload bytes, zero-padded
```

Rules:

- Payload length is derived from the decoded VPN frame. No per-chunk data length is stored.
- `chunk_id` must be `< total_chunks`.
- Every chunk repeats `total_chunks`.
- Payload bytes are appended in chunk ID order.
- Final padding is removed by parsing the assembled VPN frame. If trailing padding makes the VPN frame invalid, extraction fails.
- Maximum payload with this format is roughly `255 * 14 = 3570` bytes before DNS packet overhead.

### Files to change

- `internal/dnsparser/transport.go`
- `internal/dnsparser/transport_test.go`
- `internal/enums/dns_names.go`
- `internal/domainmatcher/matcher.go`
- `internal/client/mtu.go`
- `internal/udpserver/server_session.go`

### Implementation details

Builder:

- Split raw frame into 14-byte slices.
- Reject if more than 255 chunks are required.
- Write one AAAA RR per chunk.
- Use name compression for repeated owner names if the existing response builder supports it. If not, use full owner names first and optimize later.

Extractor:

- Collect AAAA answers.
- Validate duplicate chunk IDs are either identical or rejected.
- Validate total chunk count is consistent.
- Sort by chunk ID.
- Concatenate bytes `2..15` from each chunk.
- Parse the raw frame with the shared VPN frame assembly.

MTU:

- Download MTU search should naturally find a lower ceiling for AAAA.
- Do not special-case AAAA in MTU logic unless tests show the binary search repeatedly asks for impossible sizes. The builder's `ErrCarrierPayloadTooLarge` should cause probe failure.

### Tests

Add tests:

- Single AAAA answer extraction.
- Multi-AAAA answer extraction with shuffled answer order.
- Duplicate chunk ID rejection.
- Invalid `data_len > 12` rejection.
- Payload requiring more than 255 chunks returns `ErrCarrierPayloadTooLarge`.
- End-to-end MTU probe can pass with a small download size.

Acceptance criteria:

- AAAA works for payloads up to roughly 3060 bytes before DNS packet overhead.
- Shuffled AAAA answers still parse correctly.
- Oversized responses fail cleanly.
- AAAA is available to `AUTO` mode after it is implemented and registered.

## Step 4: A Carrier

### Purpose

Add A as a very low-capacity binary fixed-width tunnel response carrier. A is likely to work through many DNS paths but is inefficient because each answer carries only 4 bytes.

### DNS constraints

A RDATA must be exactly 4 bytes. DNS answer order is not guaranteed.

### Wire format

Use one header A record plus data A records.

Header chunk:

```text
byte 0   0
byte 1   payload_len_hi
byte 2   payload_len_lo
byte 3   total_data_chunks
```

Data chunks:

```text
byte 0   chunk_id, starting at 1
byte 1-3 payload bytes, zero-padded
```

Rules:

- Payload length is a uint16 and must match reconstructed payload length after trimming padding.
- `total_data_chunks` must match the number of chunk IDs after the header.
- Maximum data chunks are 254 if chunk IDs stay in `1..254`.
- Maximum payload is `254 * 3 = 762` bytes.
- Reject payloads larger than the format can represent.

### Files to change

- `internal/dnsparser/transport.go`
- `internal/dnsparser/transport_test.go`
- `internal/enums/dns_names.go`
- `internal/domainmatcher/matcher.go`
- `internal/client/mtu.go`
- `internal/udpserver/server_session.go`

### Implementation details

Builder:

- Write one header A RR.
- Split raw frame into 3-byte chunks.
- Write one A RR per data chunk.
- Use class `IN`, TTL `0`.

Extractor:

- Find exactly one header chunk with byte 0 equal to `0`.
- Reject duplicate header chunks unless identical and explicitly tolerated.
- Sort data chunks by chunk ID.
- Concatenate bytes 1..3.
- Trim to payload length from header.
- Validate there are no gaps.
- Parse the raw VPN frame.

Risk:

- Some recursive resolvers, clients, or logs may treat arbitrary A RDATA as real IP addresses. This is valid DNS wire data but can be operationally suspicious.
- Because capacity is low, A should be opt-in and should not be enabled by default.

### Tests

Add tests:

- Small A response extraction.
- Multi-A response extraction with shuffled answers.
- Missing header rejection.
- Missing chunk rejection.
- Oversized payload rejection.
- Domain matcher accepts A only when configured.

Acceptance criteria:

- A works for very small payloads.
- MTU discovery rejects A when configured minimum download MTU is too high.
- TXT behavior remains unchanged.
- A is available to `AUTO` mode after it is implemented and registered.

## Step 5: MX Carrier

### Purpose

Add MX as a name-based multi-answer carrier. MX supports multiple answers per owner, and each MX answer can carry a chunk in the exchange domain name.

### DNS constraints

MX RDATA:

```text
preference uint16
exchange   domain-name
```

MX RRsets are unordered. Recursive resolvers may reorder by preference. Therefore, chunk ID must be explicit. Preference can also be used as chunk ID to improve deterministic ordering but extraction must still parse the chunk ID from the exchange name.

### Wire format

Each MX answer contains one encoded chunk:

```text
preference = chunk_id
exchange = sd.v1.i<chunk-hex>.t<total-hex>.l<len-hex>.<payload-labels>.<base-domain>
```

Rules:

- `i<chunk-hex>` is chunk ID starting at `0`.
- `t<total-hex>` is total chunk count.
- `l<len-hex>` is decoded payload length for this chunk.
- `<payload-labels>` is lowercase unpadded base32 or the project's safest DNS label codec.
- The exchange name must fit DNS name limits.
- Chunks are assembled by chunk ID.
- Reject duplicate conflicting chunks.
- This carrier is overhead-heavy because every chunk carries a DNS name. Keep it as a fallback for paths where binary-looking RDATA carriers fail.

### Files to change

- `internal/dnsparser/transport.go`
- `internal/dnsparser/parser.go`
- `internal/dnsparser/transport_test.go`
- `internal/enums/dns_names.go`
- `internal/domainmatcher/matcher.go`
- `internal/client/mtu.go`
- `internal/udpserver/server_session.go`

### Implementation details

Builder:

- Determine max payload bytes per exchange name based on suffix length and metadata labels.
- Split raw frame into chunks that fit the exchange name limit.
- Emit one MX answer per chunk.
- Set preference to chunk ID, capped by `uint16`.
- Limit total chunks to a sane bound, recommended `255`, to match other carriers.

Extractor:

- For each MX answer:
  - Read the first 2 bytes as preference.
  - Parse the exchange name from packet-level RDATA offset.
  - Parse `sd.v1.i*.t*.l*` metadata.
  - Decode payload labels.
- Validate total count and chunk IDs.
- Assemble in chunk ID order.
- Parse raw VPN frame.

### Tests

Add tests:

- Single MX answer extraction.
- Multi-MX answer extraction with shuffled answers and non-sorted preferences.
- Name too large returns `ErrCarrierPayloadTooLarge`.
- Duplicate conflicting chunk rejection.
- Compressed exchange name parsing if compression support is added.

Acceptance criteria:

- MX supports larger responses than CNAME.
- Extraction does not rely on answer order.
- Invalid MX metadata fails cleanly.
- MX is available to `AUTO` mode after it is implemented and registered.

## Step 6: SRV Carrier

### Purpose

Add SRV as a name-based multi-answer carrier. SRV is similar to MX but has more numeric fields before the target name.

### DNS constraints

SRV RDATA:

```text
priority uint16
weight   uint16
port     uint16
target   domain-name
```

SRV answer names often use `_service._proto.example.com`, but the wire format does not require that pattern for this tunnel carrier. The request owner remains the tunnel query name.

### Wire format

Each SRV answer contains one encoded chunk:

```text
priority = chunk_id
weight   = total_chunks
port     = decoded_chunk_len
target   = sd.v1.i<chunk-hex>.<payload-labels>.<base-domain>
```

Rules:

- `priority` duplicates chunk ID.
- `weight` duplicates total chunk count.
- `port` duplicates decoded chunk length.
- The target name also includes chunk ID to survive any numeric-field rewriting or parser bugs.
- Chunks are assembled by target metadata, not by answer order.
- This carrier is overhead-heavy because every chunk carries a DNS name. Keep it as a fallback for paths where binary-looking RDATA carriers fail.

### Files to change

- `internal/dnsparser/transport.go`
- `internal/dnsparser/parser.go`
- `internal/dnsparser/transport_test.go`
- `internal/enums/dns_names.go`
- `internal/domainmatcher/matcher.go`
- `internal/client/mtu.go`
- `internal/udpserver/server_session.go`

### Implementation details

Builder:

- Determine max payload bytes per target name.
- Split raw frame into chunks that fit.
- Emit one SRV answer per chunk.
- Use class `IN`, TTL `0`.
- Limit chunk count to `255` unless there is a strong reason to support more.

Extractor:

- For each SRV answer:
  - Validate RDLEN is at least 7 bytes.
  - Read priority, weight, and port.
  - Parse target name from packet-level RDATA offset plus 6.
  - Parse `sd.v1.i*` metadata and payload labels.
  - Validate numeric fields agree with metadata where applicable.
- Assemble chunks in chunk ID order.
- Parse raw VPN frame.

### Tests

Add tests:

- Single SRV answer extraction.
- Multi-SRV answer extraction with shuffled answers.
- Numeric field mismatch rejection.
- Oversized target name rejection.
- Malformed target metadata rejection.

Acceptance criteria:

- SRV can carry chunked responses without relying on RR order.
- SRV failures are isolated to SRV carrier code.
- TXT behavior remains unchanged.
- SRV is available to `AUTO` mode after it is implemented and registered.

## Step 7: NULL Carrier

### Purpose

Add NULL as the simplest binary RDATA carrier. NULL RDATA is arbitrary bytes, so it is the closest DNS record type to a raw tunnel response payload.

### DNS constraints

NULL has type code `10`. It is obsolete and may be filtered by some resolvers, but the wire format allows arbitrary RDATA.

### Wire format

Use one or more NULL answers.

Recommended RDATA envelope:

```text
byte 0   chunk_id
byte 1   total_chunks
byte 2.. payload bytes
```

Rules:

- Each answer can carry as much payload as fits within response-size limits.
- Chunk count must be `<= 255`.
- Extraction sorts by chunk ID.
- For a single chunk, still use the envelope for consistency and validation.

### Files to change

- `internal/dnsparser/transport.go`
- `internal/dnsparser/transport_test.go`
- `internal/enums/dns_names.go`
- `internal/domainmatcher/matcher.go`
- `internal/client/mtu.go`
- `internal/udpserver/server_session.go`

### Implementation details

Builder:

- Split raw frame into chunks sized by a conservative per-RR payload limit.
- Start with a limit such as `512` bytes per NULL RDATA unless using EDNS/request size to compute a tighter bound.
- Build one NULL RR per chunk.
- Preserve OPT record from request.

Extractor:

- Collect NULL answers.
- Validate version.
- Validate total chunk count is consistent.
- Reject missing chunks.
- Concatenate payload bytes in chunk ID order.
- Parse raw VPN frame.

Risk:

- Recursive resolvers may block or mishandle NULL.
- This should be opt-in and should not be the default.

### Tests

Add tests:

- Single NULL answer extraction.
- Chunked NULL extraction with shuffled answers.
- Missing chunk rejection.
- Conflicting duplicate chunk rejection.
- Domain matcher accepts NULL only when configured.

Acceptance criteria:

- NULL works through direct unit tests.
- Resolver compatibility is measured by MTU tests rather than assumed.
- NULL is available to `AUTO` mode after it is implemented and registered.

## Step 8: PRIVATE Carrier

### Purpose

Add a private-use RR type as a raw binary carrier. This behaves like NULL but uses a configurable private-use type code.

### DNS constraints

Private-use DNS RR type codes are in `65280..65534`. There is no single standard RR named `PRIVATE`.

### Wire format

Use the same RDATA envelope as NULL:

```text
byte 0   chunk_id
byte 1   total_chunks
byte 2.. payload bytes
```

The only difference from NULL is the RR type code.

### Files to change

- `internal/enums/dns.go`
- `internal/enums/dns_names.go`
- `internal/dnsparser/transport.go`
- `internal/dnsparser/transport_test.go`
- `internal/config/client.go`
- `internal/config/server.go`
- `internal/domainmatcher/matcher.go`
- `internal/client/mtu.go`
- `internal/udpserver/server_session.go`

### Implementation details

Config:

- Add `TUNNEL_PRIVATE_RECORD_TYPE`.
- Validate range `65280..65534`.
- When any tunnel carrier setting contains `PRIVATE`, replace it with the configured numeric type.
- Allow explicit names such as `TYPE65280` as an optional convenience if desired.

Builder and extractor:

- Reuse NULL binary carrier code.
- The carrier type passed to the response builder must be the configured private type.
- Type names in logs should display `PRIVATE(65280)` or `TYPE65280` consistently.

Tests:

- Config accepts `PRIVATE` with default private type.
- Config rejects private type outside `65280..65534`.
- Build/extract response using type `65280`.
- Domain matcher accepts only the configured private type, not all private-use types.
- `DNSRecordTypeName` or carrier display helper names the configured type clearly.

Acceptance criteria:

- PRIVATE support is not hardcoded to every private-use type.
- Operators can change the private type without code changes.
- NULL and PRIVATE share tested binary-carrier logic.
- PRIVATE is available to `AUTO` mode after it is implemented and registered.

## Step 9: CAA Carrier

### Purpose

Add CAA as a structured binary/text RDATA carrier. CAA has a simple RDATA structure and supports multiple answers.

### DNS constraints

CAA RDATA:

```text
flags      uint8
tag_len    uint8
tag        tag_len bytes
value      remaining bytes
```

CAA tags are interpreted by certificate authorities, so use a non-critical custom tag and do not set the critical bit.

### Wire format

Use one or more CAA answers with:

```text
flags = 0
tag   = "stormdns"
value = binary chunk envelope
```

Value envelope:

```text
byte 0   chunk_id
byte 1   total_chunks
byte 2.. payload bytes
```

Rules:

- Only parse CAA answers with tag `stormdns`.
- Ignore unrelated CAA answers.
- `flags` must be `0` for tunnel carrier records.
- Chunk assembly is the same as NULL/PRIVATE.

### Files to change

- `internal/dnsparser/transport.go`
- `internal/dnsparser/transport_test.go`
- `internal/enums/dns_names.go`
- `internal/domainmatcher/matcher.go`
- `internal/client/mtu.go`
- `internal/udpserver/server_session.go`

### Implementation details

Builder:

- Split raw frame into value chunks.
- For each chunk:
  - write flags byte `0`;
  - write tag length and tag `stormdns`;
  - append binary chunk envelope to value.
- Use answer type `DNS_RECORD_TYPE_CAA`, class `IN`, TTL `0`.

Extractor:

- Collect CAA answers.
- Validate RDLEN is at least `2 + len("stormdns") + 3`.
- Read flags, tag length, tag, value.
- Ignore CAA answers with other tags.
- Reject critical flag on `stormdns` records.
- Assemble value chunks.
- Parse raw VPN frame.

Risk:

- Some systems may log or inspect CAA specially because it is certificate-related.
- The custom tag should be clearly documented as tunnel-internal.

### Tests

Add tests:

- Single CAA answer extraction.
- Chunked CAA extraction with shuffled answers.
- Non-`stormdns` CAA records are ignored.
- Critical `stormdns` CAA is rejected.
- Missing chunk rejection.
- Oversized payload rejection.

Acceptance criteria:

- CAA works independently of unrelated CAA answers.
- CAA support remains opt-in.
- TXT behavior remains unchanged.
- CAA is available to `AUTO` mode after it is implemented and registered.

## Recommended Implementation Order

The user-facing list is TXT, CNAME, AAAA, A, MX, SRV, NULL, PRIVATE, CAA. The technically safest implementation order is:

1. Phase 0 foundation
2. TXT refactor and regression
3. NULL
4. PRIVATE
5. CAA
6. AAAA
7. A
8. MX
9. SRV
10. CNAME

Reasoning:

- TXT regression proves the abstraction.
- `AUTO` scaffolding belongs in Phase 0 and should initially work with only TXT registered.
- NULL and PRIVATE validate raw binary carriers with minimal DNS-specific RDATA structure and low overhead.
- CAA reuses binary chunking with a small RDATA wrapper.
- AAAA and A validate fixed-width binary record chunking with predictable overhead.
- MX, SRV, and CNAME require robust DNS-name-in-RDATA parsing and name-length handling.
- Name-based carriers are later because they are overhead-heavy, which matters in the target network.

If separate agents work in parallel, assign disjoint areas:

- One agent owns Phase 0.
- One agent owns binary chunk helpers after Phase 0.
- One agent owns name-based chunk helpers after `RDataOffset` support lands.
- One agent owns config, connection model, resolver cache logs, and documentation.

## End-To-End Test Matrix

For every carrier, add at least these tests:

- Build query with configured QTYPE.
- Domain matcher accepts only configured QTYPE.
- Server builds response with matching answer type.
- Client extracts the VPN packet from that answer type.
- Shuffled multi-answer response parses correctly when carrier supports chunking.
- Oversized response returns `ErrCarrierPayloadTooLarge`.
- MTU probe passes with a small enough download target.
- MTU probe fails cleanly with an impossible download target.
- `AUTO` mode includes the carrier in its scan matrix after the carrier is implemented.
- `AUTO` mode picks the better carrier when two carriers pass for the same domain/resolver.
- Default TXT-only config still passes all existing tests.

Manual smoke tests after a record implementation:

1. Configure server and client with only that record type.
2. Set conservative MTUs first:
   - low upload;
   - low download;
   - low retries;
   - one or two known-good resolvers.
3. Run client startup MTU tests.
4. Confirm session init succeeds.
5. Open a small TCP/SOCKS request through the tunnel.
6. Raise download MTU gradually and observe where resolver compatibility breaks.

## Documentation Updates

After Phase 0 and each carrier step:

- Update `client_config.toml.simple`.
- Update `server_config.toml.simple`.
- Update `README.MD`.
- Update `README_FA.MD` if the project keeps both languages in sync.
- Mention that non-TXT carriers are opt-in and resolver-dependent.
- Document `PRIVATE` as a private-use numeric type, not a standards-defined RR named `PRIVATE`.

Suggested config documentation:

```toml
# Client: DNS record carrier selection.
# TXT is the default and most compatible.
# Use a fixed record type such as "TXT", "AAAA", "NULL", or "CAA",
# or use "AUTO" to scan each resolver/domain across the auto candidate list.
TUNNEL_DNS_RECORD_TYPE = "TXT"

# Client: candidates used only when TUNNEL_DNS_RECORD_TYPE = "AUTO".
# Empty means all implemented carriers.
TUNNEL_DNS_AUTO_RECORD_TYPES = ["TXT", "CNAME", "AAAA", "A", "MX", "SRV", "NULL", "PRIVATE", "CAA"]

# Server: accepted tunnel carrier records.
# Use ["TXT"] for the default, or ["AUTO"] to accept all implemented carriers.
TUNNEL_DNS_RECORD_TYPES = ["TXT"]

# Used when any tunnel carrier setting contains "PRIVATE".
# Must be in the private-use DNS RR type range 65280..65534.
TUNNEL_PRIVATE_RECORD_TYPE = 65280
```

## Agent Handoff Template

Use this template when asking an AI agent to implement a single record:

```text
Implement only the <RECORD> carrier from docs/DNS_RECORD_CARRIER_SUPPORT_PLAN.md.

Do not implement other carriers.
Keep TXT behavior backward-compatible.
Add focused tests for <RECORD>.
Register <RECORD> in AUTO mode only after its build/extract tests pass.
Run:
  go test ./internal/dnsparser ./internal/domainmatcher ./internal/config ./internal/client ./internal/udpserver

Report changed files, tests run, and any limitations.
```

For CNAME, MX, and SRV agents, also say:

```text
Use packet-level RDATA offsets for parsing names in RDATA. Do not assume RDATA names are uncompressed slices.
```

For A, AAAA, NULL, PRIVATE, and CAA agents, also say:

```text
Do not rely on DNS answer order. Every chunk must be self-identifying or validated through explicit header metadata.
```

## Completion Definition

The feature is complete when:

- All requested carriers can be enabled through config.
- TXT remains the default and remains backward-compatible.
- Client `AUTO` mode scans implemented carriers per domain/resolver and selects the best carrier for each domain/resolver pair.
- Per-resolver MTU validation accounts for carrier type.
- Resolver cache logs preserve carrier type.
- Server accepts only configured carrier QTYPEs for tunnel domains.
- Client extracts responses using the expected carrier for each connection.
- Each carrier has unit tests for build, extract, malformed input, and oversized payload.
- The full test suite passes with default TXT config.
