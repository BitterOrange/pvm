package daemon

import (
	"errors"
	"strings"
	"testing"
)

func TestAppliedResult(t *testing.T) {
	timeout := errors.New("exit status 3")
	for _, tc := range []struct {
		name, out string
		err       error
		want      string // "" = success
	}{
		{"applied", `{"sha256":"abc"}`, nil, ""},
		{"step failed", `{"sha256":"abc","error":"rules: bad"}`, timeout, "failed to apply the new config: rules: bad"},
		{"older config", `{"sha256":"old"}`, timeout, "did not apply the new config within"},
		{"no receipt", ``, timeout, "did not apply the new config within"},
		{"garbage", `nope`, nil, "receipt unreadable"},
	} {
		err := appliedResult(tc.out, tc.err, "abc")
		if tc.want == "" {
			if err != nil {
				t.Errorf("%s: %v", tc.name, err)
			}
			continue
		}
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: got %v, want %q", tc.name, err, tc.want)
		}
	}
}

func TestCheckMemMB(t *testing.T) {
	for _, c := range []struct {
		mb, host int
		ok       bool
	}{
		{4096, 8192, true},
		{8192, 8192, true},
		{900000, 8192, false},
		{64, 8192, false},
	} {
		if err := checkMemMB(c.mb, c.host); (err == nil) != c.ok {
			t.Errorf("checkMemMB(%d, %d) = %v, want ok=%v", c.mb, c.host, err, c.ok)
		}
	}
}
