package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"text/tabwriter"

	"github.com/huangzheng2016/pvm/internal/fsutil"
	"github.com/huangzheng2016/pvm/internal/paths"
	"github.com/huangzheng2016/pvm/internal/qemu"
)

const qemuUsage = "[ls [--json]] | install [<tag>] [--file <archive>] [--use] [--unpinned] [-y] | use <tag>|official [-y] | check [<tag>|official|--default] [--read-only] | rm <tag>|official [-y] | gc [-y]"

// splitArgs separates flags from positional arguments in any order.
func splitArgs(args []string, withValue map[string]bool) (pos []string, flags map[string]string, err error) {
	flags = map[string]string{}
	for i := 0; i < len(args); i++ {
		a := args[i]
		if !strings.HasPrefix(a, "-") || a == "-" {
			pos = append(pos, a)
			continue
		}
		name, val, hasVal := strings.Cut(strings.TrimLeft(a, "-"), "=")
		if withValue[name] && !hasVal {
			if i+1 >= len(args) {
				return nil, nil, fmt.Errorf("--%s needs a value", name)
			}
			i++
			val = args[i]
		}
		flags[name] = val
	}
	return pos, flags, nil
}

func knownFlags(flags map[string]string, allowed ...string) error {
	for f := range flags {
		ok := false
		for _, a := range allowed {
			if f == a {
				ok = true
			}
		}
		if !ok {
			return fmt.Errorf("unknown flag -%s", f)
		}
	}
	return nil
}

// CmdQEMU manages the QEMU builds pvm runs: project builds from the
// pvm-qemu repository and the platform's official QEMU.
func CmdQEMU(args []string) int {
	p, err := mustPaths()
	if err != nil {
		return fatalf("%v", err)
	}
	if len(args) == 0 {
		args = []string{"ls"}
	}
	sub, rest := args[0], args[1:]
	pos, flags, err := splitArgs(rest, map[string]bool{"file": true})
	if err != nil {
		return usageErr(err)
	}
	yes := flags["y"] != "" || hasFlag(flags, "y") || hasFlag(flags, "yes")
	switch sub {
	case "ls", "list":
		if err := knownFlags(flags, "json"); err != nil {
			return usageErr(err)
		}
		return qemuList(p, hasFlag(flags, "json"))
	case "install":
		if err := knownFlags(flags, "file", "use", "unpinned", "y", "yes"); err != nil || len(pos) > 1 {
			return usageErr(err)
		}
		tag := ""
		if len(pos) == 1 {
			tag = pos[0]
		}
		return qemuInstall(p, tag, flags["file"], hasFlag(flags, "use"), hasFlag(flags, "unpinned"), yes)
	case "use":
		if err := knownFlags(flags, "y", "yes"); err != nil || len(pos) != 1 {
			return usageErr(err)
		}
		return qemuUse(p, pos[0])
	case "check":
		if err := knownFlags(flags, "default", "read-only"); err != nil || len(pos) > 1 {
			return qemuCheckUsage(err)
		}
		id := ""
		if len(pos) == 1 {
			id = pos[0]
		}
		return qemuCheck(p, id, hasFlag(flags, "read-only"))
	case "rm", "remove":
		if err := knownFlags(flags, "y", "yes"); err != nil || len(pos) != 1 {
			return usageErr(err)
		}
		return qemuRemove(p, pos[0], yes)
	case "gc":
		if err := knownFlags(flags, "y", "yes"); err != nil || len(pos) != 0 {
			return usageErr(err)
		}
		return qemuGC(p, yes)
	}
	return usageErr(fmt.Errorf("unknown qemu subcommand %q", sub))
}

func hasFlag(flags map[string]string, name string) bool {
	_, ok := flags[name]
	return ok
}

func usageErr(err error) int {
	if err != nil {
		fmt.Fprintf(os.Stderr, "pvm: %v\n", err)
	}
	fmt.Fprintf(os.Stderr, "usage: pvm qemu %s\n", qemuUsage)
	return 2
}

func qemuCheckUsage(err error) int {
	usageErr(err)
	return 2
}

// resolveQEMU is the default build for work the CLI does itself (building
// golden images, the Windows 9p decision). When an older pvm's QEMU waits
// to be taken over and no daemon runs, the CLI takes it over itself: it
// holds the daemon run lock meanwhile, so no daemon starts on the old
// folder. With a daemon running, the daemon does it.
func resolveQEMU(p *paths.Paths) (*qemu.Toolchain, error) {
	tc, err := qemu.Resolve(p)
	if err == nil || len(qemu.AdoptionCandidates(p)) == 0 {
		return tc, err
	}
	lock, lerr := fsutil.TryLock(p.DaemonRunLock())
	if lerr != nil {
		return nil, fmt.Errorf("the QEMU an older pvm installed is waiting for the running daemon to take it over; restart it to finish the upgrade: pvm daemon restart")
	}
	errs := qemu.Adopt(p, func(dir string) bool { return qemu.InUse(p, dir) })
	lock.Unlock()
	for _, e := range errs {
		fmt.Fprintf(os.Stderr, "pvm: %v\n", e)
	}
	return qemu.Resolve(p)
}

type qemuListBuild struct {
	qemu.Build
	Default bool   `json:"default"`
	Label   string `json:"label"`
	SizeMB  int64  `json:"size_mb,omitempty"`
	Problem string `json:"problem,omitempty"`
}

type qemuListing struct {
	Default     string            `json:"default"`
	Builds      []qemuListBuild   `json:"builds"`
	Recommended string            `json:"recommended"`
	Revoked     map[string]string `json:"revoked,omitempty"`
	Official    *qemu.Official    `json:"official,omitempty"`
	OfficialErr string            `json:"official_missing,omitempty"`
	Legacy      []qemu.Candidate  `json:"legacy,omitempty"`
	Leftovers   []qemu.Leftover   `json:"leftovers,omitempty"`
}

func qemuList(p *paths.Paths, asJSON bool) int {
	st, err := qemu.LoadState(p)
	if err != nil {
		return fatalf("%v", err)
	}
	pins, err := qemu.LoadPins()
	if err != nil {
		return fatalf("%v", err)
	}
	l := qemuListing{Default: st.Default, Recommended: pins.RecommendedTag(), Revoked: pins.Revoked}
	for _, b := range st.Builds {
		lb := qemuListBuild{Build: b, Default: b.ID == st.Default, Label: b.Label()}
		if b.Dir != "" {
			lb.SizeMB = dirSizeMB(filepath.Dir(b.Dir))
		}
		if reason := pins.RevokedReason(b.ID); reason != "" && b.Source == qemu.SourceProject {
			lb.Problem = "withdrawn: " + reason
		}
		l.Builds = append(l.Builds, lb)
	}
	if o, err := qemu.DetectOfficial(); err == nil {
		l.Official = o
	} else {
		l.OfficialErr = err.Error()
	}
	l.Legacy = qemu.LegacyTrees(p)
	l.Leftovers = qemu.Leftovers(p, upgradeKeeps(p))
	if asJSON {
		data, _ := json.MarshalIndent(l, "", "  ")
		fmt.Println(string(data))
		return 0
	}
	if len(l.Builds) == 0 {
		fmt.Println("no QEMU builds registered")
	} else {
		tw := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
		fmt.Fprintln(tw, "DEFAULT\tBUILD\tSOURCE\tQEMU\tVIRTIO-9P\tSIZE\tPATH")
		for _, b := range l.Builds {
			mark, size := "", ""
			if b.Default {
				mark = "*"
			}
			if b.SizeMB > 0 {
				size = fmt.Sprintf("%d MB", b.SizeMB)
			}
			src := "project build"
			if b.Source == qemu.SourceOfficial {
				src = qemu.ChannelLabel(b.Channel)
			}
			nine := "no"
			if b.Caps.Virtio9p {
				nine = "yes"
			}
			fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n", mark, b.ID, src, b.Version, nine, size, b.System)
		}
		tw.Flush()
		for _, b := range l.Builds {
			if b.Problem != "" {
				fmt.Printf("%s: %s\n", b.ID, b.Problem)
			}
		}
	}
	if l.Recommended != "" {
		state := "not installed: pvm qemu install"
		if st.Find(l.Recommended) != nil {
			state = "installed"
		}
		fmt.Printf("recommended by this pvm: %s (%s)\n", l.Recommended, state)
	}
	if l.Official != nil && st.Find(qemu.OfficialID) == nil {
		fmt.Printf("found %s %s at %s; to use it: pvm qemu use official\n", qemu.ChannelLabel(l.Official.Channel), l.Official.PackageVersion, l.Official.System)
	}
	for _, c := range l.Legacy {
		if c.Tag != "" {
			fmt.Printf("%s holds pvm-qemu %s from an older pvm; the daemon takes it over at its next start\n", c.Dir, c.Tag)
		}
	}
	if n := len(l.Leftovers); n > 0 {
		fmt.Printf("%d leftover folder(s) can be removed: pvm qemu gc\n", n)
	}
	return 0
}

func dirSizeMB(dir string) int64 {
	var n int64
	filepath.Walk(dir, func(_ string, fi os.FileInfo, err error) error {
		if err == nil && fi.Mode().IsRegular() {
			n += fi.Size()
		}
		return nil
	})
	return n >> 20
}

func qemuInstall(p *paths.Paths, tag, file string, use, unpinned, yes bool) int {
	if !yes {
		if !stdinIsTTY() {
			return fatalf("pvm qemu install needs -y when not run at a terminal")
		}
		what := "the QEMU build this pvm recommends"
		if tag != "" {
			what = "QEMU build " + tag
		}
		if file != "" {
			what = file
		}
		if !confirmYes(os.Stdin, os.Stdout, "Install "+what+" from the pvm-qemu project?") {
			return 1
		}
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	tty := isTTY(os.Stdout)
	lastPct := -1
	res, err := qemu.Install(ctx, p, qemu.InstallOptions{
		Tag: tag, File: file, Use: use, Unpinned: unpinned,
		Log: func(s string) {
			if tty && lastPct >= 0 {
				fmt.Println()
			}
			lastPct = -1
			fmt.Println(s)
		},
		Progress: func(done, total int64) {
			if total <= 0 {
				return
			}
			pct := int(done * 100 / total)
			if tty {
				if pct != lastPct {
					fmt.Printf("\r  %3d%%  %d of %d MB", pct, done>>20, total>>20)
				}
			} else if pct/10 != lastPct/10 {
				fmt.Printf("  %d%%\n", pct)
			}
			lastPct = pct
		},
	})
	if tty && lastPct >= 0 {
		fmt.Println()
	}
	if err != nil {
		return fatalf("%v", err)
	}
	b := res.Build
	switch {
	case res.AlreadyInstalled && res.MadeDefault:
		fmt.Printf("%s is already installed; it is now the default\n", b.ID)
	case res.AlreadyInstalled:
		fmt.Printf("%s is already installed\n", b.ID)
	case res.MadeDefault:
		fmt.Printf("installed %s (QEMU %s) and made it the default\n", b.ID, b.Version)
	default:
		fmt.Printf("installed %s (QEMU %s); to use it: pvm qemu use %s\n", b.ID, b.Version, b.ID)
	}
	return 0
}

func qemuUse(p *paths.Paths, id string) int {
	if id == qemu.OfficialID {
		b, err := qemu.UseOfficial(p, true)
		if err != nil {
			return fatalf("%v", err)
		}
		fmt.Printf("using %s (%s)\n", b.Label(), b.System)
		printRunningOnOther(p)
		return 0
	}
	if err := qemu.Verify(p, id); err != nil {
		return fatalf("%v", err)
	}
	if err := qemu.UpdateState(p, func(s *qemu.State) error {
		if s.Find(id) == nil {
			return fmt.Errorf("QEMU build %s is not installed; install it: pvm qemu install %s --use", id, id)
		}
		s.Default = id
		return nil
	}); err != nil {
		return fatalf("%v", err)
	}
	fmt.Printf("using %s\n", id)
	printRunningOnOther(p)
	return 0
}

// printRunningOnOther names VMs that keep their QEMU until they restart.
func printRunningOnOther(p *paths.Paths) {
	if _, err := qemu.Resolve(p); err != nil {
		return
	}
	fmt.Println("running VMs keep the QEMU they started with until they restart; RAM snapshots restore only into the build that wrote them")
}

// qemuCheck: exit 0 ok, 3 not set up, 1 failing, 2 usage.
func qemuCheck(p *paths.Paths, id string, readOnly bool) int {
	if id == "" && !readOnly && len(qemu.AdoptionCandidates(p)) > 0 {
		if _, err := resolveQEMU(p); err != nil {
			fmt.Fprintf(os.Stderr, "pvm: %v\n", err)
		}
	}
	err := qemu.Verify(p, id)
	switch {
	case err == nil:
		st, _ := qemu.LoadState(p)
		shown := id
		if shown == "" && st != nil {
			shown = st.Default
		}
		fmt.Printf("%s passed its checks\n", shown)
		return 0
	case errors.Is(err, qemu.ErrNotSetUp):
		if len(qemu.AdoptionCandidates(p)) > 0 {
			fmt.Println("the QEMU an older pvm installed is taken over at the daemon's next start")
			return 0
		}
		fmt.Fprintf(os.Stderr, "pvm: %v\n", err)
		return 3
	}
	fmt.Fprintf(os.Stderr, "pvm: %v\n", err)
	return 1
}

func qemuRemove(p *paths.Paths, id string, yes bool) int {
	if !yes {
		if !stdinIsTTY() {
			return fatalf("pvm qemu rm needs -y when not run at a terminal")
		}
		if !confirmAction(os.Stdin, os.Stdout, "Remove QEMU build "+id+"?") {
			return 1
		}
	}
	if err := qemu.Remove(p, id, func(dir string) bool { return qemu.InUse(p, dir) }); err != nil {
		return fatalf("%v", err)
	}
	fmt.Printf("removed %s\n", id)
	return 0
}

func qemuGC(p *paths.Paths, yes bool) int {
	list := qemu.Leftovers(p, upgradeKeeps(p))
	if len(list) == 0 {
		fmt.Println("nothing to remove")
		return 0
	}
	var total int64
	for _, l := range list {
		fmt.Printf("%6d MB  %s  (%s)\n", l.Size>>20, l.Path, l.Reason)
		total += l.Size
	}
	if !yes {
		if !stdinIsTTY() {
			return fatalf("pvm qemu gc needs -y when not run at a terminal")
		}
		if !confirmAction(os.Stdin, os.Stdout, fmt.Sprintf("Delete these (%d MB)?", total>>20)) {
			return 1
		}
	}
	failed := 0
	for _, l := range list {
		if qemu.InUse(p, l.Path) {
			fmt.Fprintf(os.Stderr, "pvm: kept %s: a VM runs from it\n", l.Path)
			failed++
			continue
		}
		if err := os.RemoveAll(l.Path); err != nil {
			fmt.Fprintf(os.Stderr, "pvm: %v\n", err)
			failed++
		}
	}
	if failed > 0 {
		return 1
	}
	fmt.Printf("removed %d MB\n", total>>20)
	return 0
}

// upgradeKeeps keeps the old QEMU folder that a pvm upgrade recovery point
// still restores on --rollback.
func upgradeKeeps(p *paths.Paths) func(string) bool {
	exe, err := os.Executable()
	if err != nil {
		return nil
	}
	raw, err := os.ReadFile(exe + ".upgrade.json")
	if err != nil {
		return nil
	}
	var state upgradeRecovery
	if json.Unmarshal(raw, &state) != nil || state.QEMUBackup == "" {
		return nil
	}
	keep := filepath.Clean(state.QEMUBackup)
	if real, err := filepath.EvalSymlinks(keep); err == nil {
		keep = real
	}
	return func(path string) bool {
		clean := filepath.Clean(path)
		return clean == keep || clean == filepath.Clean(state.QEMUBackup)
	}
}
