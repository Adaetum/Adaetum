"""Serve the product demo player on the local network (port 8001) with caching off, so a refresh always shows the latest scene, script and audio.
Only the player files are served; model files, H3 packages and scripts are blocked."""
import socket
from functools import partial
from http.server import SimpleHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path

BLOCKED = ("/h3", "/piper_voices", "/.espeak-ng-data", "/_", "/.", "/tts", "/models")
class NoCache(SimpleHTTPRequestHandler):
    def do_GET(self):
        if self.path.startswith(BLOCKED) or self.path.endswith((".py", ".md")):
            self.send_error(404); return
        super().do_GET()
    def end_headers(self):
        self.send_header("Cache-Control", "no-store")
        super().end_headers()
    def log_message(self, *args):
        pass

root = Path(__file__).resolve().parent
try:
    ip = socket.gethostbyname(socket.gethostname())
except OSError:
    ip = "<this-machine-ip>"
print("Player: http://127.0.0.1:8001/player.html   on the network: http://%s:8001/player.html" % ip)
ThreadingHTTPServer(("0.0.0.0", 8001), partial(NoCache, directory=str(root))).serve_forever()
