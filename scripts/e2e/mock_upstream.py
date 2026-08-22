#!/usr/bin/env python3
"""Mock OpenAI-compatible upstream for llm-proxy live E2E tests.

Serves /v1/models, /v1/chat/completions, and /v1/responses with canned
responses (both non-streaming and SSE). Run:

    python3 scripts/e2e/mock_upstream.py [port]

Then point an llm-proxy venice backend at http://127.0.0.1:<port>/v1 via
base_url and exercise Claude Code / Codex through the proxy without any
real provider credentials.
"""
import json
import sys
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

PORT = int(sys.argv[1]) if len(sys.argv) > 1 else 9911


def sse(handler, events):
    handler.send_response(200)
    handler.send_header("Content-Type", "text/event-stream")
    handler.send_header("Cache-Control", "no-cache")
    handler.end_headers()
    for ev in events:
        handler.wfile.write(f"data: {json.dumps(ev)}\n\n".encode())
        handler.wfile.flush()


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

    def do_POST(self):
        req = self._body()
        stream = bool(req.get("stream"))

        if self.path.startswith("/v1/chat/completions"):
            model = req.get("model", "stealth-mock")
            if not stream:
                body = json.dumps({
                    "id": "chatcmpl-mock", "object": "chat.completion",
                    "created": 1700000000, "model": model,
                    "choices": [{"index": 0,
                                 "message": {"role": "assistant", "content": "E2E_OK"},
                                 "finish_reason": "stop"}],
                    "usage": {"prompt_tokens": 5, "completion_tokens": 2, "total_tokens": 7},
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
            for c in chunks:
                self.wfile.write(f"data: {json.dumps(c)}\n\n".encode())
                self.wfile.flush()
            self.wfile.write(b"data: [DONE]\n\n")
            self.wfile.flush()
            return

        if self.path.startswith("/v1/responses"):
            model = req.get("model", "stealth-mock")
            if not stream:
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

            def ev(t, d):
                self.wfile.write(f"event: {t}\ndata: {json.dumps(d)}\n\n".encode())
                self.wfile.flush()

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
            return

        self.send_error(404)


if __name__ == "__main__":
    ThreadingHTTPServer(("127.0.0.1", PORT), Handler).serve_forever()
