package exit

import (
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"strings"
)

// invitePrefix versions the invite string format.
const invitePrefix = "pvmx1:"

// Invite carries what a client needs to join an exit without a typed
// token: where the control plane is, the fingerprint that authenticates
// it, and a one-time secret that authenticates the client. Copy it
// through a channel an attacker cannot alter or read, such as the ssh
// session the invite was created in.
type Invite struct {
	Server      string `json:"server"`
	Fingerprint string `json:"fingerprint"`
	Secret      string `json:"secret"`
}

func (i Invite) validate() error {
	host, port, err := net.SplitHostPort(i.Server)
	if err != nil || host == "" || port == "" {
		return fmt.Errorf("invite server %q is not host:port", i.Server)
	}
	if b, err := hex.DecodeString(i.Fingerprint); err != nil || len(b) != 32 {
		return fmt.Errorf("invite fingerprint is not a sha256 hex digest")
	}
	if b, err := base64.RawURLEncoding.DecodeString(i.Secret); err != nil || len(b) < 16 {
		return fmt.Errorf("invite secret is malformed")
	}
	return nil
}

// String encodes the invite for copy and paste.
func (i Invite) String() string {
	b, _ := json.Marshal(i)
	return invitePrefix + base64.RawURLEncoding.EncodeToString(b)
}

// IsInvite reports whether s looks like an invite string.
func IsInvite(s string) bool { return strings.HasPrefix(strings.TrimSpace(s), invitePrefix) }

// ParseInvite decodes and validates an invite string.
func ParseInvite(s string) (Invite, error) {
	s = strings.TrimSpace(s)
	if !strings.HasPrefix(s, invitePrefix) {
		return Invite{}, fmt.Errorf("not a pvm exit invite (expected %s...)", invitePrefix)
	}
	b, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(s, invitePrefix))
	if err != nil {
		return Invite{}, fmt.Errorf("invite is truncated or mistyped")
	}
	var inv Invite
	dec := json.NewDecoder(strings.NewReader(string(b)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&inv); err != nil {
		return Invite{}, fmt.Errorf("invite is malformed: %w", err)
	}
	inv.Fingerprint = strings.ToLower(inv.Fingerprint)
	if err := inv.validate(); err != nil {
		return Invite{}, err
	}
	return inv, nil
}
