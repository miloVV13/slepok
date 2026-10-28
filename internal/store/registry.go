package store

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// Project is a registry entry: a project directory slepok has worked with.
// The packages a project needs are read from its slepok.lock when needed,
// never cached here, so the lock stays the only source of truth.
type Project struct {
	Path     string    `json:"path"`
	LastUsed time.Time `json:"lastUsed"`
}

func (s *Store) registryPath() string { return filepath.Join(s.root, "projects.json") }

// Projects returns the registered projects, most recently used first.
func (s *Store) Projects() ([]Project, error) {
	data, err := os.ReadFile(s.registryPath())
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var projects []Project
	if err := json.Unmarshal(data, &projects); err != nil {
		return nil, err
	}
	slices.SortFunc(projects, func(a, b Project) int { return b.LastUsed.Compare(a.LastUsed) })
	return projects, nil
}

// Register records that the project at dir was used now.
func (s *Store) Register(dir string) error {
	dir, err := filepath.Abs(dir)
	if err != nil {
		return err
	}
	projects, err := s.Projects()
	if err != nil {
		return err
	}
	projects = slices.DeleteFunc(projects, func(p Project) bool { return p.Path == dir })
	projects = append(projects, Project{Path: dir, LastUsed: time.Now().UTC().Truncate(time.Second)})
	return s.writeRegistry(projects)
}

// Forget removes projects from the registry.
func (s *Store) Forget(dirs ...string) error {
	projects, err := s.Projects()
	if err != nil {
		return err
	}
	projects = slices.DeleteFunc(projects, func(p Project) bool { return slices.Contains(dirs, p.Path) })
	return s.writeRegistry(projects)
}

func (s *Store) writeRegistry(projects []Project) error {
	slices.SortFunc(projects, func(a, b Project) int { return strings.Compare(a.Path, b.Path) })
	data, err := json.MarshalIndent(projects, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(s.root, ".projects.json.tmp-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), s.registryPath())
}
