package server

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

// Failure entries retain their inspection independently of the request feed:
// successful traffic must not evict details for a still-visible failure.
type redisActivity struct {
	Request *InspectedRequest   `json:"request,omitempty"`
	Error   *UpstreamErrorEvent `json:"error,omitempty"`
}

func (r *redisStats) requestsKey() string { return r.prefix + "recent:requests" }
func (r *redisStats) errorsKey() string   { return r.prefix + "recent:errors" }

func (r *redisStats) queueActivity(ctx context.Context, pipe redis.Pipeliner, a completedAttempt) error {
	entry := redisActivity{}
	if a.request.ID != "" {
		entry.Request = &a.request
	}
	if !a.success {
		event := a.errorEvent()
		entry.Error = &event
	}
	if entry.Request == nil && entry.Error == nil {
		return nil
	}
	payload, err := json.Marshal(entry)
	if err != nil {
		return fmt.Errorf("encode shared activity: %w", err)
	}
	id := a.request.ID
	if id == "" {
		id = rand.Text()
	}
	// Equal scores sort lexically by the exact nanosecond timestamp, then ID.
	// This avoids float64 timestamp rounding and insertion-order differences
	// between replicas. The newline separates the ordering prefix from JSON.
	member := fmt.Sprintf("%020d:%s\n%s", a.at.UnixNano(), id, payload)
	keys := make([]string, 0, 2)
	if entry.Request != nil {
		keys = append(keys, r.requestsKey())
	}
	if entry.Error != nil {
		keys = append(keys, r.errorsKey())
	}
	for _, key := range keys {
		pipe.ZAdd(ctx, key, redis.Z{Score: 0, Member: member})
		pipe.ZRemRangeByRank(ctx, key, 0, -maxRecentErrors-1)
	}
	return nil
}

func decodeRedisActivity(members []string, failures bool) ([]redisActivity, error) {
	entries := make([]redisActivity, 0, len(members))
	for _, member := range members {
		_, payload, ok := strings.Cut(member, "\n")
		if !ok {
			return nil, fmt.Errorf("invalid shared activity entry")
		}
		var entry redisActivity
		if err := json.Unmarshal([]byte(payload), &entry); err != nil {
			return nil, fmt.Errorf("decode shared activity: %w", err)
		}
		if failures {
			if entry.Error == nil || entry.Error.At.IsZero() ||
				(entry.Error.RequestID != "" && (entry.Request == nil || entry.Request.ID != entry.Error.RequestID)) {
				return nil, fmt.Errorf("invalid shared failure entry")
			}
		} else if entry.Request == nil || entry.Request.ID == "" || entry.Request.At.IsZero() {
			return nil, fmt.Errorf("invalid shared request entry")
		}
		entries = append(entries, entry)
	}
	return entries, nil
}

func (r *redisStats) activity(ctx context.Context, failures bool) ([]redisActivity, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	key := r.requestsKey()
	if failures {
		key = r.errorsKey()
	}
	members, err := r.client.ZRangeArgs(ctx, redis.ZRangeArgs{
		Key: key, Start: 0, Stop: maxRecentErrors - 1, Rev: true,
	}).Result()
	if err != nil {
		return nil, err
	}
	return decodeRedisActivity(members, failures)
}

func (st *Stats) recentUpstreamErrors(ctx context.Context) ([]UpstreamErrorEvent, error) {
	if st.redisInitErr != nil {
		return nil, st.redisInitErr
	}
	if st.redis == nil {
		return st.RecentUpstreamErrors(), nil
	}
	ctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	entries, err := st.redis.activity(ctx, true)
	if err != nil {
		return nil, err
	}
	events := make([]UpstreamErrorEvent, 0, len(entries))
	for _, entry := range entries {
		events = append(events, *entry.Error)
	}
	return events, nil
}

func (st *Stats) recentRequests(ctx context.Context) ([]InspectedRequest, error) {
	if st.redisInitErr != nil {
		return nil, st.redisInitErr
	}
	if st.redis == nil {
		return st.RecentRequests(), nil
	}
	ctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	entries, err := st.redis.activity(ctx, false)
	if err != nil {
		return nil, err
	}
	requests := make([]InspectedRequest, 0, len(entries))
	for _, entry := range entries {
		requests = append(requests, *entry.Request)
	}
	return requests, nil
}

func (st *Stats) request(ctx context.Context, id string) (InspectedRequest, bool, error) {
	if st.redisInitErr != nil {
		return InspectedRequest{}, false, st.redisInitErr
	}
	if st.redis == nil {
		req, ok := st.Request(id)
		return req, ok, nil
	}
	ctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return InspectedRequest{}, false, err
	}
	// Read both retained feeds in one transaction so concurrent completions
	// cannot move a request between the snapshots during detail lookup.
	pipe := st.redis.client.TxPipeline()
	requests := pipe.ZRangeArgs(ctx, redis.ZRangeArgs{
		Key: st.redis.requestsKey(), Start: 0, Stop: maxRecentErrors - 1, Rev: true,
	})
	failures := pipe.ZRangeArgs(ctx, redis.ZRangeArgs{
		Key: st.redis.errorsKey(), Start: 0, Stop: maxRecentErrors - 1, Rev: true,
	})
	if _, err := pipe.Exec(ctx); err != nil {
		return InspectedRequest{}, false, err
	}
	requestEntries, err := decodeRedisActivity(requests.Val(), false)
	if err != nil {
		return InspectedRequest{}, false, err
	}
	failureEntries, err := decodeRedisActivity(failures.Val(), true)
	if err != nil {
		return InspectedRequest{}, false, err
	}
	for _, entries := range [][]redisActivity{requestEntries, failureEntries} {
		for _, entry := range entries {
			if entry.Request != nil && entry.Request.ID == id {
				return *entry.Request, true, nil
			}
		}
	}
	return InspectedRequest{}, false, nil
}
