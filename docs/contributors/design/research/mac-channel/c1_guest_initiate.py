# ABOUTME: Can the guest OPEN a channel to the host, rather than being connected
# ABOUTME: to? Tries vsock to CID 2 (host) across a spread of ports.
import socket
HOST_CID = 2
results = []
for port in (1024, 2000, 8080, 50000):
    try:
        s = socket.socket(socket.AF_VSOCK, socket.SOCK_STREAM)
        s.settimeout(3)
        s.connect((HOST_CID, port))
        results.append(f"port{port}=CONNECTED")
        s.close()
    except Exception as e:
        results.append(f"port{port}={type(e).__name__}")
print(" ".join(results))
