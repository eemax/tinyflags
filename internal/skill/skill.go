package skill

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/eemax/tinyflags/internal/core"
	cerr "github.com/eemax/tinyflags/internal/errors"
)

type Info struct {
	Name   string `json:"name"`
	Source string `json:"source"`
	Path   string `json:"path,omitempty"`
}

func Load(name, projectDir string, cfg core.Config) (string, Info, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", Info{}, cerr.New(cerr.ExitRuntime, "skill name is required")
	}

	if projectDir != "" {
		localPath := filepath.Join(projectDir, ".tinyflags", "skills", name+".md")
		if content, ok, err := readFileIfExists(localPath); err != nil {
			return "", Info{}, cerr.Wrap(cerr.ExitRuntime, "read project skill", err)
		} else if ok {
			return content, Info{Name: name, Source: "project-local", Path: localPath}, nil
		}
	}

	if cfg.SkillsDir != "" {
		globalPath := filepath.Join(cfg.SkillsDir, name+".md")
		if content, ok, err := readFileIfExists(globalPath); err != nil {
			return "", Info{}, cerr.Wrap(cerr.ExitRuntime, "read global skill", err)
		} else if ok {
			return content, Info{Name: name, Source: "global", Path: globalPath}, nil
		}
	}

	if inline, ok := cfg.Skills[name]; ok {
		return inline, Info{Name: name, Source: "config-inline"}, nil
	}

	return "", Info{}, cerr.New(cerr.ExitRuntime, fmt.Sprintf("skill %q not found", name))
}

func List(projectDir string, cfg core.Config) ([]Info, error) {
	seen := map[string]Info{}
	if projectDir != "" {
		localDir := filepath.Join(projectDir, ".tinyflags", "skills")
		if infos, err := scanDir(localDir, "project-local"); err != nil {
			return nil, err
		} else {
			for _, info := range infos {
				seen[info.Name] = info
			}
		}
	}
	if cfg.SkillsDir != "" {
		if infos, err := scanDir(cfg.SkillsDir, "global"); err != nil {
			return nil, err
		} else {
			for _, info := range infos {
				if _, exists := seen[info.Name]; !exists {
					seen[info.Name] = info
				}
			}
		}
	}
	for name := range cfg.Skills {
		if _, exists := seen[name]; !exists {
			seen[name] = Info{Name: name, Source: "config-inline"}
		}
	}
	items := make([]Info, 0, len(seen))
	for _, info := range seen {
		items = append(items, info)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Name < items[j].Name })
	return items, nil
}

func scanDir(dir, source string) ([]Info, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, cerr.Wrap(cerr.ExitRuntime, "read skills directory", err)
	}
	out := make([]Info, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if filepath.Ext(name) != ".md" {
			continue
		}
		out = append(out, Info{
			Name:   strings.TrimSuffix(name, ".md"),
			Source: source,
			Path:   filepath.Join(dir, name),
		})
	}
	return out, nil
}

func readFileIfExists(path string) (string, bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", false, nil
		}
		return "", false, err
	}
	return string(data), true, nil
}
