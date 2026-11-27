package exitsvc

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// legacyUnitPath is where `pvm exit-agent install` put its unit.
const legacyUnitPath = "/etc/systemd/system/pvm-exit-agent.service"

// migratedConfig rebuilds the legacy service's flags as a config. The
// state directory and token file stay where they are, so the certificate
// fingerprint, the token and every registered peer carry over.
func migratedConfig(d Dirs) (Config, error) {
	b, err := os.ReadFile(legacyUnitPath)
	if err != nil {
		return Config{}, fmt.Errorf("--migrate: read %s: %w", legacyUnitPath, err)
	}
	return configFromLegacyUnit(string(b), DefaultConfig(d))
}

func configFromLegacyUnit(unit string, c Config) (Config, error) {
	var argv []string
	for line := range strings.Lines(unit) {
		if v, ok := strings.CutPrefix(strings.TrimSpace(line), "ExecStart="); ok {
			argv = strings.Fields(v)
		}
	}
	i := 0
	for i < len(argv) && argv[i] != "exit-agent" {
		i++
	}
	if i == len(argv) {
		return Config{}, errors.New("--migrate: the legacy unit has no `exit-agent` ExecStart")
	}
	c.AdminTokenFile = ""
	args := argv[i+1:]
	for j := 0; j < len(args); j++ {
		k, ok := strings.CutPrefix(args[j], "--")
		if !ok {
			return Config{}, fmt.Errorf("--migrate: unexpected argument %q in the legacy unit", args[j])
		}
		if k == "v" {
			continue
		}
		if j+1 >= len(args) {
			return Config{}, fmt.Errorf("--migrate: --%s has no value in the legacy unit", k)
		}
		v := args[j+1]
		j++
		var err error
		switch k {
		case "listen":
			c.Listen = v
		case "token-file":
			c.AdminTokenFile = v
		case "token":
			return Config{}, errors.New("--migrate: the legacy unit passes --token on the command line; move it into a 0600 file, set --token-file, then migrate")
		case "state":
			c.StateDir = v
		case "transport":
			c.Transport = v
		case "nat":
			c.NAT = v
		case "tun-prefix":
			c.TunPrefix = v
		case "subnet-base":
			c.SubnetBase, err = strconv.Atoi(v)
		case "udp-port":
			c.UDPPort, err = strconv.Atoi(v)
		case "tcp-port":
			c.TCPPort, err = strconv.Atoi(v)
		case "public-endpoint":
			c.PublicEndpoint = v
		case "timezone":
			c.Timezone = v
		case "domain-allow-cidr":
			for _, s := range strings.Split(v, ",") {
				if s = strings.TrimSpace(s); s != "" {
					c.DomainAllow = append(c.DomainAllow, s)
				}
			}
		default:
			return Config{}, fmt.Errorf("--migrate: unknown flag --%s in the legacy unit", k)
		}
		if err != nil {
			return Config{}, fmt.Errorf("--migrate: --%s %q: %w", k, v, err)
		}
	}
	if c.AdminTokenFile == "" {
		return Config{}, errors.New("--migrate: the legacy unit has no --token-file")
	}
	return c, c.Validate()
}

// retireLegacyUnit stops the old service and keeps its unit file as
// <unit>.migrated, so the takeover can be undone by hand.
func retireLegacyUnit() error {
	if err := run("systemctl", "disable", "--now", "pvm-exit-agent.service"); err != nil {
		return err
	}
	return os.Rename(legacyUnitPath, legacyUnitPath+".migrated")
}
