package store

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"sync"

	"github.com/kazemsoft/panel4wp/internal/core"
)

type Store struct {
	mu   sync.Mutex
	path string
}

func New(path string) *Store { return &Store{path: path} }

func (s *Store) read() (map[string]core.Site, error) {
	b, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return make(map[string]core.Site), nil
	}
	if err != nil {
		return nil, err
	}
	var sites map[string]core.Site
	if err := json.Unmarshal(b, &sites); err != nil {
		return nil, err
	}
	if sites == nil {
		sites = make(map[string]core.Site)
	}
	return sites, nil
}

func (s *Store) write(sites map[string]core.Site) error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(sites, "", "  ")
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(s.path), ".sites-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if err := f.Chmod(0600); err != nil {
		f.Close()
		return err
	}
	if _, err := f.Write(b); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Rename(f.Name(), s.path); err != nil {
		return err
	}
	dir, err := os.Open(filepath.Dir(s.path))
	if err == nil {
		defer dir.Close()
		_ = dir.Sync()
	}
	return nil
}

func (s *Store) List() ([]core.Site, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sites, err := s.read()
	if err != nil {
		return nil, err
	}
	out := make([]core.Site, 0, len(sites))
	for _, site := range sites {
		out = append(out, site)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out, nil
}

func (s *Store) Get(id string) (core.Site, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sites, err := s.read()
	if err != nil {
		return core.Site{}, false, err
	}
	site, ok := sites[id]
	return site, ok, nil
}

func (s *Store) Put(site core.Site) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	sites, err := s.read()
	if err != nil {
		return err
	}
	sites[site.ID] = site
	return s.write(sites)
}

func (s *Store) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	sites, err := s.read()
	if err != nil {
		return err
	}
	delete(sites, id)
	return s.write(sites)
}
