package zcode

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
)

const valkeyCaptchaKeySuffix = "zcode:captcha"

var deleteCaptchaIfMatch = redis.NewScript(`
local current = redis.call('GET', KEYS[1])
if current == ARGV[1] then
  return redis.call('DEL', KEYS[1])
end
return 0
`)

var takeCaptcha = redis.NewScript(`
local current = redis.call('GET', KEYS[1])
if not current then
  return ''
end
redis.call('DEL', KEYS[1])
return current
`)

// ValkeyCaptchaStore stores one disposable ZCode proof in Redis-compatible
// Valkey. The server-side TTL is an additional bound beyond the timestamp in
// the value, so an abandoned proof is removed automatically.
type ValkeyCaptchaStore struct {
	client *redis.Client
	key    string
	once   sync.Once
}

// NewValkeyCaptchaStore creates a shared CAPTCHA store using the same Redis
// URL and namespace as the proxy's shared statistics. The client speaks the
// Redis protocol, so Valkey and Redis are both supported.
func NewValkeyCaptchaStore(rawURL, prefix string) (*ValkeyCaptchaStore, error) {
	options, err := redis.ParseURL(rawURL)
	if err != nil {
		return nil, fmt.Errorf("parse stats.redis_url for ZCode CAPTCHA: %w", err)
	}
	if prefix == "" {
		prefix = "llm-proxy:stats:"
	}
	prefix = strings.TrimRight(prefix, ":")
	return &ValkeyCaptchaStore{
		client: redis.NewClient(options),
		key:    prefix + ":" + valkeyCaptchaKeySuffix,
	}, nil
}

func (s *ValkeyCaptchaStore) Set(ctx context.Context, param string, issuedAt time.Time) error {
	b, err := json.Marshal(captchaRecord{VerifyParam: param, IssuedAt: issuedAt})
	if err != nil {
		return err
	}
	return s.client.Set(ctx, s.key, b, captchaTTL).Err()
}

func (s *ValkeyCaptchaStore) Get(ctx context.Context) (string, time.Time, error) {
	b, err := s.client.Get(ctx, s.key).Bytes()
	if errors.Is(err, redis.Nil) {
		return "", time.Time{}, os.ErrNotExist
	}
	if err != nil {
		return "", time.Time{}, err
	}
	var record captchaRecord
	if err := json.Unmarshal(b, &record); err != nil {
		return "", time.Time{}, fmt.Errorf("decode ZCode CAPTCHA verification parameter: %w", err)
	}
	return record.VerifyParam, record.IssuedAt, nil
}

// Take atomically returns and deletes the current proof. A proof must not be
// handed to two concurrent model requests, even when they run on different
// proxy replicas.
func (s *ValkeyCaptchaStore) Take(ctx context.Context) (string, time.Time, error) {
	value, err := takeCaptcha.Run(ctx, s.client, []string{s.key}).Result()
	if err != nil {
		return "", time.Time{}, err
	}
	var b []byte
	switch value := value.(type) {
	case string:
		b = []byte(value)
	case []byte:
		b = value
	default:
		return "", time.Time{}, fmt.Errorf("decode ZCode CAPTCHA verification parameter: unexpected Redis value %T", value)
	}
	if len(b) == 0 {
		return "", time.Time{}, os.ErrNotExist
	}
	var record captchaRecord
	if err := json.Unmarshal(b, &record); err != nil {
		return "", time.Time{}, fmt.Errorf("decode ZCode CAPTCHA verification parameter: %w", err)
	}
	return record.VerifyParam, record.IssuedAt, nil
}

func (s *ValkeyCaptchaStore) DeleteIfMatch(ctx context.Context, param string, issuedAt time.Time) error {
	b, err := json.Marshal(captchaRecord{VerifyParam: param, IssuedAt: issuedAt})
	if err != nil {
		return err
	}
	return deleteCaptchaIfMatch.Run(ctx, s.client, []string{s.key}, b).Err()
}

// Close releases the Valkey connection pool.
func (s *ValkeyCaptchaStore) Close() error {
	var err error
	s.once.Do(func() { err = s.client.Close() })
	return err
}

var _ CaptchaStore = (*ValkeyCaptchaStore)(nil)
