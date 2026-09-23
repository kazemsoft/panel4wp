package audit

import (
	"bufio"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const maxLogBytes = 10 << 20

type Entry struct {
	Time    time.Time `json:"time"`
	Action  string    `json:"action"`
	SiteID  string    `json:"site_id,omitempty"`
	Domain  string    `json:"domain,omitempty"`
	Success bool      `json:"success"`
	Detail  string    `json:"detail,omitempty"`
}

type Log struct {
	path string
	mu   sync.Mutex
}

func New(path string) *Log { return &Log{path: path} }

func clean(value string, limit int) string {
	value = strings.TrimSpace(strings.ReplaceAll(strings.ReplaceAll(value, "\n", " "), "\r", " "))
	if len(value) > limit {
		value = value[:limit]
	}
	return value
}

func (l *Log) Append(entry Entry) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	entry.Time = entry.Time.UTC()
	if entry.Time.IsZero() {
		entry.Time = time.Now().UTC()
	}
	entry.Action = clean(entry.Action, 80)
	entry.SiteID = clean(entry.SiteID, 64)
	entry.Domain = clean(entry.Domain, 253)
	entry.Detail = clean(entry.Detail, 500)
	if entry.Action == "" {
		return errors.New("audit action is required")
	}
	if err := os.MkdirAll(filepath.Dir(l.path), 0700); err != nil {
		return err
	}
	if info, err := os.Stat(l.path); err == nil && info.Size() >= maxLogBytes {
		_ = os.Remove(l.path + ".1")
		if err := os.Rename(l.path, l.path+".1"); err != nil {
			return err
		}
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	data, err := json.Marshal(entry)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(l.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Write(append(data, '\n'))
	return err
}

func (l *Log) List(limit int) ([]Entry, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if limit < 1 || limit > 500 {
		return nil, errors.New("audit limit must be between 1 and 500")
	}
	f, err := os.Open(l.path)
	if errors.Is(err, os.ErrNotExist) {
		return []Entry{}, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	entries := make([]Entry, 0, limit)
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 1024), 1<<20)
	for scanner.Scan() {
		var entry Entry
		if err := json.Unmarshal(scanner.Bytes(), &entry); err != nil {
			return nil, err
		}
		if len(entries) == limit {
			copy(entries, entries[1:])
			entries[len(entries)-1] = entry
		} else {
			entries = append(entries, entry)
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	for left, right := 0, len(entries)-1; left < right; left, right = left+1, right-1 {
		entries[left], entries[right] = entries[right], entries[left]
	}
	return entries, nil
}
