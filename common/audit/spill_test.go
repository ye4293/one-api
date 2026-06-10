package audit

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSpillWriteAndScan(t *testing.T) {
	dir := t.TempDir()
	s := &spillStore{dir: dir, maxBytes: 100 * 1024 * 1024}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path, err := s.write([]byte(`{"x":1}` + "\n"))
	if err != nil {
		t.Fatalf("write 失败: %v", err)
	}
	if filepath.Dir(path) != dir {
		t.Errorf("文件应写入 spill 目录")
	}
	files, _ := s.scan()
	if len(files) != 1 {
		t.Errorf("scan 应找到 1 个 spill 文件, got %d", len(files))
	}
}

func TestSpillRejectWhenFull(t *testing.T) {
	dir := t.TempDir()
	s := &spillStore{dir: dir, maxBytes: 1} // 1 字节上限
	_, err := s.write([]byte("aaaa"))
	if err == nil {
		t.Errorf("磁盘满应返回错误（触发丢弃+计数）")
	}
}
