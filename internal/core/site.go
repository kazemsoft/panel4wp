package core

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
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
	Status     Status    `json:"status"`
	Error      string    `json:"error,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
}

type CreateRequest struct {
	Site          Site   `json:"site"`
	DBPassword    string `json:"db_password"`
	AdminPassword string `json:"admin_password"`
}

var domainLabel = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$`)
var siteID = regexp.MustCompile(`^[0-9a-f]{16}$`)

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
	return nil
}
