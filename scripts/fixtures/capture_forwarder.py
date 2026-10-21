#!/usr/bin/env python3
"""Logging HTTP forwarder for recording client/upstream exchanges.

Usage: capture_forwarder.py <listen_port> <upstream_base> <log_prefix>

Forwards every request to <upstream_base> and streams the response back,
writing POST bodies to <log_prefix>NNN.req.json, the path to NNN.meta, and the
response body to NNN.resp. Run one in front of the proxy (prefix cc_) and one
between the proxy and its upstream (prefix up_), then feed the directory to
anonymize_transcripts.py. Captures contain raw prompts and credentials-adjacent
metadata: keep them out of the repository until anonymized.
"""
import http.server, socketserver, sys, urllib.request, itertools, json
port, upstream, prefix = int(sys.argv[1]), sys.argv[2].rstrip('/'), sys.argv[3]
seq = itertools.count()
class H(http.server.BaseHTTPRequestHandler):
    protocol_version = 'HTTP/1.1'
    def _go(self, method):
        n = next(seq)
        body = self.rfile.read(int(self.headers.get('content-length', 0) or 0)) if method == 'POST' else None
        if body is not None:
            open(f'{prefix}{n:03d}.req.json', 'wb').write(body)
            open(f'{prefix}{n:03d}.meta', 'w').write(self.path + '\n')
        hdrs = {k: v for k, v in self.headers.items() if k.lower() not in ('host', 'content-length', 'connection', 'accept-encoding')}
        req = urllib.request.Request(upstream + self.path, data=body, method=method, headers=hdrs)
        try:
            resp = urllib.request.urlopen(req, timeout=600)
        except urllib.error.HTTPError as e:
            resp = e
        self.send_response(resp.status if hasattr(resp, 'status') else resp.code)
        for k, v in resp.headers.items():
            if k.lower() not in ('transfer-encoding', 'connection', 'content-length'):
                self.send_header(k, v)
        self.send_header('Transfer-Encoding', 'chunked'); self.end_headers()
        out = open(f'{prefix}{n:03d}.resp', 'wb') if body is not None else None
        while True:
            chunk = resp.read1(65536) if hasattr(resp, 'read1') else resp.read(65536)
            if not chunk: break
            if out: out.write(chunk); out.flush()
            self.wfile.write(b'%x\r\n%s\r\n' % (len(chunk), chunk)); self.wfile.flush()
        self.wfile.write(b'0\r\n\r\n')
    def do_POST(self): self._go('POST')
    def do_GET(self): self._go('GET')
    def log_message(self, *a): pass
class S(socketserver.ThreadingMixIn, http.server.HTTPServer): daemon_threads = True
S(('127.0.0.1', port), H).serve_forever()
