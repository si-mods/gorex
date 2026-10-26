"""rexctl: a small client of GoRex's session server, for tests.

python3 -I rexctl.py --sock PATH hello|list|shutdown
python3 -I rexctl.py --sock PATH type SID TEXT   (\\r is Enter, \\x03 is Ctrl-C)
python3 -I rexctl.py --sock PATH screen SID
"""
import json
import socket
import sys
import time


def call(sock, op, **kw):
    s = socket.socket(socket.AF_UNIX)
    s.connect(sock)
    s.sendall((json.dumps(dict(id=1, op=op, **kw)) + "\n").encode())
    r = json.loads(s.makefile().readline())
    s.close()
    if r.get("error"):
        sys.exit(r["error"])
    return r.get("data")


def attach(sock, sid, data=b"", wait=1.0):
    s = socket.socket(socket.AF_UNIX)
    s.connect(sock)
    s.sendall((json.dumps(dict(op="attach", sid=sid, cols=120, rows=40)) + "\n").encode() + data)
    s.settimeout(wait)
    out, end = b"", time.time() + wait
    try:
        while time.time() < end:
            b = s.recv(65536)
            if not b:
                break
            out += b
    except socket.timeout:
        pass
    s.close()
    return out


def main(argv):
    if len(argv) < 3 or argv[0] != "--sock":
        sys.exit(__doc__)
    sock, cmd, args = argv[1], argv[2], argv[3:]
    if cmd == "hello":
        h = call(sock, "hello")
        print(h["pid"], h.get("exe"), h.get("exeTime"), h.get("upgrade", False))
    elif cmd == "list":
        for i in call(sock, "list") or []:
            print(i["id"], i["pid"], i.get("program"), i.get("exited", False))
    elif cmd == "shutdown":
        call(sock, "shutdown")
    elif cmd == "type":
        attach(sock, args[0], args[1].encode().decode("unicode_escape").encode(), 0.5)
    elif cmd == "screen":
        sys.stdout.write(attach(sock, args[0]).decode(errors="replace"))
    else:
        sys.exit(__doc__)


if __name__ == "__main__":
    main(sys.argv[1:])
