package qemu

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

var sumLineRE = regexp.MustCompile(`^([0-9a-f]{64}) [ *](.+)$`)

// readSums parses a tree's SHA256SUMS into relative path -> digest.
func readSums(dir string) (map[string]string, error) {
	f, err := os.Open(filepath.Join(dir, "SHA256SUMS"))
	if err != nil {
		return nil, err
	}
	defer f.Close()
	sums := map[string]string{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimRight(sc.Text(), "\r")
		if line == "" {
			continue
		}
		m := sumLineRE.FindStringSubmatch(line)
		if m == nil {
			return nil, fmt.Errorf("SHA256SUMS: bad line %q", line)
		}
		rel := m[2]
		if strings.HasPrefix(rel, "/") || strings.Contains(rel, "\\") || rel == ".." || strings.HasPrefix(rel, "../") || strings.Contains(rel, "/../") {
			return nil, fmt.Errorf("SHA256SUMS: unsafe path %q", rel)
		}
		if _, dup := sums[rel]; dup {
			return nil, fmt.Errorf("SHA256SUMS: %s listed twice", rel)
		}
		sums[rel] = m[1]
	}
	return sums, sc.Err()
}

// listTree returns every file below dir, relative and slash-separated,
// except SHA256SUMS. Symlinks and special files are refused: a tree may
// hold only regular files and directories.
func listTree(dir string) ([]string, error) {
	var files []string
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, path)
		rel = filepath.ToSlash(rel)
		switch {
		case d.Type()&fs.ModeSymlink != 0:
			return fmt.Errorf("%s is a symlink", rel)
		case d.IsDir():
			return nil
		case !d.Type().IsRegular():
			return fmt.Errorf("%s is not a regular file", rel)
		}
		if rel != "SHA256SUMS" {
			files = append(files, rel)
		}
		return nil
	})
	sort.Strings(files)
	return files, err
}

// VerifyTree checks every file of a project build against its SHA256SUMS
// and refuses files the list does not name. It returns the stats of the
// verified files for cheap later checks.
func VerifyTree(dir string) (treeStats, error) {
	sums, err := readSums(dir)
	if err != nil {
		return nil, err
	}
	files, err := listTree(dir)
	if err != nil {
		return nil, err
	}
	stats := treeStats{}
	var bad []string
	for _, rel := range files {
		want, ok := sums[rel]
		if !ok {
			return nil, fmt.Errorf("%s is not listed in SHA256SUMS", rel)
		}
		got, err := fileSHA256(filepath.Join(dir, filepath.FromSlash(rel)))
		if err != nil {
			return nil, err
		}
		if got != want {
			bad = append(bad, rel)
			continue
		}
		fi, err := os.Stat(filepath.Join(dir, filepath.FromSlash(rel)))
		if err != nil {
			return nil, err
		}
		stats[rel] = statSig{fi.Size(), fi.ModTime()}
	}
	for rel := range sums {
		if _, ok := stats[rel]; !ok && !contains(bad, rel) {
			bad = append(bad, rel+" (missing)")
		}
	}
	if len(bad) > 0 {
		sort.Strings(bad)
		more := ""
		if len(bad) > 1 {
			more = fmt.Sprintf(" and %d more", len(bad)-1)
		}
		return nil, fmt.Errorf("%s differs from SHA256SUMS%s", bad[0], more)
	}
	return stats, nil
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// treeSum is the SHA-256 of a tree's SHA256SUMS, which pins compare with.
func treeSum(dir string) (string, error) {
	return fileSHA256(filepath.Join(dir, "SHA256SUMS"))
}

type statSig struct {
	size int64
	mod  time.Time
}

type treeStats map[string]statSig

// unchanged compares the current stats of every verified file.
func (t treeStats) unchanged(dir string) bool {
	for rel, want := range t {
		fi, err := os.Stat(filepath.Join(dir, filepath.FromSlash(rel)))
		if err != nil || fi.Size() != want.size || !fi.ModTime().Equal(want.mod) {
			return false
		}
	}
	return true
}

// verified remembers, per process, the stats of trees that passed a full
// verification, so a start only stats the files; any change triggers a
// full verification again.
var verified = struct {
	sync.Mutex
	trees map[string]treeStats
}{trees: map[string]treeStats{}}

// checkTree verifies dir fully once per process and after any change.
func checkTree(dir string) error {
	verified.Lock()
	stats := verified.trees[dir]
	verified.Unlock()
	if stats != nil && stats.unchanged(dir) {
		return nil
	}
	stats, err := VerifyTree(dir)
	verified.Lock()
	defer verified.Unlock()
	if err != nil {
		delete(verified.trees, dir)
		return err
	}
	verified.trees[dir] = stats
	return nil
}

// fileSig is a cheap identity check for an official binary: its stat, and
// its hash when the stat changed.
var officialSigs = struct {
	sync.Mutex
	m map[string]statSig
}{m: map[string]statSig{}}

// officialChanged reports whether the binary at path no longer hashes to
// want. It hashes only when the stat changed since the last check.
func officialChanged(path, want string) (bool, string, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return true, "", err
	}
	sig := statSig{fi.Size(), fi.ModTime()}
	officialSigs.Lock()
	old, ok := officialSigs.m[path]
	officialSigs.Unlock()
	if ok && old == sig {
		return false, want, nil
	}
	got, err := fileSHA256(path)
	if err != nil {
		return true, "", err
	}
	if got == want {
		officialSigs.Lock()
		officialSigs.m[path] = sig
		officialSigs.Unlock()
	}
	return got != want, got, nil
}

func hexSum(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}
