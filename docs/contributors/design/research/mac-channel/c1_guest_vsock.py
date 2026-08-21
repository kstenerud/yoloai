# ABOUTME: Guest-side capability read for C1 — which socket families exist in an
# ABOUTME: apple container guest, and what its vsock CID is.
import socket, os, fcntl, struct
for fam in ("AF_VSOCK", "AF_UNIX", "AF_INET"):
    f = getattr(socket, fam, None)
    if f is None:
        print(f"{fam}=absent-in-python"); continue
    try:
        s = socket.socket(f, socket.SOCK_STREAM); s.close(); print(f"{fam}=ok")
    except Exception as e:
        print(f"{fam}=FAILED:{type(e).__name__}")
print("dev_vsock=", os.path.exists("/dev/vsock"))
try:
    fd = os.open("/dev/vsock", os.O_RDONLY)
    buf = fcntl.ioctl(fd, 0x7b9, struct.pack("I", 0))
    print("local_cid=", struct.unpack("I", buf)[0]); os.close(fd)
except Exception as e:
    print("local_cid=FAILED:", type(e).__name__)
