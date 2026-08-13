package journal

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type Entry struct {
	Time    time.Time `json:"time"`
	Kind    string    `json:"kind"`
	Payload any       `json:"payload"`
}

type Journal struct {
	mu   sync.Mutex
	file *os.File
}

func New(dataDir string) (*Journal, error) {
	if err := os.MkdirAll(dataDir, 0o750); err != nil {
		return nil, err
	}
	path := filepath.Join(dataDir, "events.jsonl")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, err
	}
	return &Journal{file: f}, nil
}

func (j *Journal) Append(kind string, payload any) error {
	if j == nil || j.file == nil {
		return nil
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	data, err := json.Marshal(Entry{Time: time.Now().UTC(), Kind: kind, Payload: payload})
	if err != nil {
		return fmt.Errorf("encode journal entry: %w", err)
	}
	if _, err := j.file.Write(append(data, '\n')); err != nil {
		return err
	}
	return j.file.Sync()
}

func (j *Journal) Close() error {
	if j == nil || j.file == nil {
		return nil
	}
	return j.file.Close()
}
