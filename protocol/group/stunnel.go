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

func (s *Stunnel) UpdateOutbounds(tags []string) error {
	if len(tags) == 0 {
		return E.New("outbounds list cannot be empty")
	}
	newOutbounds := make([]adapter.Outbound, 0, len(tags))
	for i, tag := range tags {
		detour, loaded := s.outbound.Outbound(tag)
		if !loaded {
			return E.New("outbound ", i, " not found: ", tag)
		}
		newOutbounds = append(newOutbounds, detour)
	}

	s.group.access.Lock()
	s.group.outbounds = newOutbounds
	s.tags = tags

	// Clear unavailable marks for entries in the new list
	s.group.unavailableAccess.Lock()
	newUnavailable := make(map[string]time.Time)
	for tag, markedAt := range s.group.unavailable {
		for _, o := range newOutbounds {
			if RealTag(o) == tag {
				newUnavailable[tag] = markedAt
				break
			}
		}
	}
	s.group.unavailable = newUnavailable
	s.group.unavailableAccess.Unlock()

	// Check if current selections are still valid
	tcpValid := outboundInList(s.group.selectedOutboundTCP, newOutbounds)
	udpValid := outboundInList(s.group.selectedOutboundUDP, newOutbounds)
	if !tcpValid {
		s.group.selectedOutboundTCP = nil
	}
	if !udpValid {
		s.group.selectedOutboundUDP = nil
	}

	// Interrupt if selection changed and configured
	if (!tcpValid || !udpValid) && s.interruptExternalConnections {
		s.group.interruptGroup.Interrupt(true)
	}
	s.group.access.Unlock()

	// Trigger immediate re-selection + health check (outside lock)
	go s.group.CheckOutbounds(true)

	return nil
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
	access                       sync.RWMutex
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
	g.access.RLock()
	outbounds := g.outbounds
	g.access.RUnlock()
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
	for _, detour := range outbounds {
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
		for _, detour := range outbounds {
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
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
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
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
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
	g.access.RLock()
	testOutbounds := g.outbounds
	g.access.RUnlock()
	b, _ := batch.New(ctx, batch.WithConcurrencyNum[any](10))
	checked := make(map[string]bool)
	var resultAccess sync.Mutex
	for _, detour := range testOutbounds {
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

func outboundInList(target adapter.Outbound, list []adapter.Outbound) bool {
	if target == nil {
		return false
	}
	for _, o := range list {
		if o.Tag() == target.Tag() {
			return true
		}
	}
	return false
}
