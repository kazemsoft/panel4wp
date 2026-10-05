package store

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/kazemsoft/panel4wp/internal/core"
)

func (s *Store) readResourcePlans() ([]core.ResourcePlan, error) {
	data, err := os.ReadFile(filepath.Join(filepath.Dir(s.path), "resource-plans.json"))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var plans []core.ResourcePlan
	if err := json.Unmarshal(data, &plans); err != nil {
		return nil, err
	}
	if len(plans) > 30 {
		return nil, errors.New("too many resource plans")
	}
	seen := map[string]bool{}
	names := map[string]bool{}
	for _, plan := range plans {
		if err := plan.Validate(); err != nil {
			return nil, err
		}
		name := strings.ToLower(plan.Name)
		if seen[plan.ID] || names[name] {
			return nil, errors.New("duplicate resource plan")
		}
		seen[plan.ID] = true
		names[name] = true
	}
	return plans, nil
}
func (s *Store) ListResourcePlans() ([]core.ResourcePlan, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.readResourcePlans()
}
func (s *Store) PutResourcePlan(plan core.ResourcePlan) error {
	if err := plan.Validate(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	plans, err := s.readResourcePlans()
	if err != nil {
		return err
	}
	if len(plans) >= 30 {
		return errors.New("at most 30 resource plans are supported")
	}
	for _, existing := range plans {
		if existing.ID == plan.ID || strings.EqualFold(existing.Name, plan.Name) {
			return errors.New("resource plan already exists")
		}
	}
	return writeJSON(filepath.Join(filepath.Dir(s.path), "resource-plans.json"), append(plans, plan))
}
func (s *Store) DeleteResourcePlan(id string) error {
	if !core.ValidID(id) {
		return errors.New("invalid resource plan ID")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	plans, err := s.readResourcePlans()
	if err != nil {
		return err
	}
	filtered := make([]core.ResourcePlan, 0, len(plans))
	found := false
	for _, plan := range plans {
		if plan.ID == id {
			found = true
		} else {
			filtered = append(filtered, plan)
		}
	}
	if !found {
		return errors.New("resource plan not found")
	}
	return writeJSON(filepath.Join(filepath.Dir(s.path), "resource-plans.json"), filtered)
}
