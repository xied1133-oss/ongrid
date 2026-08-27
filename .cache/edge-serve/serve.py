#!/usr/bin/env python3
"""临时 HTTPS 静态分发点：模拟生产 nginx 的 /edge/ + /install.sh。"""
import http.server, ssl, os, sys

os.chdir(os.path.dirname(os.path.abspath(__file__)))

class H(http.server.SimpleHTTPRequestHandler):
    def log_message(self, *a):
        sys.stderr.write("%s\n" % (a[0] % a[1:]))

httpd = http.server.HTTPServer(("0.0.0.0", 8443), H)
ctx = ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER)
ctx.load_cert_chain("cert.pem", "key.pem")
httpd.socket = ctx.wrap_socket(httpd.socket, server_side=True)
print("serving https://0.0.0.0:8443", flush=True)
httpd.serve_forever()
