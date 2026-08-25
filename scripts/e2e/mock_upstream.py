#!/usr/bin/env python3
"""Mock OpenAI-compatible upstream for llm-proxy live E2E and chaos tests.

Serves /v1/models, /v1/chat/completions, and /v1/responses with canned
responses (both non-streaming and SSE). Run:

    python3 scripts/e2e/mock_upstream.py [port]

Then point an llm-proxy venice backend at http://127.0.0.1:<port>/v1 via
base_url and exercise Claude Code / Codex through the proxy without any
real provider credentials.

Chaos flags make the mock misbehave the way real providers do, to exercise
the proxy's retry and surfacing paths end-to-end (see scripts/e2e/chaos_e2e.sh):

    --fail-first N STATUS   fail the first N inference attempts with STATUS
    --retry-after SECONDS   attach Retry-After to those failures
    --cut-stream-after N    close the connection mid-SSE after N events
    --slow-ttft MS          sleep MS before answering or first SSE event
    --error-200             answer non-streaming chat with HTTP 200 + error body

Counters are global: --fail-first 3 fails each of the first three POSTs no
matter which endpoint they hit.
"""
import argparse
import json
import sys
import time
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

parser = argparse.ArgumentParser()
parser.add_argument("port", nargs="?", type=int, default=9911)
parser.add_argument("--fail-first", type=int, default=0, metavar="N")
parser.add_argument("--fail-status", type=int, default=503, metavar="CODE")
parser.add_argument("--retry-after", type=float, default=0, metavar="SECONDS")
parser.add_argument("--cut-stream-after", type=int, default=0, metavar="N")
parser.add_argument("--slow-ttft", type=int, default=0, metavar="MS")
parser.add_argument("--error-200", action="store_true")
ARGS = parser.parse_args()
PORT = ARGS.port

# Global attempt counter for --fail-first; guarded by ThreadingHTTPServer's
# GIL, which is close enough for a test fixture.
_attempts = 0


def should_fail_connect():
    """True for the first ARGS.fail_first inference attempts."""
    global _attempts
    _attempts += 1
    return _attempts <= ARGS.fail_first


class Handler(BaseHTTPRequestHandler):
    def log_message(self, fmt, *args):  # quieter logs
        sys.stderr.write("mock: %s %s\n" % (self.command, self.path))

    def _body(self):
        n = int(self.headers.get("Content-Length") or 0)
        raw = self.rfile.read(n) if n else b"{}"
        try:
            return json.loads(raw)
        except Exception:
            return {}

    def do_GET(self):
        if self.path.startswith("/v1/models"):
            body = json.dumps({
                "object": "list",
                "data": [{"id": "stealth-mock", "object": "model", "owned_by": "mock"}],
            }).encode()
            self.send_response(200)
            self.send_header("Content-Type", "application/json")
            self.send_header("Content-Length", str(len(body)))
            self.end_headers()
            self.wfile.write(body)
        else:
            self.send_error(404)

    def _fail_connect(self):
        """Answer with the configured failure status instead of inferring."""
        self.send_response(ARGS.fail_status)
        if ARGS.retry_after > 0:
            self.send_header("Retry-After", str(int(ARGS.retry_after)))
        body = json.dumps({"error": {"message": "mock chaos failure",
                                     "type": "server_error"}}).encode()
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def _maybe_slow(self):
        if ARGS.slow_ttft > 0:
            time.sleep(ARGS.slow_ttft / 1000.0)

    def do_POST(self):
        req = self._body()
        stream = bool(req.get("stream"))
        self._maybe_slow()

        if should_fail_connect():
            self._fail_connect()
            return

        if self.path.startswith("/v1/chat/completions"):
            model = req.get("model", "stealth-mock")
            if not stream:
                # Requests that declare tools get a tool_calls reply so the
                # proxy's tool-call stats have something to count.
                wants_tool = bool(req.get("tools"))
                if wants_tool:
                    message = {
                        "role": "assistant",
                        "content": None,
                        "tool_calls": [{
                            "id": "call_mock_1", "type": "function",
                            "function": {"name": (req.get("tools") or [{}])[0].get("function", {}).get("name", "get_weather"),
                                         "arguments": "{\"city\":\"Rome\"}"},
                        }],
                    }
                    finish = "tool_calls"
                else:
                    message = {"role": "assistant", "content": "E2E_OK"}
                    finish = "stop"
                if ARGS.error_200:
                    body = json.dumps({
                        "error": {"message": "quota exhausted behind a 200",
                                  "type": "insufficient_credits"},
                    }).encode()
                else:
                    body = json.dumps({
                        "id": "chatcmpl-mock", "object": "chat.completion",
                        "created": 1700000000, "model": model,
                        "choices": [{"index": 0, "message": message,
                                     "finish_reason": finish}],
                        "usage": {
                            "prompt_tokens": 120, "completion_tokens": 42, "total_tokens": 162,
                            "prompt_tokens_details": {"cached_tokens": 96},
                        },
                    }).encode()
                self.send_response(200)
                self.send_header("Content-Type", "application/json")
                self.send_header("Content-Length", str(len(body)))
                self.end_headers()
                self.wfile.write(body)
                return
            chunks = [
                {"id": "chatcmpl-mock", "object": "chat.completion.chunk",
                 "created": 1700000000, "model": model,
                 "choices": [{"index": 0, "delta": {"role": "assistant"}, "finish_reason": None}]},
                {"id": "chatcmpl-mock", "object": "chat.completion.chunk",
                 "created": 1700000000, "model": model,
                 "choices": [{"index": 0, "delta": {"content": "E2E_OK"}, "finish_reason": None}]},
                {"id": "chatcmpl-mock", "object": "chat.completion.chunk",
                 "created": 1700000000, "model": model,
                 "choices": [{"index": 0, "delta": {}, "finish_reason": "stop"}],
                 "usage": {"prompt_tokens": 5, "completion_tokens": 2, "total_tokens": 7}},
            ]
            self.send_response(200)
            self.send_header("Content-Type", "text/event-stream")
            self.send_header("Cache-Control", "no-cache")
            self.end_headers()
            sent = 0
            for c in chunks:
                if ARGS.cut_stream_after and sent >= ARGS.cut_stream_after:
                    # Sever without any terminator: the proxy must see a
                    # broken stream, not a completed turn.
                    self.close_connection = True
                    return
                self.wfile.write(f"data: {json.dumps(c)}\n\n".encode())
                self.wfile.flush()
                sent += 1
            if ARGS.cut_stream_after and sent >= ARGS.cut_stream_after:
                self.close_connection = True
                return
            self.wfile.write(b"data: [DONE]\n\n")
            self.wfile.flush()
            return

        if self.path.startswith("/v1/responses"):
            model = req.get("model", "stealth-mock")
            if not stream:
                if ARGS.error_200:
                    body = json.dumps({
                        "error": {"message": "quota exhausted behind a 200",
                                  "type": "insufficient_credits"},
                    }).encode()
                else:
                    body = json.dumps({
                        "id": "resp-mock", "object": "response", "model": model,
                        "status": "completed",
                        "output": [
                            {"type": "message", "role": "assistant",
                             "content": [{"type": "output_text", "text": "E2E_OK"}]},
                        ],
                        "usage": {"input_tokens": 5, "output_tokens": 2, "total_tokens": 7},
                    }).encode()
                self.send_response(200)
                self.send_header("Content-Type", "application/json")
                self.send_header("Content-Length", str(len(body)))
                self.end_headers()
                self.wfile.write(body)
                return
            # Streaming Responses SSE
            self.send_response(200)
            self.send_header("Content-Type", "text/event-stream")
            self.end_headers()

            events = []

            def ev(t, d):
                events.append((t, d))

            ev("response.created", {"type": "response.created",
                                    "response": {"id": "resp-mock", "model": model}})
            ev("response.output_item.added", {
                "type": "response.output_item.added", "output_index": 0,
                "item": {"type": "message", "role": "assistant"}})
            for delta in ("E2E", "_OK"):
                ev("response.output_text.delta", {
                    "type": "response.output_text.delta",
                    "item_id": "msg_0", "output_index": 0, "delta": delta})
            ev("response.output_item.done", {
                "type": "response.output_item.done", "output_index": 0,
                "item": {"type": "message", "role": "assistant",
                         "content": [{"type": "output_text", "text": "E2E_OK"}]}})
            ev("response.completed", {
                "type": "response.completed",
                "response": {"id": "resp-mock", "model": model, "status": "completed",
                             "output": [{"type": "message", "role": "assistant",
                                         "content": [{"type": "output_text", "text": "E2E_OK"}]}],
                             "usage": {"input_tokens": 5, "output_tokens": 2}}})
            for i, (t, d) in enumerate(events):
                if ARGS.cut_stream_after and i >= ARGS.cut_stream_after:
                    self.close_connection = True
                    return
                self.wfile.write(f"event: {t}\ndata: {json.dumps(d)}\n\n".encode())
                self.wfile.flush()
            return

        self.send_error(404)


if __name__ == "__main__":
    ThreadingHTTPServer(("127.0.0.1", PORT), Handler).serve_forever()
