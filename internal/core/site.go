package core

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

type Status string

const (
	StatusCreating Status = "creating"
	StatusRunning  Status = "running"
	StatusStopped  Status = "stopped"
	StatusFailed   Status = "failed"
	StatusDeleting Status = "deleting"
)

type Site struct {
	ID         string    `json:"id"`
	Domain     string    `json:"domain"`
	Title      string    `json:"title"`
	AdminEmail string    `json:"admin_email"`
	MemoryMB   int       `json:"memory_mb,omitempty"`
	CPUs       float64   `json:"cpus,omitempty"`
	Status     Status    `json:"status"`
	Error      string    `json:"error,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
	Backups    []Backup  `json:"backups,omitempty"`
}

type Backup struct {
	ID        string    `json:"id"`
	CreatedAt time.Time `json:"created_at"`
}

type CreateRequest struct {
	Site          Site   `json:"site"`
	DBPassword    string `json:"db_password"`
	AdminPassword string `json:"admin_password"`
}

type BackupRequest struct {
	Site     Site   `json:"site"`
	BackupID string `json:"backup_id"`
}

type RestoreRequest struct {
	Site           Site   `json:"site"`
	BackupID       string `json:"backup_id"`
	SafetyBackupID string `json:"safety_backup_id"`
}

type UpdateRequest struct {
	Site Site `json:"site"`
}

type FileRequest struct {
	SiteID  string `json:"site_id"`
	Path    string `json:"path"`
	Content []byte `json:"content,omitempty"`
}

type FileEntry struct {
	Name     string `json:"name"`
	Type     string `json:"type"`
	Size     int64  `json:"size"`
	Modified int64  `json:"modified"`
}

type FileContent struct {
	Name    string `json:"name"`
	Content []byte `json:"content"`
}

type StatsRequest struct {
	SiteIDs []string `json:"site_ids"`
}

type ContainerStats struct {
	CPU      string `json:"cpu"`
	Memory   string `json:"memory"`
	MemoryPC string `json:"memory_percent"`
	NetIO    string `json:"network_io"`
	BlockIO  string `json:"block_io"`
	PIDs     string `json:"pids"`
}

type SiteStats struct {
	WordPress *ContainerStats `json:"wordpress,omitempty"`
	Database  *ContainerStats `json:"database,omitempty"`
}

var domainLabel = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$`)
var siteID = regexp.MustCompile(`^[0-9a-f]{16}$`)
var backupID = regexp.MustCompile(`^[0-9]{8}T[0-9]{6}Z-[0-9a-f]{8}$`)

func NewID() (string, error) {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}

func RandomPassword() (string, error) {
	var b [24]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}

func ValidID(id string) bool { return siteID.MatchString(id) }

func ValidBackupID(id string) bool { return backupID.MatchString(id) }

func ValidRelativePath(path string, allowEmpty bool) bool {
	if path == "" {
		return allowEmpty
	}
	if len(path) > 512 || strings.HasPrefix(path, "/") || strings.ContainsAny(path, "\x00\n\r\\") {
		return false
	}
	clean := filepath.Clean(path)
	if clean == "." || clean != path {
		return false
	}
	for _, part := range strings.Split(path, "/") {
		if part == "" || part == "." || part == ".." {
			return false
		}
	}
	return true
}

func NewBackupID(now time.Time) (string, error) {
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return now.UTC().Format("20060102T150405Z") + "-" + hex.EncodeToString(b[:]), nil
}

func NormalizeDomain(raw string) (string, error) {
	domain := strings.ToLower(strings.TrimSpace(raw))
	if domain == "" || len(domain) > 253 || strings.ContainsAny(domain, "/:@\\\n\r\t ") {
		return "", errors.New("invalid domain")
	}
	labels := strings.Split(domain, ".")
	if len(labels) < 2 {
		return "", errors.New("domain must include a suffix")
	}
	for _, label := range labels {
		if !domainLabel.MatchString(label) {
			return "", errors.New("invalid domain label")
		}
	}
	if labels[len(labels)-1] == "localhost" && len(labels) != 2 {
		return "", errors.New("local domain must be one level below localhost")
	}
	return domain, nil
}

func ValidateSite(s Site) error {
	if !ValidID(s.ID) {
		return errors.New("invalid site ID")
	}
	if _, err := NormalizeDomain(s.Domain); err != nil {
		return err
	}
	if strings.TrimSpace(s.Title) == "" || len([]rune(s.Title)) > 120 {
		return errors.New("title must be 1 to 120 characters")
	}
	if strings.ContainsAny(s.Title, "\n\r") {
		return errors.New("title must be one line")
	}
	if strings.TrimSpace(s.AdminEmail) == "" || len(s.AdminEmail) > 254 || strings.ContainsAny(s.AdminEmail, " \n\r\t") || !strings.Contains(s.AdminEmail, "@") {
		return fmt.Errorf("invalid admin email")
	}
	if s.MemoryMB != 0 && (s.MemoryMB < 256 || s.MemoryMB > 8192) {
		return errors.New("memory must be between 256 and 8192 MB per container")
	}
	if s.CPUs != 0 && (s.CPUs < 0.25 || s.CPUs > 8) {
		return errors.New("CPU limit must be between 0.25 and 8 per container")
	}
	return nil
}

func ResourceLimits(s Site) (int, float64) {
	memory, cpus := s.MemoryMB, s.CPUs
	if memory == 0 {
		memory = 512
	}
	if cpus == 0 {
		cpus = 1
	}
	return memory, cpus
}
