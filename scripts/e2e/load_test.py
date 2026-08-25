#!/usr/bin/env python3
"""Concurrent load generator for llm-proxy (stdlib only).

Drives a mix of Anthropic Messages, Chat Completions, and Responses traffic —
streaming and not — against one proxy endpoint and reports latency/TTFT
percentiles plus status-code counts. Intended for soak runs against a mock or
live backend:

    python3 scripts/e2e/load_test.py --url http://127.0.0.1:18090 \
        --key llx_... --model stealth-mock --requests 200 --concurrency 10
"""
import argparse
import http.client
import json
import random
import sys
import threading
import time
from concurrent.futures import ThreadPoolExecutor
from urllib.parse import urlparse

parser = argparse.ArgumentParser()
parser.add_argument("--url", default="http://127.0.0.1:18090")
parser.add_argument("--key", required=True)
parser.add_argument("--model", default="stealth-mock")
parser.add_argument("--requests", type=int, default=64)
parser.add_argument("--concurrency", type=int, default=8)
parser.add_argument("--timeout", type=float, default=120)
parser.add_argument("--stream-ratio", type=float, default=0.5)
parser.add_argument(
    "--endpoints", default="messages,chat,responses",
    help="comma-separated subset of messages,chat,responses")
ARGS = parser.parse_args()

U = urlparse(ARGS.url)
HOST, PORT = U.hostname, U.port or 80
PREFIX = U.path.rstrip("/")
lock = threading.Lock()
results = []


def bodies(stream):
    """(path, headers, payload) per endpoint dialect."""
    auth = {"Authorization": f"Bearer {ARGS.key}", "Content-Type": "application/json"}
    anthropic = {"x-api-key": ARGS.key, "anthropic-version": "2023-06-01",
                 "Content-Type": "application/json"}
    msg = [{"role": "user", "content": "Reply with exactly: E2E_OK"}]
    return {
        "messages": ("/v1/messages", anthropic,
                     {"model": ARGS.model, "max_tokens": 32, "stream": stream,
                      "messages": msg}),
        "chat": ("/v1/chat/completions", auth,
                 {"model": ARGS.model, "max_tokens": 32, "stream": stream,
                  "messages": msg}),
        "responses": ("/v1/responses", auth,
                      {"model": ARGS.model, "stream": stream, "input": "Reply with exactly: E2E_OK"}),
    }


def one_request(endpoint):
    stream = random.random() < ARGS.stream_ratio
    path, headers, payload = bodies(stream)[endpoint]
    conn = http.client.HTTPConnection(HOST, PORT, timeout=ARGS.timeout)
    t0 = time.perf_counter()
    try:
        conn.request("POST", PREFIX + path, json.dumps(payload), headers)
        resp = conn.getresponse()
        ttft = None
        if stream:
            chunk = resp.read(1)
            ttft = time.perf_counter() - t0
            while chunk:
                chunk = resp.read(65536)
            latency = time.perf_counter() - t0
        else:
            resp.read()
            latency = time.perf_counter() - t0
        rec = {"endpoint": endpoint, "status": resp.status,
               "latency": latency, "ttft": ttft}
    except Exception as exc:  # noqa: BLE001 - record and continue
        rec = {"endpoint": endpoint, "status": 0, "latency": time.perf_counter() - t0,
               "ttft": None, "error": repr(exc)}
    finally:
        conn.close()
    with lock:
        results.append(rec)


def pct(sorted_values, p):
    if not sorted_values:
        return float("nan")
    idx = min(len(sorted_values) - 1, int(round(p / 100 * (len(sorted_values) - 1))))
    return sorted_values[idx]


def main():
    endpoints = [e.strip() for e in ARGS.endpoints.split(",") if e.strip()]
    wall0 = time.perf_counter()
    with ThreadPoolExecutor(max_workers=ARGS.concurrency) as pool:
        list(pool.map(one_request,
                      [endpoints[i % len(endpoints)] for i in range(ARGS.requests)]))
    wall = time.perf_counter() - wall0

    latencies = sorted(r["latency"] for r in results)
    ttfts = sorted(r["ttft"] for r in results if r["ttft"] is not None)
    by_status = {}
    for r in results:
        by_status[r["status"]] = by_status.get(r["status"], 0) + 1

    print(f"requests={len(results)} concurrency={ARGS.concurrency} wall={wall:.2f}s "
          f"rps={len(results) / wall:.1f}")
    print("statuses:", " ".join(f"{k}x{v}" for k, v in sorted(by_status.items())))
    print("e2e ms  p50=%.0f p90=%.0f p99=%.0f max=%.0f" % (
        pct(latencies, 50) * 1000, pct(latencies, 90) * 1000,
        pct(latencies, 99) * 1000, latencies[-1] * 1000))
    if ttfts:
        print("ttft ms p50=%.0f p90=%.0f p99=%.0f max=%.0f" % (
            pct(ttfts, 50) * 1000, pct(ttfts, 90) * 1000,
            pct(ttfts, 99) * 1000, ttfts[-1] * 1000))
    failures = [r for r in results if r["status"] != 200]
    if failures:
        print("non-200s:")
        for r in failures[:5]:
            print(" ", r)
    # Non-zero exit when anything but 200 showed up, so soaks gate in CI too.
    sys.exit(0 if not failures else 1)


if __name__ == "__main__":
    main()
