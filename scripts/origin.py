#!/usr/bin/env python3
# SPDX-License-Identifier: GPL-3.0-or-later
"""Local replay origin for offline E2E tests (Python 3 stdlib only).

Serves the committed replay fixtures and can inject network faults so the
recording/repair pipeline can be exercised without touching Bilibili.

Endpoints
---------
GET /health
    JSON status (used by scripts/e2e.sh to wait for readiness).

GET /flv/<path>
    Streams a file from ``--flv-root`` (default: ``testdata/flv``) using
    HTTP/1.1 chunked transfer encoding and a byte rate limiter
    (``--throttle`` / ``THROTTLE_BPS``, default 1048576 = 1 MiB/s).
    Fault injection query parameters (all optional):
      ?drop_after=<bytes>  send at most that many payload bytes, then abort the
                           connection mid-stream (no terminating chunk), like a
                           dropped upstream. Values >= file size do not abort.
      ?corrupt=1           XOR a bounded byte region (bytes [64, 64+256)) with
                           0xA5, leaving the FLV header/tag sizes valid enough
                           for a parser to resynchronise. Bounded on purpose.
    ``?drop_after=...&corrupt=1`` first corrupts, then truncates.

GET /hls/<path>
    Static serving from ``--hls-root`` (default: ``testdata/hls``): the TS and
    fMP4 playlists and their relative-URI segments. Correct content types,
    byte ranges (HTTP 206) and HEAD are supported. Path traversal is rejected.

Run
---
    python3 scripts/origin.py                      # 127.0.0.1:$ORIGIN_PORT (18080)
    ORIGIN_PORT=0 python3 scripts/origin.py        # pick a free port, printed on stderr
    python3 scripts/origin.py --selftest           # start ephemeral server, verify, exit 0

Environment:
    ORIGIN_PORT      listen port (default 18080; 0 = ephemeral)
    ORIGIN_HOST      bind host (default 127.0.0.1)
    THROTTLE_BPS     /flv throttle in bytes/second (default 1048576; 0 = unlimited)
    ORIGIN_FLV_ROOT  directory behind /flv/ (default <repo>/testdata/flv)
    ORIGIN_HLS_ROOT  directory behind /hls/ (default <repo>/testdata/hls)
"""

from __future__ import annotations

import argparse
import json
import os
import socket
import sys
import tempfile
import threading
import time
import urllib.error
import urllib.request
from http.client import HTTPConnection, IncompleteRead
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from urllib.parse import parse_qs, unquote, urlparse

REPO_ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
DEFAULT_PORT = 18080
DEFAULT_THROTTLE_BPS = 1024 * 1024
DEFAULT_FLV_ROOT = os.path.join(REPO_ROOT, "testdata", "flv")
DEFAULT_HLS_ROOT = os.path.join(REPO_ROOT, "testdata", "hls")

# Bounded corruption window used by ?corrupt=1. Kept after the 9-byte FLV header
# + 4-byte PreviousTagSize0 so the container still starts with "FLV".
CORRUPT_START = 64
CORRUPT_LEN = 256
CORRUPT_XOR = 0xA5

CONTENT_TYPES = {
    ".m3u8": "application/vnd.apple.mpegurl",
    ".ts": "video/mp2t",
    ".m4s": "video/iso.segment",
    ".mp4": "video/mp4",
    ".m4v": "video/mp4",
    ".m4a": "audio/mp4",
    ".aac": "audio/aac",
    ".flv": "video/x-flv",
    ".json": "application/json",
    ".txt": "text/plain; charset=utf-8",
    ".html": "text/html; charset=utf-8",
}

CHUNK_SIZE_MAX = 64 * 1024
CHUNK_SIZE_MIN = 4 * 1024


def _content_type(path: str) -> str:
    return CONTENT_TYPES.get(os.path.splitext(path)[1].lower(), "application/octet-stream")


def _resolve_in(root: str, rel: str) -> str | None:
    """Resolve ``rel`` under ``root``; return None on traversal attempts."""
    if not rel or rel.startswith("/"):
        rel = rel.lstrip("/")
    root_real = os.path.realpath(root)
    candidate = os.path.realpath(os.path.join(root_real, rel))
    if candidate == root_real:
        return None
    if not candidate.startswith(root_real + os.sep):
        return None
    return candidate


class OriginServer(ThreadingHTTPServer):
    daemon_threads = True
    allow_reuse_address = True

    def __init__(self, addr, flv_root: str, hls_root: str, throttle_bps: int):
        super().__init__(addr, OriginHandler)
        self.flv_root = os.path.abspath(flv_root)
        self.hls_root = os.path.abspath(hls_root)
        self.throttle_bps = throttle_bps


class OriginHandler(BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.1"
    server_version = "dsh-origin/1.0"
    sys_version = ""

    # -- plumbing ---------------------------------------------------------

    def log_message(self, fmt: str, *args) -> None:  # keep test logs quiet-ish
        sys.stderr.write("[origin] %s - %s\n" % (self.address_string(), fmt % args))

    def _send_text(self, status: int, body: str) -> None:
        data = body.encode("utf-8")
        self.send_response(status)
        self.send_header("Content-Type", "text/plain; charset=utf-8")
        self.send_header("Content-Length", str(len(data)))
        self.send_header("Cache-Control", "no-store")
        self.end_headers()
        if self.command != "HEAD":
            self.wfile.write(data)

    def _send_json(self, status: int, obj: dict) -> None:
        data = json.dumps(obj, sort_keys=True).encode("utf-8")
        self.send_response(status)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(data)))
        self.send_header("Cache-Control", "no-store")
        self.end_headers()
        if self.command != "HEAD":
            self.wfile.write(data)

    # -- GET / HEAD -------------------------------------------------------

    def do_HEAD(self) -> None:  # noqa: N802 (http.server API)
        self._dispatch(head_only=True)

    def do_GET(self) -> None:  # noqa: N802
        self._dispatch(head_only=False)

    def _dispatch(self, head_only: bool) -> None:
        parsed = urlparse(self.path)
        path = unquote(parsed.path)
        query = parse_qs(parsed.query)
        try:
            if path in ("/health", "/healthz"):
                self._send_json(200, {
                    "ok": True,
                    "flv_root": self.server.flv_root,
                    "hls_root": self.server.hls_root,
                    "throttle_bps": self.server.throttle_bps,
                    "port": self.server.server_address[1],
                })
            elif path == "/" or path == "":
                self._send_text(200, (
                    "dsh local replay origin\n"
                    "  /flv/<path>   throttled chunked FLV (drop_after, corrupt)\n"
                    "  /hls/<path>   HLS ts/fmp4 static fixtures\n"
                    "  /healthz      JSON health\n"
                ))
            elif path.startswith("/flv/"):
                self._handle_flv(path[len("/flv/"):], query, head_only)
            elif path.startswith("/hls/"):
                self._handle_hls(path[len("/hls/"):], query, head_only)
            else:
                self._send_text(404, "not found: %s\n" % path)
        except (BrokenPipeError, ConnectionResetError):
            self.close_connection = True
        except Exception as exc:  # keep serving other requests on a bad one
            sys.stderr.write("[origin] handler error on %s: %r\n" % (self.path, exc))
            self.close_connection = True

    # -- /flv -------------------------------------------------------------

    def _handle_flv(self, rel: str, query: dict, head_only: bool) -> None:
        file_path = _resolve_in(self.server.flv_root, rel)
        if file_path is None or not os.path.isfile(file_path):
            self._send_text(404, (
                "flv fixture not found: %r under %s\n"
                "(set ORIGIN_FLV_ROOT or generate testdata/flv fixtures)\n"
                % (rel, self.server.flv_root)
            ))
            return

        size = os.path.getsize(file_path)
        drop_after = _int_param(query, "drop_after")
        corrupt = query.get("corrupt", ["0"])[0] in ("1", "true", "yes")
        bps = self.server.throttle_bps

        if head_only:
            self.send_response(200)
            self.send_header("Content-Type", "video/x-flv")
            self.send_header("Content-Length", str(size))
            self.send_header("Cache-Control", "no-store")
            self.end_headers()
            return

        limit = size if drop_after is None else max(0, min(drop_after, size))
        abort = drop_after is not None and drop_after < size

        self.send_response(200)
        self.send_header("Content-Type", "video/x-flv")
        self.send_header("Transfer-Encoding", "chunked")
        self.send_header("Cache-Control", "no-store")
        self.send_header("X-Origin-Flv-Root", self.server.flv_root)
        self.send_header("X-Origin-Throttle-Bps", str(bps))
        self.send_header("X-Origin-Drop-After", str(drop_after) if drop_after is not None else "")
        self.send_header("X-Origin-Corrupt", "1" if corrupt else "0")
        self.end_headers()

        chunk_size = CHUNK_SIZE_MAX if bps <= 0 else max(CHUNK_SIZE_MIN, min(CHUNK_SIZE_MAX, bps // 8))
        started = time.monotonic()
        sent = 0
        try:
            with open(file_path, "rb") as fh:
                offset = 0
                while sent < limit:
                    want = min(chunk_size, limit - sent)
                    chunk = fh.read(want)
                    if not chunk:
                        break
                    if corrupt:
                        chunk = _corrupt(chunk, offset)
                    offset += len(chunk)
                    self.wfile.write(b"%x\r\n" % len(chunk))
                    self.wfile.write(chunk)
                    self.wfile.write(b"\r\n")
                    self.wfile.flush()
                    sent += len(chunk)
                    if bps > 0:
                        target = sent / bps
                        ahead = target - (time.monotonic() - started)
                        if ahead > 0:
                            time.sleep(ahead)
        except (BrokenPipeError, ConnectionResetError):
            self.close_connection = True
            return

        if abort:
            # Abort mid-stream: no terminating 0-length chunk, drop the socket.
            # Clients observe a truncated/invalid chunked body (or a reset).
            self.close_connection = True
            try:
                self.connection.shutdown(socket.SHUT_RDWR)
            except OSError:
                pass
            try:
                self.connection.close()
            except OSError:
                pass
            sys.stderr.write("[origin] /flv/%s?drop_after=%d aborted after %d/%d bytes\n"
                             % (rel, drop_after, sent, size))
            return

        self.wfile.write(b"0\r\n\r\n")
        self.wfile.flush()

    # -- /hls -------------------------------------------------------------

    def _handle_hls(self, rel: str, _query: dict, head_only: bool) -> None:
        file_path = _resolve_in(self.server.hls_root, rel)
        if file_path is None or not os.path.isfile(file_path):
            self._send_text(404, "hls fixture not found: %r under %s\n"
                                 % (rel, self.server.hls_root))
            return

        size = os.path.getsize(file_path)
        ctype = _content_type(file_path)
        start, end = 0, size - 1
        status = 200
        range_header = self.headers.get("Range", "").strip()

        if range_header.startswith("bytes=") and not head_only:
            spec = range_header[len("bytes="):].split(",")[0].strip()
            first, _, last = spec.partition("-")
            try:
                if first:
                    start = int(first)
                    end = int(last) if last else size - 1
                else:  # suffix range: bytes=-N
                    length = int(last)
                    start = max(0, size - length)
                    end = size - 1
            except ValueError:
                start, end = 0, size - 1
            else:
                end = min(end, size - 1)
                if start > end or start >= size:
                    self.send_response(416)
                    self.send_header("Content-Range", "bytes */%d" % size)
                    self.send_header("Content-Length", "0")
                    self.end_headers()
                    return
                status = 206

        length = end - start + 1
        self.send_response(status)
        self.send_header("Content-Type", ctype)
        self.send_header("Content-Length", str(length))
        self.send_header("Accept-Ranges", "bytes")
        self.send_header("Cache-Control", "no-cache")
        if status == 206:
            self.send_header("Content-Range", "bytes %d-%d/%d" % (start, end, size))
        self.end_headers()

        if head_only:
            return
        with open(file_path, "rb") as fh:
            fh.seek(start)
            remaining = length
            while remaining > 0:
                data = fh.read(min(CHUNK_SIZE_MAX, remaining))
                if not data:
                    break
                self.wfile.write(data)
                remaining -= len(data)


def _int_param(query: dict, name: str) -> int | None:
    raw = query.get(name)
    if not raw:
        return None
    try:
        return int(raw[0])
    except (TypeError, ValueError):
        return None


def _corrupt(chunk: bytes, offset: int) -> bytes:
    lo = max(CORRUPT_START, offset)
    hi = min(CORRUPT_START + CORRUPT_LEN, offset + len(chunk))
    if lo >= hi:
        return chunk
    buf = bytearray(chunk)
    for i in range(lo - offset, hi - offset):
        buf[i] ^= CORRUPT_XOR
    return bytes(buf)


# ---------------------------------------------------------------------------
# selftest
# ---------------------------------------------------------------------------

def _synthetic_flv(size: int = 256 * 1024) -> bytes:
    """Build a structurally valid FLV shell (header + prev-tag-size + tags).

    Deliberately not decodable media: it is only used to verify byte serving,
    throttling, corruption and truncation behaviour.
    """
    out = bytearray(b"FLV\x01\x05\x00\x00\x00\x09")  # header: v1, audio+video
    out += b"\x00\x00\x00\x00"                        # PreviousTagSize0
    payload = b"\x00" * 1024
    while len(out) < size:
        body = payload
        out += b"\x09"                                # video tag
        out += len(body).to_bytes(3, "big")           # data size
        out += b"\x00\x00\x00"                        # timestamp
        out += b"\x00\x00\x00"                        # stream id
        out += body
        out += (11 + len(body)).to_bytes(4, "big")    # PreviousTagSize
    return bytes(out[:size])


def _http_get(url: str, headers: dict | None = None, timeout: float = 15.0):
    req = urllib.request.Request(url, headers=headers or {})
    with urllib.request.urlopen(req, timeout=timeout) as resp:
        return resp.status, dict(resp.headers), resp.read()


def _write_fixture_file(root: str, name: str, data: bytes) -> str:
    os.makedirs(root, exist_ok=True)
    path = os.path.join(root, name)
    with open(path, "wb") as fh:
        fh.write(data)
    return path


def _fetch_playlist(origin: str, diag: "_Diag", rel_playlist: str, hls_root: str) -> None:
    playlist_url = "%s/hls/%s" % (origin, rel_playlist)
    try:
        status, headers, body = _http_get(playlist_url)
    except Exception as exc:  # noqa: BLE001 - selftest reports, never crashes
        diag.fail("GET %s raised %r" % (playlist_url, exc))
        return
    if status != 200:
        diag.fail("GET %s -> %d" % (playlist_url, status))
        return
    ctype = headers.get("Content-Type", "")
    if not ctype.startswith("application/vnd.apple.mpegurl"):
        diag.fail("playlist content-type %r" % ctype)
    text = body.decode("utf-8")
    if not text.startswith("#EXTM3U"):
        diag.fail("playlist does not start with #EXTM3U")
    diag.ok("playlist %s (%d bytes, %s)" % (rel_playlist, len(body), ctype))

    base_dir = rel_playlist.rsplit("/", 1)[0]
    refs: list[str] = []
    for line in text.splitlines():
        line = line.strip()
        if not line:
            continue
        if line.startswith("#EXT-X-MAP:"):
            uri = line.split('URI="', 1)[-1].split('"', 1)[0]
            refs.append(uri)
        elif not line.startswith("#"):
            refs.append(line)
    for uri in refs[:4]:  # playlist, init, first two segments is enough
        target_rel = uri if "/" in uri else "%s/%s" % (base_dir, uri)
        target_url = "%s/hls/%s" % (origin, target_rel)
        try:
            st, hdrs, payload = _http_get(target_url)
        except Exception as exc:  # noqa: BLE001
            diag.fail("GET %s raised %r" % (target_url, exc))
            continue
        if st != 200:
            diag.fail("GET %s -> %d" % (target_url, st))
            continue
        disk_path = _resolve_in(hls_root, target_rel)
        if not disk_path or not os.path.isfile(disk_path):
            diag.fail("referenced segment missing on disk: %s" % target_rel)
            continue
        with open(disk_path, "rb") as fh:
            expected = fh.read()
        if payload != expected:
            diag.fail("served bytes differ for %s (%d vs %d)" % (target_rel, len(payload), len(expected)))
            continue
        want_ct = _content_type(target_rel)
        got_ct = hdrs.get("Content-Type", "")
        if got_ct != want_ct:
            diag.fail("content-type for %s: %r != %r" % (target_rel, got_ct, want_ct))
            continue
        # Byte-range sanity on the same object.
        try:
            st2, hdrs2, part = _http_get(target_url, headers={"Range": "bytes=0-99"})
        except Exception as exc:  # noqa: BLE001
            diag.fail("range GET %s raised %r" % (target_url, exc))
            continue
        if st2 != 206 or len(part) != min(100, len(expected)) or part != expected[:len(part)]:
            diag.fail("range GET %s -> status=%d len=%d" % (target_url, st2, len(part)))
            continue
        diag.ok("segment %s (%d bytes, %s, range ok)" % (target_rel, len(payload), got_ct))


class _Diag:
    def __init__(self) -> None:
        self.passed = 0
        self.failed = 0
        self.skipped = 0

    def ok(self, msg: str) -> None:
        self.passed += 1
        print("PASS  %s" % msg)

    def fail(self, msg: str) -> None:
        self.failed += 1
        print("FAIL  %s" % msg, file=sys.stderr)

    def skip(self, msg: str) -> None:
        self.skipped += 1
        print("SKIP  %s" % msg)


def selftest(flv_root: str, hls_root: str) -> int:
    diag = _Diag()
    tmp = tempfile.mkdtemp(prefix="origin-selftest.")
    synthetic = _synthetic_flv()
    test_flv = _write_fixture_file(os.path.join(tmp, "flv"), "synthetic.flv", synthetic)

    server = OriginServer(("127.0.0.1", 0), tmp + "/flv", hls_root, 0)
    port = server.server_address[1]
    origin = "http://127.0.0.1:%d" % port
    thread = threading.Thread(target=server.serve_forever, name="origin-selftest", daemon=True)
    thread.start()
    print("[selftest] serving %s (flv root %s, hls root %s)" % (origin, tmp + "/flv", hls_root))
    try:
        # -- health --------------------------------------------------------
        status, headers, body = _http_get(origin + "/health")
        health = json.loads(body)
        if status == 200 and health.get("ok") is True:
            diag.ok("/healthz -> %s" % body.decode().strip())
        else:
            diag.fail("/healthz -> %d %r" % (status, body))

        # -- full FLV fetch, byte identical --------------------------------
        status, headers, body = _http_get(origin + "/flv/synthetic.flv")
        if status != 200:
            diag.fail("GET /flv/synthetic.flv -> %d" % status)
        elif headers.get("Content-Type") != "video/x-flv":
            diag.fail("flv content-type %r" % headers.get("Content-Type"))
        elif headers.get("Transfer-Encoding") != "chunked":
            diag.fail("flv transfer-encoding %r" % headers.get("Transfer-Encoding"))
        elif body != synthetic:
            diag.fail("flv bytes differ (%d vs %d)" % (len(body), len(synthetic)))
        else:
            diag.ok("GET /flv/synthetic.flv -> 200 chunked, %d bytes identical" % len(body))

        # -- HEAD has Content-Length, no body ------------------------------
        conn = HTTPConnection("127.0.0.1", port, timeout=10)
        conn.request("HEAD", "/flv/synthetic.flv")
        resp = conn.getresponse()
        head_len = resp.getheader("Content-Length")
        conn.close()
        if resp.status == 200 and head_len == str(len(synthetic)):
            diag.ok("HEAD /flv/synthetic.flv -> 200 Content-Length=%s" % head_len)
        else:
            diag.fail("HEAD /flv/synthetic.flv -> %d Content-Length=%r" % (resp.status, head_len))

        # -- throttle: 256 KiB at 512 KiB/s should take ~0.5 s ------------
        slow = OriginServer(("127.0.0.1", 0), tmp + "/flv", hls_root, 512 * 1024)
        slow_port = slow.server_address[1]
        threading.Thread(target=slow.serve_forever, name="origin-selftest-slow", daemon=True).start()
        slow_origin = "http://127.0.0.1:%d" % slow_port
        t0 = time.monotonic()
        _, _, throttled = _http_get(slow_origin + "/flv/synthetic.flv")
        elapsed = time.monotonic() - t0
        slow.shutdown()
        slow.server_close()
        if throttled == synthetic and elapsed >= 0.30:
            diag.ok("throttle: %d bytes in %.2fs at 512KiB/s" % (len(throttled), elapsed))
        else:
            diag.fail("throttle: got %d bytes in %.2fs (expected full payload in >=0.30s)"
                      % (len(throttled), elapsed))

        # -- drop_after: connection aborted before EOF ---------------------
        status, headers, body = None, {}, b""
        try:
            status, headers, body = _http_get(origin + "/flv/synthetic.flv?drop_after=1024")
        except (IncompleteRead, ConnectionError, urllib.error.URLError, OSError) as exc:
            body = getattr(exc, "partial", b"") or b""
            status = None
        if (status is not None and len(body) < len(synthetic)) or (status is None and len(body) < len(synthetic)):
            diag.ok("drop_after=1024 -> truncated (%d < %d bytes)" % (len(body), len(synthetic)))
        else:
            diag.fail("drop_after=1024 did not truncate (status=%r len=%d)" % (status, len(body)))

        # -- drop_after >= size behaves like a normal stream ---------------
        try:
            _, _, full = _http_get(origin + "/flv/synthetic.flv?drop_after=%d" % (len(synthetic) + 10))
            if full == synthetic:
                diag.ok("drop_after > size -> full body")
            else:
                diag.fail("drop_after > size returned %d/%d bytes" % (len(full), len(synthetic)))
        except Exception as exc:  # noqa: BLE001
            diag.fail("drop_after > size raised %r" % exc)

        # -- corrupt=1: same length, bounded region differs, magic intact ---
        _, _, corrupted = _http_get(origin + "/flv/synthetic.flv?corrupt=1")
        region = (CORRUPT_START, CORRUPT_START + CORRUPT_LEN)
        if len(corrupted) != len(synthetic):
            diag.fail("corrupt=1 changed length (%d vs %d)" % (len(corrupted), len(synthetic)))
        elif corrupted[:3] != b"FLV":
            diag.fail("corrupt=1 destroyed FLV magic")
        elif corrupted[:CORRUPT_START] != synthetic[:CORRUPT_START]:
            diag.fail("corrupt=1 modified bytes before bounded region")
        elif corrupted[region[1]:] != synthetic[region[1]:]:
            diag.fail("corrupt=1 modified bytes after bounded region")
        elif corrupted[region[0]:region[1]] == synthetic[region[0]:region[1]]:
            diag.fail("corrupt=1 did not modify bounded region")
        else:
            diag.ok("corrupt=1 -> bounded region [%d,%d) flipped, length/magic intact"
                    % region)

        # -- traversal rejected --------------------------------------------
        try:
            st, _, _ = _http_get(origin + "/flv/../etc/passwd")
            if st == 404:
                diag.ok("path traversal rejected (404)")
            else:
                diag.fail("path traversal returned %d" % st)
        except urllib.error.HTTPError as exc:
            if exc.code == 404:
                diag.ok("path traversal rejected (404)")
            else:
                diag.fail("path traversal returned %d" % exc.code)

        # -- HLS fixtures --------------------------------------------------
        print("[selftest] flv fixture: %s (%d bytes)" % (test_flv, os.path.getsize(test_flv)))
        if os.path.isdir(os.path.join(hls_root, "ts")):
            _fetch_playlist(origin, diag, "ts/playlist.m3u8", hls_root)
        else:
            diag.skip("hls/ts fixture absent (run scripts/gen_hls_fixtures.sh)")
        if os.path.isdir(os.path.join(hls_root, "fmp4")):
            _fetch_playlist(origin, diag, "fmp4/playlist.m3u8", hls_root)
        else:
            diag.skip("hls/fmp4 fixture absent (run scripts/gen_hls_fixtures.sh)")
    finally:
        server.shutdown()
        server.server_close()
        import shutil
        shutil.rmtree(tmp, ignore_errors=True)

    print("[selftest] %d passed, %d failed, %d skipped" % (diag.passed, diag.failed, diag.skipped))
    return 0 if diag.failed == 0 else 1


# ---------------------------------------------------------------------------
# main
# ---------------------------------------------------------------------------

def _parse_args(argv: list[str]) -> argparse.Namespace:
    ap = argparse.ArgumentParser(
        description="Local replay origin for offline E2E tests.",
        formatter_class=argparse.ArgumentDefaultsHelpFormatter,
    )
    ap.add_argument("--host", default=os.environ.get("ORIGIN_HOST", "127.0.0.1"),
                    help="bind address")
    ap.add_argument("--port", type=int, default=int(os.environ.get("ORIGIN_PORT", DEFAULT_PORT)),
                    help="listen port (0 = ephemeral)")
    ap.add_argument("--throttle", type=int,
                    default=int(os.environ.get("THROTTLE_BPS", DEFAULT_THROTTLE_BPS)),
                    help="bytes/second for /flv streaming (0 = unlimited)")
    ap.add_argument("--flv-root", default=os.environ.get("ORIGIN_FLV_ROOT", DEFAULT_FLV_ROOT),
                    help="directory served under /flv/")
    ap.add_argument("--hls-root", default=os.environ.get("ORIGIN_HLS_ROOT", DEFAULT_HLS_ROOT),
                    help="directory served under /hls/")
    ap.add_argument("--selftest", action="store_true",
                    help="start an ephemeral server, run self-checks, exit 0/1")
    return ap.parse_args(argv)


def main(argv: list[str]) -> int:
    args = _parse_args(argv)
    flv_root = os.path.abspath(args.flv_root)
    hls_root = os.path.abspath(args.hls_root)

    if args.selftest:
        return selftest(flv_root, hls_root)

    if not os.path.isdir(flv_root):
        sys.stderr.write("[origin] warning: flv root %s does not exist yet\n" % flv_root)
    if not os.path.isdir(hls_root):
        sys.stderr.write("[origin] warning: hls root %s does not exist yet\n" % hls_root)

    server = OriginServer((args.host, args.port), flv_root, hls_root, args.throttle)
    host, port = server.server_address[:2]
    print("[origin] listening on http://%s:%d  (flv=%s hls=%s throttle=%d B/s)"
          % (host, port, flv_root, hls_root, args.throttle), flush=True)
    try:
        server.serve_forever()
    except KeyboardInterrupt:
        pass
    finally:
        server.server_close()
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
