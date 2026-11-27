package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os/exec"
	"sort"
	"strconv"
	"time"

	"github.com/huangzheng2016/pvm/internal/netcheck"
)

type ExitProbeResult struct {
	IP      string            `json:"ip"`
	Service string            `json:"service"`
	Geo     *netcheck.GeoInfo `json:"geo,omitempty"`
}

// ProbeExit samples the public exit IP (and optionally geo) through the
// exit's policy-routing mark (or main table when exitName is "direct"/"").
func ProbeExit(ctx context.Context, c *Config, exitName string, wantGeo bool) (*ExitProbeResult, error) {
	mark := -1
	if def, ok := c.Exits[exitName]; ok && def.Provider == "mihomo" && def.MihomoPort > 0 {
		// A Mihomo exit has no tunnel mark; sample through its SOCKS
		// listener, letting the node resolve the echo service's name.
		socks := net.JoinHostPort("127.0.0.1", strconv.Itoa(def.MihomoPort))
		return probeExitWith(ctx, func(ctx context.Context, _, addr string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(addr)
			if err != nil {
				return nil, err
			}
			n, err := strconv.Atoi(port)
			if err != nil {
				return nil, err
			}
			return socks5Dial(ctx, socks, host, n)
		}, wantGeo)
	}
	if exitName != "" && exitName != "direct" {
		names := make([]string, 0, len(c.Exits))
		for n := range c.Exits {
			names = append(names, n)
		}
		sort.Strings(names)
		idx := exitIndex(names, exitName)
		if idx < 0 {
			return nil, fmt.Errorf("exit %q is not in gateway config", exitName)
		}
		mark = fwMarkBase + idx
	}
	dial := func(ctx context.Context, _, addr string) (net.Conn, error) {
		d := net.Dialer{Timeout: 6 * time.Second}
		if mark >= 0 {
			d.Control = soMark(mark)
		}
		return d.DialContext(ctx, "tcp4", addr)
	}
	return probeExitWith(ctx, dial, wantGeo)
}

func probeExitWith(ctx context.Context, dial func(context.Context, string, string) (net.Conn, error), wantGeo bool) (*ExitProbeResult, error) {
	ip, svc, err := netcheck.ProbeExitIP(ctx, dial, nil)
	if err != nil {
		return nil, err
	}
	out := &ExitProbeResult{IP: ip, Service: svc}
	if wantGeo {
		geo, err := netcheck.LookupGeo(ctx, dial, ip)
		if err != nil {
			return out, fmt.Errorf("geo lookup for exit IP failed: %w", err)
		}
		out.Geo = geo
	}
	return out, nil
}

// ReadDropCounters reads the named nft drop counters so `pvm check` can prove
// a negative probe was dropped by the gateway rather than merely timing out.
func parseDropCounters(out []byte) (map[string]uint64, error) {
	var doc struct {
		Nftables []struct {
			Counter *struct {
				Name    string `json:"name"`
				Packets uint64 `json:"packets"`
			} `json:"counter"`
		} `json:"nftables"`
	}
	res := map[string]uint64{}
	if err := json.Unmarshal(out, &doc); err != nil {
		return nil, err
	}
	for _, item := range doc.Nftables {
		if item.Counter != nil && item.Counter.Name != "" {
			res[item.Counter.Name] = item.Counter.Packets
		}
	}
	return res, nil
}

func ReadDropCounters() (map[string]uint64, error) {
	out, err := exec.Command("nft", "-j", "list", "table", "inet", "pvm").Output()
	if err != nil {
		return nil, err
	}
	return parseDropCounters(out)
}
