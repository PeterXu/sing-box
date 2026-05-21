# Stunnel Outbound Design

## Overview

A new outbound type `stunnel` that selects the lowest-latency outbound via periodic health checks (like urltest) and adds **automatic failover** — when the selected outbound fails on a real connection, it immediately retries through the next-best available outbound.

## Motivation

urltest selects the fastest outbound and only switches during periodic health checks. If the selected outbound dies between checks, all connections fail until the next check cycle. Stunnel closes this gap by reacting to per-connection failures in real time.

## Configuration

```json
{
  "type": "stunnel",
  "tag": "my-tunnel",
  "outbounds": ["新加坡1", "新加坡2", "E-IEPL-香港6", "direct"],
  "url": "https://cp.cloudflare.com/",
  "interval": "10s",
  "idle_timeout": "30m",
  "interrupt_exist_connections": false,
  "cooldown": "30s"
}
```

### Option Struct (`option/group.go`)

```go
type StunnelOutboundOptions struct {
    Outbounds                 []string             `json:"outbounds"`
    URL                       string               `json:"url,omitempty"`
    Interval                  badoption.Duration   `json:"interval,omitempty"`
    IdleTimeout               badoption.Duration   `json:"idle_timeout,omitempty"`
    InterruptExistConnections bool                 `json:"interrupt_exist_connections,omitempty"`
    Cooldown                  badoption.Duration   `json:"cooldown,omitempty"`
}
```

Defaults (matching urltest where applicable):
- `url`: `"https://www.gstatic.com/generate_204"`
- `interval`: `3m`
- `idle_timeout`: `30m`
- `cooldown`: `30s`

## Core Behavior

### Health Checking

Reuses `common/urltest.URLTest()` — HTTP HEAD request through each outbound, measuring round-trip latency. Same periodic loop as urltest:
- Runs at `interval` when active
- Pauses after `idle_timeout` of no connections
- Resumes on next connection (Touch)

### Selection

1. From all **available** outbounds (not marked unavailable), pick the one with lowest latency
2. Tolerance logic retained: don't switch unless new outbound is significantly faster
3. Separate TCP/UDP tracking (same as urltest)

### Failover

When `DialContext()` or `ListenPacket()` returns an error:
1. Mark the failed outbound as **unavailable** with a timestamp
2. Select the next-best available outbound (lowest latency among remaining)
3. **Retry** the connection through the new outbound
4. Repeat until one succeeds or all outbounds exhausted
5. If all fail, return error immediately (fail-fast)

### Unavailable Tracking

- Internal map: `outbound tag → time.Time (marked unavailable at)`
- Outbound is unavailable if `now - markedAt < cooldown`
- Health check success clears the mark immediately (bypasses cooldown)
- After cooldown expires, outbound is eligible for selection again even without a new health check

### Interrupt Behavior

Configurable via `interrupt_exist_connections` (default: false). When enabled and failover switches to a different outbound, existing connections through the failed outbound are interrupted via `interrupt.Group`.

## File Changes

| File | Change |
|------|--------|
| `protocol/group/stunnel.go` | New — stunnel outbound implementation |
| `option/group.go` | Add `StunnelOutboundOptions` struct |
| `constant/proxy.go` | Add `TypeStunnel = "stunnel"` constant |
| `include/registry.go` | Add `group.RegisterStunnel(registry)` |

## Implementation Approach

Fork `protocol/group/urltest.go` as the base. Key additions:

1. **Unavailable map**: `sync.Map` or mutex-protected map tracking failed outbounds with timestamps
2. **DialContext/ListenPacket retry loop**: Wrap connection attempts with failover logic
3. **Health check integration**: Clear unavailable marks on successful checks
4. **Cooldown timer**: Respect configured cooldown duration

The health check loop, history storage, idle timeout, interrupt group, and lifecycle management are reused from urltest with minimal changes.

## Example Config (from user's scenario)

```json
{
  "outbounds": [
    {
      "type": "stunnel",
      "tag": "auto",
      "outbounds": ["新加坡1", "新加坡2", "E-IEPL-香港6", "direct"],
      "url": "https://cp.cloudflare.com/",
      "interval": "10s",
      "cooldown": "30s"
    },
    { "type": "vmess", "tag": "新加坡1", ... },
    { "type": "vmess", "tag": "新加坡2", ... },
    { "type": "vmess", "tag": "E-IEPL-香港6", ... },
    { "type": "direct", "tag": "direct" }
  ],
  "route": { "final": "auto" }
}
```

## Phase 2: Dynamic Outbound Management

Future enhancement to support adding/removing outbounds at runtime:
- `UpdateOutbounds(tags []string)` method on stunnel struct
- Thread-safe replacement of internal outbound list
- Auto re-selection if current outbound is removed
- Health check trigger for newly added outbounds
- Expose via Clash API
