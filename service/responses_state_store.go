package service

import (
	"container/list"
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/songquanpeng/one-api/common"
	"github.com/songquanpeng/one-api/model"
)

const responseStateTTL = 7 * 24 * time.Hour

type responseStateStore interface {
	Lookup(context.Context, []string) (map[string]model.ResponseSource, error)
	Write(context.Context, map[string]model.ResponseSource) error
}

type responseLocalEntry struct {
	key     string
	source  model.ResponseSource
	expires time.Time
}
type localResponseStateStore struct {
	mu       sync.Mutex
	entries  map[string]*list.Element
	lru      *list.List
	capacity int
	now      func() time.Time
}

func newLocalResponseStateStore(capacity int) *localResponseStateStore {
	return &localResponseStateStore{entries: make(map[string]*list.Element), lru: list.New(), capacity: capacity, now: time.Now}
}

var localResponseStates = newLocalResponseStateStore(100000)

func (s *localResponseStateStore) Lookup(ctx context.Context, keys []string) (map[string]model.ResponseSource, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	result := make(map[string]model.ResponseSource, len(keys))
	now := s.now()
	for _, key := range keys {
		if element := s.entries[key]; element != nil {
			entry := element.Value.(*responseLocalEntry)
			if !now.Before(entry.expires) {
				delete(s.entries, key)
				s.lru.Remove(element)
				continue
			}
			entry.expires = now.Add(responseStateTTL)
			s.lru.MoveToFront(element)
			result[key] = entry.source
		}
	}
	return result, nil
}

func (s *localResponseStateStore) Write(ctx context.Context, records map[string]model.ResponseSource) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	if len(records) > s.capacity {
		return fmt.Errorf("单次状态登记超过本地索引容量")
	}
	// 先检查整批冲突，保证失败时不写入一半记录。
	for key, source := range records {
		if element := s.entries[key]; element != nil {
			entry := element.Value.(*responseLocalEntry)
			if now.Before(entry.expires) && entry.source != source {
				return responseStateConflict()
			}
		}
	}
	for key, source := range records {
		if element := s.entries[key]; element != nil {
			s.lru.Remove(element)
		}
		s.entries[key] = s.lru.PushFront(&responseLocalEntry{key, source, now.Add(responseStateTTL)})
	}
	for len(s.entries) > s.capacity {
		last := s.lru.Back()
		delete(s.entries, last.Value.(*responseLocalEntry).key)
		s.lru.Remove(last)
	}
	return nil
}

type redisResponseStateStore struct{}

func (redisResponseStateStore) Lookup(ctx context.Context, keys []string) (map[string]model.ResponseSource, error) {
	result := make(map[string]model.ResponseSource, len(keys))
	if len(keys) == 0 {
		return result, nil
	}
	if common.RDB == nil {
		return nil, fmt.Errorf("Redis 未初始化")
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	const script = `local values = redis.call('MGET', unpack(KEYS))
for i, value in ipairs(values) do if value then redis.call('EXPIRE', KEYS[i], ARGV[1]) end end
return values`
	values, err := common.RDB.Eval(ctx, script, keys, int64(responseStateTTL/time.Second)).Slice()
	if err != nil {
		return nil, err
	}
	for i, value := range values {
		if value == nil {
			continue
		}
		raw, ok := value.(string)
		if !ok {
			return nil, fmt.Errorf("状态索引数据格式错误")
		}
		var source model.ResponseSource
		if err := json.Unmarshal([]byte(raw), &source); err != nil || source.Provider == "" {
			return nil, fmt.Errorf("状态索引数据损坏")
		}
		result[keys[i]] = source
	}
	return result, nil
}

func (redisResponseStateStore) Write(ctx context.Context, records map[string]model.ResponseSource) error {
	if len(records) == 0 {
		return nil
	}
	if common.RDB == nil {
		return fmt.Errorf("Redis 未初始化")
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	keys := make([]string, 0, len(records))
	args := []interface{}{int64(responseStateTTL / time.Second)}
	for key, source := range records {
		value, err := json.Marshal(source)
		if err != nil {
			return err
		}
		keys = append(keys, key)
		args = append(args, string(value))
	}
	const script = `for i, key in ipairs(KEYS) do
local previous = redis.call('GET', key)
if previous and previous ~= ARGV[i + 1] then return 0 end
end
for i, key in ipairs(KEYS) do redis.call('SET', key, ARGV[i + 1], 'EX', ARGV[1]) end
return 1`
	result, err := common.RDB.Eval(ctx, script, keys, args...).Int()
	if err != nil {
		return err
	}
	if result != 1 {
		return responseStateConflict()
	}
	return nil
}

func currentResponseStateStore() responseStateStore {
	if common.RedisEnabled {
		return redisResponseStateStore{}
	}
	return localResponseStates
}

func responseStateConflict() error {
	return model.NewResponsesStateError("responses_state_conflict", "请求中的历史状态来自不相容的来源", 409)
}

func responseStateUnavailable() error {
	return model.NewResponsesStateError("responses_state_store_unavailable", "状态索引暂时不可用，请稍后重试", 503)
}
