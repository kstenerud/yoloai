# ABOUTME: Can the guest OPEN a channel to the host, rather than being connected
# ABOUTME: to? Tries vsock to CID 2 (host) across a spread of ports.
import socket

# AF_VSOCK is Linux-only, and this script's typecheck runs on macOS too (the
# release gate is `make releasetest` on both hosts). Reach for it the way the
# sibling c1_guest_vsock.py does — a getattr typechecks identically on a host
# that has the constant and one that does not.
AF_VSOCK = getattr(socket, "AF_VSOCK", None)
HOST_CID = 2
results = []
if AF_VSOCK is None:
    print("AF_VSOCK=absent-in-python")
    raise SystemExit(0)
for port in (1024, 2000, 8080, 50000):
    try:
        s = socket.socket(AF_VSOCK, socket.SOCK_STREAM)
        s.settimeout(3)
        s.connect((HOST_CID, port))
        results.append(f"port{port}=CONNECTED")
        s.close()
    except Exception as e:
        results.append(f"port{port}={type(e).__name__}")
print(" ".join(results))
