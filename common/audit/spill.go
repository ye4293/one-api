package audit

import (
	"compress/gzip"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

var errDiskFull = errors.New("audit: disk spill buffer full")

type spillStore struct {
	dir      string
	maxBytes int64
	seq      int64
	mu       sync.Mutex
}

func (s *spillStore) usage() int64 {
	var total int64
	entries, _ := os.ReadDir(s.dir)
	for _, e := range entries {
		if !e.IsDir() && filepath.Ext(e.Name()) == ".gz" {
			if fi, err := e.Info(); err == nil {
				total += fi.Size()
			}
		}
	}
	return total
}

func (s *spillStore) write(ndjson []byte) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.usage()+int64(len(ndjson)) > s.maxBytes {
		return "", errDiskFull
	}
	if err := os.MkdirAll(s.dir, 0o755); err != nil {
		return "", err
	}
	name := fmt.Sprintf("audit-%d-%d.ndjson.gz", time.Now().UnixNano(), atomic.AddInt64(&s.seq, 1))
	tmpPath := filepath.Join(s.dir, name+".tmp")
	finalPath := filepath.Join(s.dir, name)

	f, err := os.Create(tmpPath)
	if err != nil {
		return "", err
	}
	gw := gzip.NewWriter(f)
	if _, err := gw.Write(ndjson); err != nil {
		gw.Close()
		f.Close()
		os.Remove(tmpPath)
		return "", err
	}
	if err := gw.Close(); err != nil {
		f.Close()
		os.Remove(tmpPath)
		return "", err
	}
	if err := f.Close(); err != nil {
		os.Remove(tmpPath)
		return "", err
	}
	if err := os.Rename(tmpPath, finalPath); err != nil {
		os.Remove(tmpPath)
		return "", err
	}
	return finalPath, nil
}

func (s *spillStore) scan() ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	entries, err := os.ReadDir(s.dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var files []string
	for _, e := range entries {
		if !e.IsDir() && filepath.Ext(e.Name()) == ".gz" {
			files = append(files, filepath.Join(s.dir, e.Name()))
		}
	}
	sort.Strings(files)
	return files, nil
}
