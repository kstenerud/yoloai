#!/usr/bin/env python3
# ABOUTME: Mock Anthropic /v1/messages endpoint used to validate the egress-broker
# ABOUTME: base_url-redirect + credential-injection seam (D105). Logs the inbound auth
# ABOUTME: header (proving redirect + which credential the agent sent) and returns a
# ABOUTME: canned SSE stream so the agent renders a response end-to-end. The future
# ABOUTME: injector's integration test should reuse/relocate this. See README.md.
import http.server, sys

SSE = (
    "event: message_start\n"
    'data: {"type":"message_start","message":{"id":"msg_1","type":"message","role":"assistant","model":"mock","content":[],"stop_reason":null,"stop_sequence":null,"usage":{"input_tokens":1,"output_tokens":1}}}\n\n'
    "event: content_block_start\n"
    'data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}\n\n'
    "event: content_block_delta\n"
    'data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"BROKER_SPIKE_OK"}}\n\n'
    "event: content_block_stop\n"
    'data: {"type":"content_block_stop","index":0}\n\n'
    "event: message_delta\n"
    'data: {"type":"message_delta","delta":{"stop_reason":"end_turn","stop_sequence":null},"usage":{"output_tokens":3}}\n\n'
    "event: message_stop\n"
    'data: {"type":"message_stop"}\n\n'
)


class H(http.server.BaseHTTPRequestHandler):
    def _log(self):
        # The agent must arrive here (redirect worked) carrying a placeholder
        # credential the real injector would strip + replace with the live key.
        auth = self.headers.get("authorization") or self.headers.get("x-api-key") or "(none)"
        sys.stderr.write(f"[MOCK] {self.command} {self.path} auth={auth!r}\n")
        sys.stderr.flush()

    def do_POST(self):
        ln = int(self.headers.get("content-length") or 0)
        self.rfile.read(ln)
        self._log()
        if "messages" in self.path:
            self.send_response(200)
            self.send_header("content-type", "text/event-stream")
            self.end_headers()
            self.wfile.write(SSE.encode())
        else:
            self.send_response(200)
            self.send_header("content-type", "application/json")
            self.end_headers()
            self.wfile.write(b"{}")

    def do_GET(self):
        self._log()
        self.send_response(200)
        self.send_header("content-type", "application/json")
        self.end_headers()
        self.wfile.write(b"{}")

    def log_message(self, *a):
        pass


http.server.HTTPServer(("127.0.0.1", 8765), H).serve_forever()
