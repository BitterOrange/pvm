package qemu

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"sync"
)

// pvmqemu.json pins the pvm-qemu release assets this pvm accepts without
// gh: their SHA-256 was checked against the release's build-provenance
// attestation by script/ci/fetch-qemu-builds.sh before the pin was merged.
//
//go:embed pvmqemu.json
var pinsJSON []byte

// PinRepo and PinWorkflow are the only source of project builds.
const (
	PinRepo     = "BitterOrange/pvm-qemu"
	PinWorkflow = "BitterOrange/pvm-qemu/.github/workflows/release.yml"
)

var tagRE = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+(-rc[0-9]+)?-pvm[0-9]+$`)

// ValidTag reports whether s is a pvm-qemu release tag.
func ValidTag(s string) bool { return tagRE.MatchString(s) }

type PinHost struct {
	Asset        string `json:"asset"`
	SHA256       string `json:"sha256"`
	Size         int64  `json:"size"`
	TreeSHA256   string `json:"tree_sha256"` // SHA-256 of the archive's SHA256SUMS
	SystemSHA256 string `json:"system_sha256"`
}

type PinBuild struct {
	Hosts map[string]PinHost `json:"hosts"`
	// Declares holds capabilities that change guest-visible CPU behaviour.
	// They come only from here, never from the archive.
	Declares map[string]bool `json:"declares,omitempty"`
}

type Pins struct {
	Schema      int                 `json:"schema"`
	Repo        string              `json:"repo"`
	Workflow    string              `json:"workflow"`
	Recommended map[string]string   `json:"recommended"`
	Builds      map[string]PinBuild `json:"builds"`
	Revoked     map[string]string   `json:"revoked"`
}

var (
	pinsOnce sync.Once
	pinsVal  *Pins
	pinsErr  error
)

// LoadPins returns the pins built into this pvm.
func LoadPins() (*Pins, error) {
	pinsOnce.Do(func() { pinsVal, pinsErr = parsePins(pinsJSON) })
	return pinsVal, pinsErr
}

func parsePins(data []byte) (*Pins, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var p Pins
	if err := dec.Decode(&p); err != nil {
		return nil, fmt.Errorf("pvmqemu.json: %w", err)
	}
	if p.Schema != 1 || p.Repo != PinRepo || p.Workflow != PinWorkflow {
		return nil, fmt.Errorf("pvmqemu.json: unexpected schema, repo or workflow")
	}
	hex64 := regexp.MustCompile(`^[0-9a-f]{64}$`)
	for tag, b := range p.Builds {
		if !ValidTag(tag) {
			return nil, fmt.Errorf("pvmqemu.json: bad tag %q", tag)
		}
		for host, h := range b.Hosts {
			if !hex64.MatchString(h.SHA256) || !hex64.MatchString(h.TreeSHA256) || !hex64.MatchString(h.SystemSHA256) || h.Size <= 0 || h.Asset != fmt.Sprintf("pvm-qemu-%s-%s.tar.gz", tag, host) {
				return nil, fmt.Errorf("pvmqemu.json: bad pin for %s on %s", tag, host)
			}
		}
	}
	for host, tag := range p.Recommended {
		if _, ok := p.Builds[tag].Hosts[host]; !ok {
			return nil, fmt.Errorf("pvmqemu.json: recommended %s for %s is not pinned", tag, host)
		}
		if _, ok := p.Revoked[tag]; ok {
			return nil, fmt.Errorf("pvmqemu.json: recommended %s is revoked", tag)
		}
	}
	return &p, nil
}

// Host returns the pin of tag for this host.
func (p *Pins) Host(tag string) (PinHost, bool) {
	h, ok := p.Builds[tag].Hosts[HostKey()]
	return h, ok
}

// RecommendedTag is the build this pvm installs by default on this host.
func (p *Pins) RecommendedTag() string { return p.Recommended[HostKey()] }

// BySHA256 finds the pinned build whose archive has this digest.
func (p *Pins) BySHA256(sha string) (string, PinHost, bool) {
	return p.find(func(h PinHost) bool { return h.SHA256 == sha })
}

// ByTree finds the pinned build whose SHA256SUMS has this digest.
func (p *Pins) ByTree(sha string) (string, PinHost, bool) {
	return p.find(func(h PinHost) bool { return h.TreeSHA256 == sha })
}

func (p *Pins) find(match func(PinHost) bool) (string, PinHost, bool) {
	tags := make([]string, 0, len(p.Builds))
	for tag := range p.Builds {
		tags = append(tags, tag)
	}
	sort.Strings(tags)
	for _, tag := range tags {
		if h, ok := p.Builds[tag].Hosts[HostKey()]; ok && match(h) {
			return tag, h, true
		}
	}
	return "", PinHost{}, false
}

// RevokedReason is non-empty when this pvm withdrew the build.
func (p *Pins) RevokedReason(tag string) string { return p.Revoked[tag] }
