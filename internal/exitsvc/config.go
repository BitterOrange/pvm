// Package exitsvc runs the pvm exit server (pvm-exit) as a service: its
// config file, systemd units, install and admin commands. The exit protocol
// itself lives in internal/exit.
package exitsvc

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/huangzheng2016/pvm/internal/exit"
	"gopkg.in/yaml.v3"
)

// Config is config.yaml. Unknown keys are errors, so a typo cannot silently
// fall back to a default.
type Config struct {
	Listen         string   `yaml:"listen"`
	PublicEndpoint string   `yaml:"public_endpoint,omitempty"`
	Transport      string   `yaml:"transport"`
	NAT            string   `yaml:"nat"`
	TunPrefix      string   `yaml:"tun_prefix"`
	SubnetBase     int      `yaml:"subnet_base"`
	UDPPort        int      `yaml:"udp_port"`
	TCPPort        int      `yaml:"tcp_port"`
	Timezone       string   `yaml:"timezone,omitempty"`
	DomainAllow    []string `yaml:"domain_allow_cidr,omitempty"`
	StateDir       string   `yaml:"state_dir"`
	AdminTokenFile string   `yaml:"admin_token_file"`
}

// Mode is how pvm-exit is installed.
type Mode string

const (
	// ModeSystem is a system service running as the pvm-exit user.
	ModeSystem Mode = "system"
	// ModeUser is a systemd user service of the invoking user, with
	// userspace NAT and no root at any point.
	ModeUser Mode = "user"
)

const (
	systemConfigDir = "/etc/pvm-exit"
	systemStateDir  = "/var/lib/pvm-exit"
	// ServiceUser runs the system service.
	ServiceUser = "pvm-exit"
	unitName    = "pvm-exit.service"
)

// Dirs are the config and state locations of one mode.
type Dirs struct {
	Mode   Mode
	Config string // directory holding config.yaml and admin.token
	State  string // cert, wireguard key, peers.json, auth.json
}

// ConfigPath is config.yaml in d.
func (d Dirs) ConfigPath() string { return filepath.Join(d.Config, "config.yaml") }

// DirsFor returns the locations of mode; user mode follows the XDG base
// directory variables.
func DirsFor(mode Mode) (Dirs, error) {
	if mode == ModeSystem {
		return Dirs{Mode: ModeSystem, Config: systemConfigDir, State: systemStateDir}, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return Dirs{}, err
	}
	cfg := os.Getenv("XDG_CONFIG_HOME")
	if !filepath.IsAbs(cfg) {
		cfg = filepath.Join(home, ".config")
	}
	state := os.Getenv("XDG_STATE_HOME")
	if !filepath.IsAbs(state) {
		state = filepath.Join(home, ".local", "state")
	}
	return Dirs{Mode: ModeUser, Config: filepath.Join(cfg, "pvm-exit"), State: filepath.Join(state, "pvm-exit")}, nil
}

// DefaultConfig is what init writes for mode.
func DefaultConfig(d Dirs) Config {
	c := Config{
		Listen:         ":18443",
		Transport:      "both",
		NAT:            exit.NATAuto,
		TunPrefix:      "pvmexit",
		SubnetBase:     99,
		UDPPort:        18443,
		TCPPort:        18444,
		StateDir:       d.State,
		AdminTokenFile: filepath.Join(d.Config, "admin.token"),
	}
	if d.Mode == ModeUser {
		c.NAT = exit.NATUserspace
	}
	return c
}

// ParseConfig decodes one YAML document strictly and validates it.
func ParseConfig(data []byte) (Config, error) {
	var c Config
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&c); err != nil {
		if errors.Is(err, io.EOF) {
			return Config{}, errors.New("config is empty")
		}
		return Config{}, err
	}
	var extra any
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		return Config{}, errors.New("config must contain exactly one YAML document")
	}
	return c, c.Validate()
}

// LoadConfig reads and validates path.
func LoadConfig(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, err
	}
	c, err := ParseConfig(data)
	if err != nil {
		return Config{}, fmt.Errorf("%s: %w", path, err)
	}
	return c, nil
}

// Marshal renders the config with a header naming the file's owner command.
func (c Config) Marshal() []byte {
	b, _ := yaml.Marshal(c)
	return append([]byte("# pvm-exit configuration; unknown keys are rejected.\n"), b...)
}

func validPort(p int) bool { return p >= 1 && p <= 65535 }

// Validate checks every field without touching the system.
func (c Config) Validate() error {
	if _, port, err := net.SplitHostPort(c.Listen); err != nil {
		return fmt.Errorf("listen %q is not [host]:port", c.Listen)
	} else if n, err := strconv.Atoi(port); err != nil || !validPort(n) {
		return fmt.Errorf("listen port %q is invalid", port)
	}
	switch c.Transport {
	case "both", "udp", "tcp":
	default:
		return fmt.Errorf("transport %q is not both, udp or tcp", c.Transport)
	}
	switch c.NAT {
	case exit.NATAuto, exit.NATNftables, exit.NATIptables, exit.NATUserspace:
	default:
		return fmt.Errorf("nat %q is not auto, nft, iptables or userspace", c.NAT)
	}
	if c.SubnetBase < 1 || c.SubnetBase > 254 {
		return fmt.Errorf("subnet_base %d is outside 1..254", c.SubnetBase)
	}
	if !validPort(c.UDPPort) || !validPort(c.TCPPort) {
		return fmt.Errorf("udp_port and tcp_port must be 1..65535")
	}
	if c.TunPrefix == "" {
		return errors.New("tun_prefix is empty")
	}
	if !filepath.IsAbs(c.StateDir) || !filepath.IsAbs(c.AdminTokenFile) {
		return errors.New("state_dir and admin_token_file must be absolute paths")
	}
	for _, s := range append([]string{c.PublicEndpoint, c.Timezone, c.TunPrefix}, c.DomainAllow...) {
		if strings.ContainsAny(s, " \t\r\n\x00") {
			return fmt.Errorf("value %q must not contain whitespace", s)
		}
	}
	return nil
}

// ListenPort is the control-plane port.
func (c Config) ListenPort() string {
	_, port, _ := net.SplitHostPort(c.Listen)
	return port
}

// ServerConfig maps the file onto the exit server with the admin token.
func (c Config) ServerConfig(adminToken string) exit.ServerConfig {
	return exit.ServerConfig{
		Listen:         c.Listen,
		Token:          adminToken,
		StateDir:       c.StateDir,
		PublicEndpoint: c.PublicEndpoint,
		UDPPort:        c.UDPPort,
		TCPPort:        c.TCPPort,
		Transports:     c.Transport,
		NAT:            c.NAT,
		TunPrefix:      c.TunPrefix,
		SubnetBase:     c.SubnetBase,
		DomainAllow:    c.DomainAllow,
		SuggestedTZ:    c.Timezone,
	}
}

// ReadAdminToken reads the admin token file, which must be a private
// regular file holding one token.
func ReadAdminToken(path string) (string, error) {
	fi, err := os.Lstat(path)
	if err != nil {
		return "", fmt.Errorf("admin token: %w", err)
	}
	if !fi.Mode().IsRegular() || fi.Mode().Perm()&0o077 != 0 {
		return "", fmt.Errorf("admin token %s must be a regular file readable only by its owner (mode 0600)", path)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("admin token: %w", err)
	}
	tok := strings.TrimSpace(string(b))
	if tok == "" || strings.ContainsAny(tok, " \t\r\n") {
		return "", fmt.Errorf("admin token %s must contain one nonempty token", path)
	}
	return tok, nil
}
