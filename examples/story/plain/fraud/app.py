"""fraud scores a payment. A plain service: nothing here knows about Pacto yet."""
import json
from http.server import BaseHTTPRequestHandler, HTTPServer


class Handler(BaseHTTPRequestHandler):
    def do_POST(self):
        if self.path != "/score":
            self.send_error(404)
            return
        body = json.dumps({"score": 0.02}).encode()
        self.send_response(200)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)


HTTPServer(("", 8080), Handler).serve_forever()
