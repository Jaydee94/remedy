#!/usr/bin/env python3
"""Spike: an MCP server over Streamable HTTP (stdlib only) with one tool that blocks for N seconds.

  python3 mcp_blocking.py <port> <mode> [token]

mode "json": the tools/call answer is a plain JSON response that arrives after the delay.
mode "sse":  the answer is a text/event-stream; the headers go out at once, a comment line is sent every
             KEEPALIVE seconds, and the result arrives as an event after the delay.
mode "ssep": like "sse", but every KEEPALIVE seconds an MCP progress notification is sent instead of a comment
             (it names the progressToken the CLI put into the request's _meta).

Every request is logged to stdout with a timestamp, so the log shows when the CLI gave up (the connection is closed
before the answer was written) and whether the Authorization header arrived.
"""
import json
import sys
import threading
import time
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

PORT = int(sys.argv[1])
MODE = sys.argv[2]
TOKEN = sys.argv[3] if len(sys.argv) > 3 else "run-token-spike"
KEEPALIVE = float(__import__("os").environ.get("KEEPALIVE", "20"))
T0 = time.time()
lock = threading.Lock()


def log(*a):
    with lock:
        print(f"[{time.time() - T0:8.1f}s]", *a, flush=True)


TOOLS = [
    {
        "name": "wait",
        "description": "Waits for the given number of seconds and then returns 'waited'. Use it when asked to wait.",
        "inputSchema": {"type": "object", "properties": {"seconds": {"type": "number"}}, "required": ["seconds"]},
    }
]


class H(BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.1"

    def log_message(self, *a):
        pass

    def reply_json(self, obj, status=200):
        b = json.dumps(obj).encode()
        self.send_response(status)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(b)))
        self.end_headers()
        self.wfile.write(b)

    def do_GET(self):
        log("GET", self.path, "-> 405")
        self.send_response(405)
        self.send_header("Content-Length", "0")
        self.end_headers()

    def do_DELETE(self):
        log("DELETE", self.path)
        self.send_response(200)
        self.send_header("Content-Length", "0")
        self.end_headers()

    def do_POST(self):
        n = int(self.headers.get("Content-Length", "0"))
        body = json.loads(self.rfile.read(n) or b"{}")
        auth = self.headers.get("Authorization", "")
        method, rid = body.get("method"), body.get("id")
        log("POST", method, "id=%s" % rid, "auth_ok=%s" % (auth == "Bearer " + TOKEN))
        if auth != "Bearer " + TOKEN:
            self.reply_json({"error": "unauthorized"}, 401)
            return
        if method == "initialize":
            self.reply_json({"jsonrpc": "2.0", "id": rid, "result": {
                "protocolVersion": body["params"].get("protocolVersion", "2025-03-26"),
                "capabilities": {"tools": {}},
                "serverInfo": {"name": "spike", "version": "0"}}})
        elif rid is None:  # a notification
            self.send_response(202)
            self.send_header("Content-Length", "0")
            self.end_headers()
        elif method == "tools/list":
            self.reply_json({"jsonrpc": "2.0", "id": rid, "result": {"tools": TOOLS}})
        elif method == "tools/call":
            secs = float(body["params"]["arguments"].get("seconds", 0))
            meta = body["params"].get("_meta") or {}
            token = meta.get("progressToken")
            log("tools/call _meta:", json.dumps(meta), "progressToken=%r" % (token,))
            result = {"jsonrpc": "2.0", "id": rid, "result": {"content": [{"type": "text", "text": "waited %gs" % secs}]}}
            try:
                if MODE == "json":
                    time.sleep(secs)
                    log("tools/call answering after", secs, "s (json)")
                    self.reply_json(result)
                else:
                    self.send_response(200)
                    self.send_header("Content-Type", "text/event-stream")
                    self.send_header("Cache-Control", "no-cache")
                    self.send_header("Transfer-Encoding", "chunked")
                    self.end_headers()

                    def chunk(data: bytes):
                        self.wfile.write(b"%x\r\n" % len(data) + data + b"\r\n")
                        self.wfile.flush()

                    end = time.time() + secs
                    n = 0
                    while time.time() < end:
                        time.sleep(min(KEEPALIVE, max(0.0, end - time.time())))
                        if time.time() < end:
                            if MODE == "ssep":
                                n += 1
                                note = {"jsonrpc": "2.0", "method": "notifications/progress",
                                        "params": {"progressToken": token if token is not None else 1, "progress": n,
                                                   "message": "waiting for approval"}}
                                chunk(b"event: message\ndata: " + json.dumps(note).encode() + b"\n\n")
                            else:
                                chunk(b": keepalive\n\n")
                    log("tools/call answering after", secs, "s (sse)")
                    chunk(b"event: message\ndata: " + json.dumps(result).encode() + b"\n\n")
                    chunk(b"")
                log("tools/call answered")
            except (BrokenPipeError, ConnectionResetError) as e:
                log("tools/call: the client went away before the answer:", type(e).__name__)
        else:
            self.reply_json({"jsonrpc": "2.0", "id": rid, "error": {"code": -32601, "message": "unknown method"}})


ThreadingHTTPServer(("127.0.0.1", PORT), H).serve_forever()
