"""Probe RTSP path patterns by sending DESCRIBE requests over a raw TCP socket.

Bypasses OpenCV/FFmpeg entirely — those have a hardcoded 30s interrupt callback
on Windows that makes path-sweeping painfully slow. RTSP is a simple text
protocol like HTTP, so we just connect, send DESCRIBE, read the status line.

    python probe_rtsp.py 192.168.1.50 192.168.1.51
    python probe_rtsp.py 192.168.1.50 --user admin --password admin

Status codes interpreted:
  200 OK              -> path exists, no auth required (parses SDP for media tracks)
  401 Unauthorized    -> path exists, auth required (retried with Digest if creds given)
  404 Not Found       -> path does not exist
  other               -> reported verbatim
"""

from __future__ import annotations

import argparse
import hashlib
import re
import socket
import sys
from concurrent.futures import ThreadPoolExecutor, as_completed

PATHS = [
    "/1", "/2", "/3",
    "/11", "/12",
    "/live", "/live/main", "/live/sub", "/live/av0", "/live/av1",
    "/stream1", "/stream2",
    "/h264", "/h264_stream", "/h265",
    "/av0_0", "/av0_1",
    "/cam/realmonitor?channel=1&subtype=0",
    "/cam/realmonitor?channel=1&subtype=1",
    "/Streaming/Channels/101",
    "/Streaming/Channels/102",
    "/profile1", "/profile2",
    "/main", "/sub",
    "/video1", "/video2",
    "/0", "/4",
    "/",
]

CONNECT_TIMEOUT = 2.0
RECV_TIMEOUT = 2.5
RECV_BYTES = 4096


def md5(s: str) -> str:
    return hashlib.md5(s.encode()).hexdigest()


def build_digest(user: str, password: str, method: str, url: str, www_auth: str) -> str | None:
    realm = re.search(r'realm="([^"]+)"', www_auth)
    nonce = re.search(r'nonce="([^"]+)"', www_auth)
    if not realm or not nonce:
        return None
    realm_v, nonce_v = realm.group(1), nonce.group(1)
    ha1 = md5(f"{user}:{realm_v}:{password}")
    ha2 = md5(f"{method}:{url}")
    response = md5(f"{ha1}:{nonce_v}:{ha2}")
    return (
        f'Digest username="{user}", realm="{realm_v}", nonce="{nonce_v}", '
        f'uri="{url}", response="{response}"'
    )


def describe_once(host: str, port: int, url: str, cseq: int, auth_header: str | None) -> tuple[int, str, bytes]:
    """Send DESCRIBE, return (status_code, status_text, full_response_bytes)."""
    req = (
        f"DESCRIBE {url} RTSP/1.0\r\n"
        f"CSeq: {cseq}\r\n"
        f"User-Agent: rtsp-probe/1.0\r\n"
        f"Accept: application/sdp\r\n"
    )
    if auth_header:
        req += f"Authorization: {auth_header}\r\n"
    req += "\r\n"

    with socket.create_connection((host, port), timeout=CONNECT_TIMEOUT) as s:
        s.settimeout(RECV_TIMEOUT)
        s.sendall(req.encode())
        chunks = []
        try:
            while True:
                buf = s.recv(RECV_BYTES)
                if not buf:
                    break
                chunks.append(buf)
                if len(b"".join(chunks)) > 16384:
                    break
                # If we have headers + first part of body, that's enough.
                if b"\r\n\r\n" in b"".join(chunks):
                    # try one more recv with short timeout for SDP body, then bail
                    s.settimeout(0.3)
                    try:
                        more = s.recv(RECV_BYTES)
                        if more:
                            chunks.append(more)
                    except socket.timeout:
                        pass
                    break
        except socket.timeout:
            pass

    data = b"".join(chunks)
    line = data.split(b"\r\n", 1)[0].decode("ascii", errors="replace")
    m = re.match(r"RTSP/1\.\d (\d+) (.*)", line)
    if not m:
        return -1, line[:60], data
    return int(m.group(1)), m.group(2), data


def parse_sdp_tracks(data: bytes) -> list[str]:
    body = data.split(b"\r\n\r\n", 1)
    if len(body) < 2:
        return []
    text = body[1].decode("utf-8", errors="replace")
    tracks = []
    cur_media = None
    for line in text.splitlines():
        if line.startswith("m="):
            cur_media = line.split()[0][2:] if " " in line else line[2:]
            tracks.append(cur_media)
        elif line.startswith("a=rtpmap:") and tracks:
            codec = line.split(" ", 1)[1] if " " in line else ""
            tracks[-1] = f"{tracks[-1]}({codec})"
    return tracks


def probe_path(host: str, port: int, path: str, user: str | None, password: str | None) -> dict:
    url = f"rtsp://{host}:{port}{path}"
    try:
        code, reason, data = describe_once(host, port, url, 1, None)
    except (socket.timeout, OSError) as e:
        return {"path": path, "code": None, "reason": f"socket: {e.__class__.__name__}", "tracks": []}

    if code == 401 and user and password:
        www = re.search(rb"WWW-Authenticate:\s*([^\r\n]+)", data, re.IGNORECASE)
        if www:
            auth = build_digest(user, password, "DESCRIBE", url, www.group(1).decode())
            if auth:
                try:
                    code, reason, data = describe_once(host, port, url, 2, auth)
                except (socket.timeout, OSError) as e:
                    return {"path": path, "code": 401, "reason": f"auth-retry-failed: {e.__class__.__name__}", "tracks": []}

    tracks = parse_sdp_tracks(data) if code == 200 else []
    return {"path": path, "code": code, "reason": reason, "tracks": tracks}


def probe_camera(host: str, port: int, user: str | None, password: str | None) -> list[dict]:
    results = []
    with ThreadPoolExecutor(max_workers=8) as ex:
        futures = {ex.submit(probe_path, host, port, p, user, password): p for p in PATHS}
        for fut in as_completed(futures):
            results.append(fut.result())
    results.sort(key=lambda r: PATHS.index(r["path"]))
    return results


def fmt_status(r: dict) -> str:
    code = r["code"]
    if code is None:
        return f"  -- {r['reason']:30s}"
    marker = "**" if code == 200 else ("##" if code == 401 else "  ")
    extra = ""
    if r["tracks"]:
        extra = f"  tracks: {', '.join(r['tracks'])}"
    return f"  {marker} {code} {r['reason']:24s}{extra}"


def main() -> int:
    ap = argparse.ArgumentParser()
    ap.add_argument("ips", nargs="+")
    ap.add_argument("--port", type=int, default=554)
    ap.add_argument("--user", default=None)
    ap.add_argument("--password", default=None)
    args = ap.parse_args()

    for ip in args.ips:
        print(f"\n=== {ip} ===", flush=True)
        results = probe_camera(ip, args.port, args.user, args.password)
        for r in results:
            print(f"  {r['path']:45s} {fmt_status(r)}", flush=True)
        ok = [r for r in results if r["code"] == 200]
        needs_auth = [r for r in results if r["code"] == 401]
        print(f"  -> {len(ok)} OK, {len(needs_auth)} need auth", flush=True)
        if ok:
            for r in ok:
                print(f"     OK  rtsp://{ip}:{args.port}{r['path']}")
        if needs_auth and not (args.user and args.password):
            print(f"     re-run with --user X --password Y to verify {len(needs_auth)} 401 path(s)")
    return 0


if __name__ == "__main__":
    sys.exit(main())
