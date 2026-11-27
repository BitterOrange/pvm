package qemu

import (
	"archive/tar"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// extractArchive unpacks a pvm-qemu .tar.gz into dst. Only regular files
// and directories are accepted, every path must stay inside dst, and a
// path may appear only once.
func extractArchive(archive, dst string) error {
	f, err := os.Open(archive)
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		name := filepath.Clean(filepath.FromSlash(hdr.Name))
		if strings.HasPrefix(hdr.Name, "/") || strings.HasPrefix(hdr.Name, "\\") || filepath.IsAbs(name) || name == ".." || strings.HasPrefix(name, ".."+string(filepath.Separator)) || filepath.VolumeName(name) != "" {
			return fmt.Errorf("unsafe archive path %q", hdr.Name)
		}
		target := filepath.Join(dst, name)
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			out, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, hdr.FileInfo().Mode().Perm()|0o200)
			if err != nil {
				return err
			}
			if _, err := io.Copy(out, tr); err != nil {
				out.Close()
				return err
			}
			if err := out.Close(); err != nil {
				return err
			}
		default:
			return fmt.Errorf("archive entry %q is not a file or directory", hdr.Name)
		}
	}
}

// manifest is the archive's pvm-qemu.json: what the build says about
// itself. Paths are relative to the tree. Capabilities that change guest
// CPU behaviour are never read from here.
type manifest struct {
	Build       string `json:"build"`
	QEMUVersion string `json:"qemu_version"`
	Host        string `json:"host"`
	System      string `json:"system"`
	SystemExec  string `json:"system_exec"`
	Img         string `json:"img"`
	FirmwareDir string `json:"firmware_dir"`
}

func readManifest(tree string) (*manifest, error) {
	data, err := os.ReadFile(filepath.Join(tree, "pvm-qemu.json"))
	if err != nil {
		return nil, err
	}
	var m manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("pvm-qemu.json: %w", err)
	}
	for _, rel := range []string{m.System, m.SystemExec, m.Img, m.FirmwareDir} {
		clean := filepath.Clean(filepath.FromSlash(rel))
		if rel == "" || strings.HasPrefix(rel, "/") || strings.HasPrefix(rel, "\\") || filepath.IsAbs(clean) || strings.HasPrefix(clean, "..") {
			return nil, fmt.Errorf("pvm-qemu.json: bad path %q", rel)
		}
	}
	return &m, nil
}

// projectBuild describes the project build whose tree is at dir.
func projectBuild(dir string, m *manifest, archiveSHA, systemSHA string) Build {
	join := func(rel string) string { return filepath.Join(dir, filepath.FromSlash(rel)) }
	return Build{
		BuildIdentity: BuildIdentity{ID: m.Build, Source: SourceProject, Version: m.QEMUVersion, SystemSHA256: systemSHA},
		Dir:           dir,
		System:        join(m.System), SystemExec: join(m.SystemExec), Img: join(m.Img), FirmwareDir: join(m.FirmwareDir),
		ArchiveSHA256: archiveSHA,
	}
}
