# ABOUTME: Guest half of C1 — listens on a unix socket inside the guest so the
# ABOUTME: host end of --publish-socket has something to talk to.
import socket, os, sys
path = sys.argv[1]
if os.path.exists(path): os.unlink(path)
s = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
s.bind(path); s.listen(16); os.chmod(path, 0o777)
print("GUEST-LISTENING", path, flush=True)
while True:
    try:
        c, _ = s.accept()
    except OSError:
        break
    data = c.recv(4096)
    print("GUEST-RECEIVED:", data.decode(errors="replace").strip(), flush=True)
    c.sendall(b"PONG-FROM-GUEST\n")
    c.close()
