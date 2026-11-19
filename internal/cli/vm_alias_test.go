package cli

import (
	"maps"
	"testing"
)

func TestParseImageBuildArgs(t *testing.T) {
	for _, args := range [][]string{
		{"ubuntu:24.04", "--keep-vm", "--cpus", "2", "--mem", "2048", "--disk", "10"},
		{"--keep-vm", "--cpus", "2", "--mem", "2048", "--disk", "10", "ubuntu:24.04"},
	} {
		opts, ok := parseImageBuildArgs(args)
		if !ok {
			t.Fatalf("parseImageBuildArgs(%v) failed", args)
		}
		if opts.spec != "ubuntu:24.04" || !opts.keep || opts.cpus != 2 || opts.mem != 2048 || opts.disk != 10 {
			t.Errorf("parseImageBuildArgs(%v) = %+v", args, opts)
		}
	}
	for _, args := range [][]string{{}, {"ubuntu:24.04", "debian:13"}, {"--cpus", "2"}} {
		if _, ok := parseImageBuildArgs(args); ok {
			t.Errorf("parseImageBuildArgs(%v) unexpectedly passed", args)
		}
	}
}

func TestParseResizeArgs(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want map[string]int
	}{
		{[]string{"dev1", "40"}, map[string]int{"size_gb": 40}},
		{[]string{"dev1", "--cpus", "4"}, map[string]int{"cpus": 4}},
		{[]string{"--mem", "8192", "dev1"}, map[string]int{"mem_mb": 8192}},
		{[]string{"dev1", "40", "--cpus=4", "--mem", "8192"}, map[string]int{"size_gb": 40, "cpus": 4, "mem_mb": 8192}},
	} {
		name, body, code := parseResizeArgs(tc.args)
		if code != 0 || name != "dev1" || !maps.Equal(body, tc.want) {
			t.Errorf("parseResizeArgs(%v) = %q, %v, %d; want dev1, %v", tc.args, name, body, code, tc.want)
		}
	}
	for _, args := range [][]string{
		{}, {"dev1"}, {"dev1", "40", "50"}, {"--cpus", "4"},
		{"dev1", "0"}, {"dev1", "big"}, {"dev1", "--cpus", "0"}, {"dev1", "40", "--mem", "0"}, {"dev1", "--disk", "40"},
	} {
		if _, _, code := parseResizeArgs(args); code == 0 {
			t.Errorf("parseResizeArgs(%v) unexpectedly passed", args)
		}
	}
}

func TestResolveImageSpec(t *testing.T) {
	cases := []struct {
		name, image, distro, version, want string
	}{
		{"image only", "debian:13", "", "", "debian:13"},
		{"distro only", "", "debian", "", "debian"},
		{"distro and version", "", "debian", "13", "debian:13"},
		{"image with no version plus version", "debian", "", "13", "debian:13"},
		{"same version", "debian:13", "", "13", "debian:13"},
		{"same distro keeps version", "debian:13", "debian", "", "debian:13"},
		{"version only errors", "", "", "13", ""},
		{"conflicting version", "debian:13", "", "12", ""},
		{"conflicting distro", "ubuntu:24.04", "debian", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := resolveImageSpec(tc.image, tc.distro, tc.version)
			if tc.want == "" {
				if err == nil {
					t.Fatalf("expected error, got %q", got)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}
