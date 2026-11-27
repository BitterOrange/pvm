package mihomo

import "testing"

// Every host arch resolves to a pinned asset, and amd64 never to the
// x86-64-v3 build, which a gateway without AVX cannot run.
func TestAssetForEveryArch(t *testing.T) {
	for _, arch := range []string{"amd64", "arm64"} {
		if _, ok := pinned[assetName(arch)]; !ok {
			t.Errorf("no pinned Mihomo asset for %s", arch)
		}
	}
	if got := BinaryName("amd64"); got != "mihomo-linux-amd64-compatible" {
		t.Errorf("BinaryName(amd64) = %q", got)
	}
}
