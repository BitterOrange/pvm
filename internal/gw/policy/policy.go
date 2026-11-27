package policy

import (
	"net"
	"sync"

	"github.com/huangzheng2016/pvm/internal/gw/dns"
	"github.com/huangzheng2016/pvm/internal/gw/rules"
)

type Route int

const (
	Direct Route = iota
	Proxy
)

func (r Route) String() string {
	if r == Proxy {
		return "proxy"
	}
	return "direct"
}

type Decision struct {
	Route Route
	Exit  string // exit-node name when Route == Proxy
}

type Engine struct {
	rules *rules.Engine
	pool  *dns.Pool

	mu      sync.RWMutex
	vmExit  map[string]string // vmID -> default exit name, "" = direct
	defExit string            // cluster-wide default exit, "" = direct
	paused  map[string]bool
	// unverified holds require-check VMs without a valid lease; like
	// paused, they lose all external egress.
	unverified map[string]bool
}

func New(eng *rules.Engine, pool *dns.Pool) *Engine {
	return &Engine{rules: eng, pool: pool, vmExit: make(map[string]string)}
}

// SetVMExit pins a VM's default route; exit "" clears the override and
// falls back to the cluster default.
func (e *Engine) SetVMExit(vmID, exit string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if exit == "" {
		delete(e.vmExit, vmID)
	} else {
		e.vmExit[vmID] = exit
	}
}

// SetDefaultExit sets the cluster-wide fallback used when neither a
// rule nor a per-VM override matches.
func (e *Engine) SetDefaultExit(exit string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.defExit = exit
}

// LookupByFakeIP resolves a fake IP back to the domain that produced
// it, so proxied connections can be routed by name (e.g. SNI-less or
// hard-coded DNS clients stay correct).
func (e *Engine) LookupByFakeIP(ip net.IP) (string, bool) {
	return e.pool.Lookup(ip)
}

func (e *Engine) IsFakeIP(ip net.IP) bool { return e.pool.Contains(ip) }

// ExitForVM returns the effective exit name for a VM: the per-VM
// override first, then the cluster default; "direct" and unset both
// normalize to "" (direct egress).
func (e *Engine) ExitForVM(vmID string) string {
	e.mu.RLock()
	defer e.mu.RUnlock()
	exit, ok := e.vmExit[vmID]
	if !ok {
		exit = e.defExit
	}
	if exit == "direct" {
		return ""
	}
	return exit
}

func (e *Engine) DecideFlow(domain string, dstIP net.IP, vmID string) Decision {
	if domain == "" && dstIP != nil && e.pool.Contains(dstIP) {
		domain, _ = e.pool.Lookup(dstIP)
	}
	if d, ok := e.rules.Match(domain, dstIP); ok {
		if d.Internal || d.Action == rules.Direct || d.Action == rules.Internal {
			// internal-rule traffic goes direct like its DNS path
			return Decision{Route: Direct}
		}
		return Decision{Route: Proxy, Exit: d.Exit}
	}
	if exit := e.ExitForVM(vmID); exit != "" {
		return Decision{Route: Proxy, Exit: exit}
	}
	return Decision{Route: Direct}
}

func (e *Engine) SetPaused(paused map[string]bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.paused = make(map[string]bool, len(paused))
	for k, v := range paused {
		if v {
			e.paused[k] = true
		}
	}
}

// SetUnverified replaces the set of require-check VMs that have no valid
// verification lease.
func (e *Engine) SetUnverified(unverified map[string]bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.unverified = make(map[string]bool, len(unverified))
	for k, v := range unverified {
		if v {
			e.unverified[k] = true
		}
	}
}

// EgressBlocked reports whether vmID may not reach anything outside the
// cluster: its egress is paused, or it requires a check and has no lease.
// The gateway relays enforce this; nft enforces it for forwarded traffic.
func (e *Engine) EgressBlocked(vmID string) bool {
	if e == nil {
		return false
	}
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.paused["*"] || e.paused[vmID] || e.unverified[vmID]
}

// AllowsPrivateDirect reports whether an explicit intranet-domain rule or an
// explicit direct/internal CIDR rule permits direct egress to a private or
// reserved address. Built-in cluster-internal suffixes never allow it.
func (e *Engine) AllowsPrivateDirect(domain string, ip net.IP) bool {
	if e == nil || e.rules == nil {
		return false
	}
	if d, ok := e.rules.Match(domain, nil); ok && !d.Internal && d.Action == rules.Internal {
		return true
	}
	if ip != nil {
		if d, ok := e.rules.Match("", ip); ok && (d.Action == rules.Direct || d.Action == rules.Internal) {
			return true
		}
	}
	return false
}
