package server

import (
	"strconv"
	"strings"
	"time"
)

// Redis hash decoding is independent of transport and uses the same snapshots
// as memory/JSON storage. Keep field names compatible with existing stores.
func redisBuckets(fields map[string]string) map[int64]*bucket {
	buckets := make(map[int64]*bucket)
	for field, value := range fields {
		parts := strings.Split(field, ":")
		if len(parts) < 3 || parts[0] != "bucket" {
			continue
		}
		win, err := strconv.ParseInt(parts[1], 10, 64)
		if err != nil {
			continue
		}
		b, ok := buckets[win]
		if !ok {
			b = &bucket{
				WindowStart:       time.Unix(win*300, 0).UTC(),
				TTFTBuckets:       make([]uint64, len(ttftEdges)),
				E2EBuckets:        make([]uint64, len(e2eEdges)),
				ThroughputBuckets: make([]uint64, len(tpsEdges)),
			}
			buckets[win] = b
		}
		n := redisUint(value)
		switch parts[2] {
		case "requests":
			b.Requests = n
		case "successes":
			b.Successes = n
		case "tokens_in":
			b.TokensIn = n
		case "tokens_out":
			b.TokensOut = n
		case "cache_read":
			b.CacheRead = n
		case "cache_write":
			b.CacheWrite = n
		case "tool_calls":
			b.ToolCalls = n
		case "tool_errors":
			b.ToolErrors = n
		case "status":
			if len(parts) != 4 || n == 0 {
				continue
			}
			if b.StatusCodes == nil {
				b.StatusCodes = map[string]uint64{}
			}
			b.StatusCodes[parts[3]] += n
		case "ttft", "e2e", "tps":
			if len(parts) != 4 {
				continue
			}
			idx, err := strconv.Atoi(parts[3])
			if err != nil {
				continue
			}
			switch parts[2] {
			case "ttft":
				if idx >= 0 && idx < len(b.TTFTBuckets) {
					b.TTFTBuckets[idx] = n
				}
			case "e2e":
				if idx >= 0 && idx < len(b.E2EBuckets) {
					b.E2EBuckets[idx] = n
				}
			case "tps":
				if idx >= 0 && idx < len(b.ThroughputBuckets) {
					b.ThroughputBuckets[idx] = n
				}
			}
		}
	}
	return buckets
}

func redisUint(value string) uint64 {
	n, _ := strconv.ParseUint(value, 10, 64)
	return n
}

func decodeRedisModel(fields map[string]string) modelSnapshot {
	buckets := redisBuckets(fields)
	snapshot := modelSnapshot{
		Requests: redisUint(fields["requests"]), Successes: redisUint(fields["successes"]),
		TokensIn: redisUint(fields["tokens_in"]), TokensOut: redisUint(fields["tokens_out"]),
		CacheRead: redisUint(fields["cache_read"]), CacheWrite: redisUint(fields["cache_write"]),
		ToolCalls: redisUint(fields["tool_calls"]), ToolErrors: redisUint(fields["tool_errors"]),
		Statuses: map[string]uint64{}, Buckets: make([]bucket, 0, len(buckets)),
	}
	for field, value := range fields {
		if status, ok := strings.CutPrefix(field, "status:"); ok {
			if n := redisUint(value); n > 0 {
				snapshot.Statuses[status] = n
			}
		}
	}
	for _, b := range buckets {
		snapshot.Buckets = append(snapshot.Buckets, *b)
	}
	return snapshot
}
