package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/huangzheng2016/pvm/internal/fsutil"
	"github.com/huangzheng2016/pvm/internal/image"
	"github.com/huangzheng2016/pvm/internal/paths"
)

// defaultsState holds the image `pvm create` uses without --image. The
// default VM lives in config.yaml; see resolveVM.
type defaultsState struct {
	DefaultImage string `json:"default_image,omitempty"`
}

func loadDefaults(p *paths.Paths) defaultsState {
	var st defaultsState
	raw, err := os.ReadFile(filepath.Join(p.Home, "defaults.json"))
	if err == nil {
		_ = json.Unmarshal(raw, &st)
	}
	return st
}

func saveDefaults(p *paths.Paths, st defaultsState) error {
	data, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	return fsutil.AtomicWrite(filepath.Join(p.Home, "defaults.json"), data, 0o600)
}

func cmdImageDefault(p *paths.Paths, args []string) int {
	args = scanJSONFlag(args)
	st := loadDefaults(p)
	if len(args) == 0 {
		if jsonOutput {
			return failErr(writeJSONOut(map[string]any{"status": "completed", "data": map[string]string{"default_image": st.DefaultImage}}))
		}
		if st.DefaultImage == "" {
			fmt.Println("no default image set")
		} else {
			fmt.Println(st.DefaultImage)
		}
		return 0
	}
	if len(args) == 1 && (args[0] == "--clear" || args[0] == "-clear") {
		st.DefaultImage = ""
	} else if len(args) == 1 {
		if _, err := image.Resolve(args[0]); err != nil {
			return failErr(err)
		}
		st.DefaultImage = args[0]
	} else {
		return failErr(fmt.Errorf("usage: pvm image default [<distro:version>|--clear]"))
	}
	if err := saveDefaults(p, st); err != nil {
		return failErr(err)
	}
	if st.DefaultImage == "" {
		fmt.Println("cleared default image")
	} else {
		fmt.Printf("default image set to %s\n", st.DefaultImage)
	}
	return 0
}

func resolveDefaultImage(p *paths.Paths) (string, error) {
	st := loadDefaults(p)
	if st.DefaultImage != "" {
		return st.DefaultImage, nil
	}
	goldens, err := image.ListGolden(p.Images)
	if err != nil {
		return "", err
	}
	seen := map[string]bool{}
	for _, g := range goldens {
		seen[g.Distro+":"+g.ImageVersion] = true
	}
	if len(seen) == 1 {
		for k := range seen {
			return k, nil
		}
	}
	return "", fmt.Errorf("--image is required (e.g. --image ubuntu:26.04 or --image debian:13), or set `pvm image default <distro:version>`")
}
