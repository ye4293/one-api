package audit

import (
	"compress/gzip"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync/atomic"
	"time"
)

var errDiskFull = errors.New("audit: disk spill buffer full")

type spillStore struct {
	dir      string
	maxBytes int64
	seq      int64
}

func (s *spillStore) usage() int64 {
	var total int64
	files, _ := s.scan()
	for _, f := range files {
		if fi, err := os.Stat(f); err == nil {
			total += fi.Size()
		}
	}
	return total
}

func (s *spillStore) write(ndjson []byte) (string, error) {
	if s.usage()+int64(len(ndjson)) > s.maxBytes {
		return "", errDiskFull
	}
	if err := os.MkdirAll(s.dir, 0o755); err != nil {
		return "", err
	}
	name := fmt.Sprintf("audit-%d-%d.ndjson.gz", time.Now().UnixNano(), atomic.AddInt64(&s.seq, 1))
	path := filepath.Join(s.dir, name)
	f, err := os.Create(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	gw := gzip.NewWriter(f)
	if _, err := gw.Write(ndjson); err != nil {
		gw.Close()
		return "", err
	}
	if err := gw.Close(); err != nil {
		return "", err
	}
	return path, nil
}

func (s *spillStore) scan() ([]string, error) {
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
