package engine

import (
	"bufio"
	"encoding/json"
	"errors"
	"io"
	"os"
	"sync"

	"github.com/lechandonga/liquidity-risk-engine-go/pkg/lre/domain"
)

// Journal 是批次事件的本地持久化抽象。实现必须保证单条记录原子追加。
type Journal interface {
	Append(domain.Batch) error
	Replay(handle func(domain.Batch) error) error
}

// FileJournal 把每个批次序列化为一行 JSON 追加到本地文件（NDJSON）。
// 仅使用标准库与本地文件系统，无任何外部服务或凭据。
type FileJournal struct {
	mu sync.Mutex
	f  *os.File
	w  *bufio.Writer
}

func OpenFileJournal(path string) (*FileJournal, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR|os.O_APPEND, 0o600)
	if err != nil {
		return nil, err
	}
	j := &FileJournal{f: f, w: bufio.NewWriter(f)}
	return j, nil
}

func (j *FileJournal) Append(b domain.Batch) error {
	j.mu.Lock()
	defer j.mu.Unlock()
	data, err := json.Marshal(b)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if _, err := j.w.Write(data); err != nil {
		return err
	}
	if err := j.w.Flush(); err != nil {
		return err
	}
	return j.f.Sync()
}

func (j *FileJournal) Replay(handle func(domain.Batch) error) error {
	j.mu.Lock()
	if err := j.w.Flush(); err != nil {
		j.mu.Unlock()
		return err
	}
	j.mu.Unlock()
	if _, err := j.f.Seek(0, io.SeekStart); err != nil {
		return err
	}
	defer j.f.Seek(0, io.SeekEnd)
	sc := bufio.NewScanner(j.f)
	sc.Buffer(make([]byte, 1024*1024), 16*1024*1024)
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var b domain.Batch
		if err := json.Unmarshal(line, &b); err != nil {
			return err
		}
		if err := handle(b); err != nil {
			if errors.Is(err, errReplayStop) {
				return nil
			}
			return err
		}
	}
	return sc.Err()
}

func (j *FileJournal) Close() error {
	j.mu.Lock()
	defer j.mu.Unlock()
	if err := j.w.Flush(); err != nil {
		return err
	}
	return j.f.Close()
}

var errReplayStop = errors.New("replay stopped")

// ReplayJournal 按日志顺序把全部批次重放到目标引擎。
// ApplyBatch 的批次幂等保证回放安全：重复 BatchID 不会重复计数。
func ReplayJournal(j Journal, target *Engine) error {
	return j.Replay(func(b domain.Batch) error {
		_, err := target.ApplyBatch(b)
		return err
	})
}
