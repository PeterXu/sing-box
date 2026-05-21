# Stunnel Outbound Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Implement a new `stunnel` outbound type that selects the lowest-latency outbound via health checks and adds automatic failover on connection error.

**Architecture:** Fork the urltest outbound pattern. Reuse the same health check engine (`common/urltest`), history storage, and periodic check loop. Add an unavailable-tracking map and retry logic in `DialContext`/`ListenPacket` so failed connections are transparently retried through the next-best outbound.

**Tech Stack:** Go, sing-box adapter/outbound interfaces, common/urltest health check, interrupt group.

---

### Task 1: Add constant and option struct

**Files:**
- Modify: `constant/proxy.go`
- Modify: `option/group.go`

- [ ] **Step 1: Add `TypeStunnel` constant to `constant/proxy.go`**

Add to the second `const` block (line 40-43, alongside `TypeSelector` and `TypeURLTest`):

```go
const (
	TypeSelector = "selector"
	TypeURLTest  = "urltest"
	TypeStunnel  = "stunnel"
)
```

Also add the display name in `ProxyDisplayName` (add before the `default` case at line 101):

```go
case TypeStunnel:
	return "Stunnel"
```

- [ ] **Step 2: Add `StunnelOutboundOptions` to `option/group.go`**

Append after the existing `URLTestOutboundOptions` struct (after line 18):

```go
type StunnelOutboundOptions struct {
	Outbounds                 []string           `json:"outbounds"`
	URL                       string             `json:"url,omitempty"`
	Interval                  badoption.Duration `json:"interval,omitempty"`
	Tolerance                 uint16             `json:"tolerance,omitempty"`
	IdleTimeout               badoption.Duration `json:"idle_timeout,omitempty"`
	InterruptExistConnections bool               `json:"interrupt_exist_connections,omitempty"`
	Cooldown                  badoption.Duration `json:"cooldown,omitempty"`
}
```

- [ ] **Step 3: Add default cooldown constant to `constant/timeout.go`**

Add to the existing const block (after `DefaultURLTestIdleTimeout`):

```go
DefaultStunnelCooldown = 30 * time.Second
```

- [ ] **Step 4: Verify build compiles**

Run: `go build ./constant/ ./option/`
Expected: no errors

- [ ] **Step 5: Commit**

```bash
git add constant/proxy.go constant/timeout.go option/group.go
git commit -m "feat(stunnel): add type constant, option struct, and default cooldown"
```

---

### Task 2: Create stunnel outbound implementation

**Files:**
- Create: `protocol/group/stunnel.go`

This is the core implementation. It forks `protocol/group/urltest.go` and adds:
1. Unavailable tracking map
2. Retry loop in `DialContext` / `ListenPacket`
3. Cooldown-based availability restoration
4. Health check integration to clear unavailable marks

- [ ] **Step 1: Create `protocol/group/stunnel.go`**

```go
package group

import (
	"context"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/adapter/outbound"
	"github.com/sagernet/sing-box/common/interrupt"
	"github.com/sagernet/sing-box/common/urltest"
	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing-tun"
	"github.com/sagernet/sing/common"
	"github.com/sagernet/sing/common/batch"
	E "github.com/sagernet/sing/common/exceptions"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
	"github.com/sagernet/sing/common/x/list"
	"github.com/sagernet/sing/service"
	"github.com/sagernet/sing/service/pause"
)

func RegisterStunnel(registry *outbound.Registry) {
	outbound.Register[option.StunnelOutboundOptions](registry, C.TypeStunnel, NewStunnel)
}

var (
	_ adapter.OutboundGroup           = (*Stunnel)(nil)
	_ adapter.ConnectionHandler       = (*Stunnel)(nil)
	_ adapter.PacketConnectionHandler = (*Stunnel)(nil)
)

type Stunnel struct {
	outbound.Adapter
	ctx                          context.Context
	outbound                     adapter.OutboundManager
	connection                   adapter.ConnectionManager
	logger                       log.ContextLogger
	tags                         []string
	link                         string
	interval                     time.Duration
	tolerance                    uint16
	idleTimeout                  time.Duration
	cooldown                     time.Duration
	group                        *StunnelGroup
	interruptExternalConnections bool
}

func NewStunnel(ctx context.Context, router adapter.Router, logger log.ContextLogger, tag string, options option.StunnelOutboundOptions) (adapter.Outbound, error) {
	outbound := &Stunnel{
		Adapter:                      outbound.NewAdapter(C.TypeStunnel, tag, []string{N.NetworkTCP, N.NetworkUDP}, options.Outbounds),
		ctx:                          ctx,
		outbound:                     service.FromContext[adapter.OutboundManager](ctx),
		connection:                   service.FromContext[adapter.ConnectionManager](ctx),
		logger:                       logger,
		tags:                         options.Outbounds,
		link:                         options.URL,
		interval:                     time.Duration(options.Interval),
		tolerance:                    options.Tolerance,
		idleTimeout:                  time.Duration(options.IdleTimeout),
		cooldown:                     time.Duration(options.Cooldown),
		interruptExternalConnections: options.InterruptExistConnections,
	}
	if len(outbound.tags) == 0 {
		return nil, E.New("missing tags")
	}
	return outbound, nil
}

func (s *Stunnel) Start() error {
	outbounds := make([]adapter.Outbound, 0, len(s.tags))
	for i, tag := range s.tags {
		detour, loaded := s.outbound.Outbound(tag)
		if !loaded {
			return E.New("outbound ", i, " not found: ", tag)
		}
		outbounds = append(outbounds, detour)
	}
	group, err := NewStunnelGroup(s.ctx, s.outbound, s.logger, outbounds, s.link, s.interval, s.tolerance, s.idleTimeout, s.cooldown, s.interruptExternalConnections)
	if err != nil {
		return err
	}
	s.group = group
	return nil
}

func (s *Stunnel) PostStart() error {
	s.group.PostStart()
	return nil
}

func (s *Stunnel) Close() error {
	return common.Close(
		common.PtrOrNil(s.group),
	)
}

func (s *Stunnel) Now() string {
	if s.group.selectedOutboundTCP != nil {
		return s.group.selectedOutboundTCP.Tag()
	} else if s.group.selectedOutboundUDP != nil {
		return s.group.selectedOutboundUDP.Tag()
	}
	return ""
}

func (s *Stunnel) All() []string {
	return s.tags
}

func (s *Stunnel) URLTest(ctx context.Context) (map[string]uint16, error) {
	return s.group.URLTest(ctx)
}

func (s *Stunnel) CheckOutbounds() {
	s.group.CheckOutbounds(true)
}

func (s *Stunnel) DialContext(ctx context.Context, network string, destination M.Socksaddr) (net.Conn, error) {
	s.group.Touch()
	switch N.NetworkName(network) {
	case N.NetworkTCP:
		return s.group.DialContext(ctx, network, destination)
	case N.NetworkUDP:
		return s.group.DialContext(ctx, network, destination)
	default:
		return nil, E.Extend(N.ErrUnknownNetwork, network)
	}
}

func (s *Stunnel) ListenPacket(ctx context.Context, destination M.Socksaddr) (net.PacketConn, error) {
	s.group.Touch()
	return s.group.ListenPacket(ctx, destination)
}

func (s *Stunnel) NewConnection(ctx context.Context, conn net.Conn, metadata adapter.InboundContext, onClose N.CloseHandlerFunc) {
	ctx = interrupt.ContextWithIsExternalConnection(ctx)
	s.connection.NewConnection(ctx, s, conn, metadata, onClose)
}

func (s *Stunnel) NewPacketConnection(ctx context.Context, conn N.PacketConn, metadata adapter.InboundContext, onClose N.CloseHandlerFunc) {
	ctx = interrupt.ContextWithIsExternalConnection(ctx)
	s.connection.NewPacketConnection(ctx, s, conn, metadata, onClose)
}

func (s *Stunnel) NewDirectRouteConnection(metadata adapter.InboundContext, routeContext tun.DirectRouteContext, timeout time.Duration) (tun.DirectRouteDestination, error) {
	s.group.Touch()
	selected := s.group.selectedOutboundTCP
	if selected == nil {
		selected, _ = s.group.Select(N.NetworkTCP)
	}
	if selected == nil {
		return nil, E.New("missing supported outbound")
	}
	if !common.Contains(selected.Network(), metadata.Network) {
		return nil, E.New(metadata.Network, " is not supported by outbound: ", selected.Tag())
	}
	return selected.(adapter.DirectRouteOutbound).NewDirectRouteConnection(metadata, routeContext, timeout)
}

type StunnelGroup struct {
	ctx                          context.Context
	outbound                     adapter.OutboundManager
	pause                        pause.Manager
	pauseCallback                *list.Element[pause.Callback]
	logger                       log.Logger
	outbounds                    []adapter.Outbound
	link                         string
	interval                     time.Duration
	tolerance                    uint16
	idleTimeout                  time.Duration
	cooldown                     time.Duration
	history                      adapter.URLTestHistoryStorage
	checking                     atomic.Bool
	selectedOutboundTCP          adapter.Outbound
	selectedOutboundUDP          adapter.Outbound
	interruptGroup               *interrupt.Group
	interruptExternalConnections bool
	access                       sync.Mutex
	ticker                       *time.Ticker
	close                        chan struct{}
	started                      bool
	lastActive                   common.TypedValue[time.Time]
	unavailable                  map[string]time.Time
	unavailableAccess            sync.RWMutex
}

func NewStunnelGroup(ctx context.Context, outboundManager adapter.OutboundManager, logger log.Logger, outbounds []adapter.Outbound, link string, interval time.Duration, tolerance uint16, idleTimeout time.Duration, cooldown time.Duration, interruptExternalConnections bool) (*StunnelGroup, error) {
	if interval == 0 {
		interval = C.DefaultURLTestInterval
	}
	if tolerance == 0 {
		tolerance = 50
	}
	if idleTimeout == 0 {
		idleTimeout = C.DefaultURLTestIdleTimeout
	}
	if cooldown == 0 {
		cooldown = C.DefaultStunnelCooldown
	}
	if interval > idleTimeout {
		return nil, E.New("interval must be less or equal than idle_timeout")
	}
	var history adapter.URLTestHistoryStorage
	if historyFromCtx := service.PtrFromContext[urltest.HistoryStorage](ctx); historyFromCtx != nil {
		history = historyFromCtx
	} else if clashServer := service.FromContext[adapter.ClashServer](ctx); clashServer != nil {
		history = clashServer.HistoryStorage()
	} else {
		history = urltest.NewHistoryStorage()
	}
	return &StunnelGroup{
		ctx:                          ctx,
		outbound:                     outboundManager,
		logger:                       logger,
		outbounds:                    outbounds,
		link:                         link,
		interval:                     interval,
		tolerance:                    tolerance,
		idleTimeout:                  idleTimeout,
		cooldown:                     cooldown,
		history:                      history,
		close:                        make(chan struct{}),
		pause:                        service.FromContext[pause.Manager](ctx),
		interruptGroup:               interrupt.NewGroup(),
		interruptExternalConnections: interruptExternalConnections,
		unavailable:                  make(map[string]time.Time),
	}, nil
}

func (g *StunnelGroup) PostStart() {
	g.access.Lock()
	defer g.access.Unlock()
	g.started = true
	g.lastActive.Store(time.Now())
	go g.CheckOutbounds(false)
}

func (g *StunnelGroup) Touch() {
	if !g.started {
		return
	}
	g.access.Lock()
	defer g.access.Unlock()
	if g.ticker != nil {
		g.lastActive.Store(time.Now())
		return
	}
	ticker := time.NewTicker(g.interval)
	g.ticker = ticker
	g.pauseCallback = pause.RegisterTicker(g.pause, ticker, g.interval, nil)
	go g.loopCheck(ticker, g.close)
}

func (g *StunnelGroup) Close() error {
	g.access.Lock()
	defer g.access.Unlock()
	if g.ticker == nil {
		return nil
	}
	g.ticker.Stop()
	g.ticker = nil
	g.pause.UnregisterCallback(g.pauseCallback)
	g.pauseCallback = nil
	close(g.close)
	return nil
}

func (g *StunnelGroup) isUnavailable(tag string) bool {
	g.unavailableAccess.RLock()
	markedAt, exists := g.unavailable[tag]
	g.unavailableAccess.RUnlock()
	if !exists {
		return false
	}
	return time.Since(markedAt) < g.cooldown
}

func (g *StunnelGroup) markUnavailable(tag string) {
	g.unavailableAccess.Lock()
	g.unavailable[tag] = time.Now()
	g.unavailableAccess.Unlock()
}

func (g *StunnelGroup) clearUnavailable(tag string) {
	g.unavailableAccess.Lock()
	delete(g.unavailable, tag)
	g.unavailableAccess.Unlock()
}

func (g *StunnelGroup) Select(network string) (adapter.Outbound, bool) {
	var minDelay uint16
	var minOutbound adapter.Outbound
	switch network {
	case N.NetworkTCP:
		if g.selectedOutboundTCP != nil {
			if history := g.history.LoadURLTestHistory(RealTag(g.selectedOutboundTCP)); history != nil {
				minOutbound = g.selectedOutboundTCP
				minDelay = history.Delay
			}
		}
	case N.NetworkUDP:
		if g.selectedOutboundUDP != nil {
			if history := g.history.LoadURLTestHistory(RealTag(g.selectedOutboundUDP)); history != nil {
				minOutbound = g.selectedOutboundUDP
				minDelay = history.Delay
			}
		}
	}
	for _, detour := range g.outbounds {
		if !common.Contains(detour.Network(), network) {
			continue
		}
		if g.isUnavailable(RealTag(detour)) {
			continue
		}
		history := g.history.LoadURLTestHistory(RealTag(detour))
		if history == nil {
			continue
		}
		if minDelay == 0 || minDelay > history.Delay+g.tolerance {
			minDelay = history.Delay
			minOutbound = detour
		}
	}
	if minOutbound == nil {
		for _, detour := range g.outbounds {
			if !common.Contains(detour.Network(), network) {
				continue
			}
			if g.isUnavailable(RealTag(detour)) {
				continue
			}
			return detour, false
		}
		return nil, false
	}
	return minOutbound, true
}

func (g *StunnelGroup) DialContext(ctx context.Context, network string, destination M.Socksaddr) (net.Conn, error) {
	for {
		outbound, _ := g.Select(network)
		if outbound == nil {
			return nil, E.New("all outbounds are unavailable")
		}
		conn, err := outbound.DialContext(ctx, network, destination)
		if err == nil {
			return g.interruptGroup.NewConn(conn, interrupt.IsExternalConnectionFromContext(ctx)), nil
		}
		g.logger.Debug("stunnel: outbound ", outbound.Tag(), " failed: ", err, ", trying next")
		g.markUnavailable(RealTag(outbound))
		g.history.DeleteURLTestHistory(RealTag(outbound))
	}
}

func (g *StunnelGroup) ListenPacket(ctx context.Context, destination M.Socksaddr) (net.PacketConn, error) {
	for {
		outbound, _ := g.Select(N.NetworkUDP)
		if outbound == nil {
			return nil, E.New("all outbounds are unavailable")
		}
		conn, err := outbound.ListenPacket(ctx, destination)
		if err == nil {
			return g.interruptGroup.NewPacketConn(conn, interrupt.IsExternalConnectionFromContext(ctx)), nil
		}
		g.logger.Debug("stunnel: outbound ", outbound.Tag(), " failed: ", err, ", trying next")
		g.markUnavailable(RealTag(outbound))
		g.history.DeleteURLTestHistory(RealTag(outbound))
	}
}

func (g *StunnelGroup) loopCheck(ticker *time.Ticker, closeChan <-chan struct{}) {
	if time.Since(g.lastActive.Load()) > g.interval {
		g.lastActive.Store(time.Now())
		g.CheckOutbounds(false)
	}
	for {
		select {
		case <-closeChan:
			return
		case <-ticker.C:
		}
		if time.Since(g.lastActive.Load()) > g.idleTimeout {
			g.access.Lock()
			if g.ticker == ticker {
				g.ticker.Stop()
				g.ticker = nil
				g.pause.UnregisterCallback(g.pauseCallback)
				g.pauseCallback = nil
			}
			g.access.Unlock()
			return
		}
		g.CheckOutbounds(false)
	}
}

func (g *StunnelGroup) CheckOutbounds(force bool) {
	_, _ = g.urlTest(g.ctx, force)
}

func (g *StunnelGroup) URLTest(ctx context.Context) (map[string]uint16, error) {
	return g.urlTest(ctx, false)
}

func (g *StunnelGroup) urlTest(ctx context.Context, force bool) (map[string]uint16, error) {
	result := make(map[string]uint16)
	if g.checking.Swap(true) {
		return result, nil
	}
	defer g.checking.Store(false)
	b, _ := batch.New(ctx, batch.WithConcurrencyNum[any](10))
	checked := make(map[string]bool)
	var resultAccess sync.Mutex
	for _, detour := range g.outbounds {
		tag := detour.Tag()
		realTag := RealTag(detour)
		if checked[realTag] {
			continue
		}
		history := g.history.LoadURLTestHistory(realTag)
		if !force && history != nil && time.Since(history.Time) < g.interval {
			continue
		}
		checked[realTag] = true
		p, loaded := g.outbound.Outbound(realTag)
		if !loaded {
			continue
		}
		b.Go(realTag, func() (any, error) {
			testCtx, cancel := context.WithTimeout(g.ctx, C.TCPTimeout)
			defer cancel()
			t, err := urltest.URLTest(testCtx, g.link, p)
			if err != nil {
				g.logger.Debug("outbound ", tag, " unavailable: ", err)
				g.history.DeleteURLTestHistory(realTag)
			} else {
				g.logger.Debug("outbound ", tag, " available: ", t, "ms")
				g.history.StoreURLTestHistory(realTag, &adapter.URLTestHistory{
					Time:  time.Now(),
					Delay: t,
				})
				g.clearUnavailable(realTag)
				resultAccess.Lock()
				result[tag] = t
				resultAccess.Unlock()
			}
			return nil, nil
		})
	}
	b.Wait()
	g.performUpdateCheck()
	return result, nil
}

func (g *StunnelGroup) performUpdateCheck() {
	var updated bool
	if outbound, exists := g.Select(N.NetworkTCP); outbound != nil && (g.selectedOutboundTCP == nil || (exists && outbound != g.selectedOutboundTCP)) {
		if g.selectedOutboundTCP != nil {
			updated = true
		}
		g.selectedOutboundTCP = outbound
	}
	if outbound, exists := g.Select(N.NetworkUDP); outbound != nil && (g.selectedOutboundUDP == nil || (exists && outbound != g.selectedOutboundUDP)) {
		if g.selectedOutboundUDP != nil {
			updated = true
		}
		g.selectedOutboundUDP = outbound
	}
	if updated {
		g.interruptGroup.Interrupt(g.interruptExternalConnections)
	}
}
```

- [ ] **Step 2: Verify build compiles**

Run: `go build ./protocol/group/`
Expected: no errors

- [ ] **Step 3: Commit**

```bash
git add protocol/group/stunnel.go
git commit -m "feat(stunnel): add stunnel outbound implementation with failover"
```

---

### Task 3: Register stunnel outbound

**Files:**
- Modify: `include/registry.go`

- [ ] **Step 1: Add `group.RegisterStunnel(registry)` call**

In `include/registry.go`, add the registration in `OutboundRegistry()` after the existing `group.RegisterURLTest(registry)` line (line 84):

```go
group.RegisterStunnel(registry)
```

- [ ] **Step 2: Verify full project build**

Run: `go build ./...`
Expected: no errors

- [ ] **Step 3: Commit**

```bash
git add include/registry.go
git commit -m "feat(stunnel): register stunnel outbound in registry"
```

---

### Task 4: Integration test

**Files:**
- Modify: `config.json` (test config)

- [ ] **Step 1: Create a test config with stunnel**

Create a test config (or modify `config.json` temporarily) using stunnel:

```json
{
  "log": {
    "level": "debug"
  },
  "inbounds": [
    {
      "type": "socks",
      "listen": "127.0.0.1",
      "listen_port": 11080
    },
    {
      "type": "http",
      "listen": "127.0.0.1",
      "listen_port": 11087
    }
  ],
  "outbounds": [
    {
      "type": "stunnel",
      "tag": "auto",
      "outbounds": ["新加坡1", "新加坡2", "E-IEPL-香港6", "direct"],
      "url": "https://cp.cloudflare.com/",
      "interval": "10s",
      "cooldown": "30s"
    },
    {
      "type": "vmess",
      "tag": "新加坡1",
      "server": "hoover.amswoop.com",
      "server_port": 31241,
      "uuid": "C74DE3A5-A547-45C1-8BA8-6FB3C0A19F2B",
      "security": "auto",
      "alter_id": 0
    },
    {
      "type": "vmess",
      "tag": "新加坡2",
      "server": "hoover.amswoop.com",
      "server_port": 31242,
      "uuid": "C74DE3A5-A547-45C1-8BA8-6FB3C0A19F2B",
      "security": "auto",
      "alter_id": 0
    },
    {
      "type": "vmess",
      "tag": "E-IEPL-香港6",
      "server": "hoover.amswoop.com",
      "server_port": 31202,
      "uuid": "C74DE3A5-A547-45C1-8BA8-6FB3C0A19F2B",
      "security": "auto",
      "alter_id": 0
    },
    {
      "type": "direct",
      "tag": "direct"
    }
  ],
  "route": {
    "final": "auto"
  }
}
```

- [ ] **Step 2: Build and run sing-box with the test config**

Run: `make build && ./sing-box run -c config.json`

Expected: starts without error, logs show health checks running through all outbounds.

- [ ] **Step 3: Test wget through stunnel**

Run: `http_proxy=http://127.0.0.1:11087 wget www.google.com`

Expected: request succeeds (goes through stunnel → selected outbound). If the first outbound fails, logs should show "stunnel: outbound X failed, trying next" and the request should succeed through the next available outbound.

- [ ] **Step 4: Verify logs show stunnel behavior**

Check logs for:
- `"stunnel: outbound ... failed: ... trying next"` messages on failover
- Health check results clearing unavailable marks
- Successful connection through the selected outbound

- [ ] **Step 5: Commit final state (restore config if needed)**

```bash
git add -A
git commit -m "feat(stunnel): integration test with stunnel outbound"
```
