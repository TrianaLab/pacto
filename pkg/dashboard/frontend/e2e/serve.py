"""Static file server for the browser suite: `python3 e2e/serve.py PORT DIR`.

`python3 -m http.server` listens with a backlog of 5 (socketserver's default
request_queue_size). Every Playwright worker opens a fresh browser at once, each
asking for the page's scripts in parallel, so the accept queue overflows; macOS
answers the overflow with a reset, Linux by dropping the SYN until a retry. A
reset module script leaves the app unmounted: the page shows only the demo strip
and the test times out waiting for a heading that will never render. A real
backlog is the whole fix.
"""
import functools
import http.server
import sys


class Server(http.server.ThreadingHTTPServer):
    request_queue_size = 1024  # the kernel clamps this to its own ceiling (somaxconn)


port, directory = int(sys.argv[1]), sys.argv[2]
handler = functools.partial(http.server.SimpleHTTPRequestHandler, directory=directory)
with Server(("127.0.0.1", port), handler) as httpd:
    httpd.serve_forever()
