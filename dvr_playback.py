#!/usr/bin/env python3
"""List and download recordings from a Kenik/Qualvision DVR.

This talks the port-5801 binary protocol used by the uCloud Cam app
(`ee ee ff ff` framing), not the older Sofia/DVRIP JSON port 34567
(that port accepts TCP here but never replies).

Login is a challenge-response:
  1. client hello  (cmd 0x101) → server returns a base64 token
  2. client login  (cmd 0x105) with
       MD5( token + ":" + sofia_hash(password) )
     sofia_hash is the usual 8-char base62 mapping of MD5(password) pairs.

File search is cmd 0x301. The device handle is uint32 at login+0x13c.
"""

from __future__ import annotations

import argparse
import datetime as dt
import hashlib
import os
import re
import socket
import struct
import subprocess
import sys

MAGIC = b"\xee\xee\xff\xff"
CMD_HELLO = 0x0101
CMD_LOGIN = 0x0105
CMD_KEEPALIVE = 0x0001
CMD_SEARCH = 0x0301
CMD_CLAIM = 0x0311
CMD_READ = 0x0313
CMD_STOP = 0x0314
ALPHABET = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"


def sofia_hash(password: str) -> str:
    digest = hashlib.md5(password.encode("utf-8")).digest()
    return "".join(ALPHABET[(a + b) % 62] for a, b in zip(digest[::2], digest[1::2]))


def login_digest(token: str, password: str) -> str:
    return hashlib.md5(f"{token}:{sofia_hash(password)}".encode()).hexdigest()


def unpack_dt(raw: bytes) -> dt.datetime | None:
    if len(raw) < 8:
        return None
    year, month, day, hour, minute, second = struct.unpack("<HHBBBB", raw[:8])
    try:
        return dt.datetime(year, month, day, hour, minute, second)
    except ValueError:
        return None


def pack_dt(when: dt.datetime) -> bytes:
    return struct.pack(
        "<HHBBBB",
        when.year,
        when.month,
        when.day,
        when.hour,
        when.minute,
        when.second,
    )


def parse_qvfs_name(name: str) -> tuple[dt.datetime | None, dt.datetime | None]:
    """qvfs_…_YY_M_D_h_m_s_YY_M_D_h_m_s → (begin, end)."""
    parts = name.split("_")
    if len(parts) < 13:
        return None, None
    nums = [int(x) for x in parts[-12:]]

    def six(xs):
        return dt.datetime(2000 + xs[0], xs[1], xs[2], xs[3], xs[4], xs[5])

    try:
        return six(nums[:6]), six(nums[6:])
    except ValueError:
        return None, None


class DVR:
    def __init__(self, host: str, port: int = 5801, timeout: float = 15.0):
        self.host = host
        self.port = port
        self.timeout = timeout
        self.sock: socket.socket | None = None
        self.handle = 0

    def connect(self):
        self.sock = socket.create_connection((self.host, self.port), timeout=8)
        self.sock.settimeout(self.timeout)

    def close(self):
        if self.sock:
            self.sock.close()
            self.sock = None

    def __enter__(self):
        self.connect()
        return self

    def __exit__(self, *exc):
        self.close()

    def _send(self, cmd: int, body: bytes):
        pkt = bytearray(0x14 + len(body))
        pkt[0:4] = MAGIC
        struct.pack_into("<I", pkt, 4, len(pkt))
        struct.pack_into("<I", pkt, 8, cmd)
        pkt[0x14:] = body
        self.sock.sendall(pkt)

    def _recv(self) -> bytes:
        hdr = self._recv_exact(8)
        if hdr[:4] != MAGIC:
            raise ConnectionError(f"bad magic {hdr[:4]!r}")
        total = int.from_bytes(hdr[4:8], "little")
        if total < 8:
            raise ConnectionError(f"bad length {total}")
        return hdr + self._recv_exact(total - 8)

    def _recv_exact(self, n: int) -> bytes:
        data = bytearray()
        while len(data) < n:
            chunk = self.sock.recv(n - len(data))
            if not chunk:
                raise ConnectionError("DVR closed the connection")
            data.extend(chunk)
        return bytes(data)

    def login(self, user: str, password: str) -> dict:
        body = bytearray(112 - 0x14)
        body[0:6] = bytes([1, 1, 2, 1, 3, 1])
        body[0x18:0x1C] = (3).to_bytes(4, "little")
        self._send(CMD_HELLO, body)
        hello = self._recv()
        end = hello.find(b"\x00", 0x30)
        token = hello[0x30:end].decode("ascii")

        digest = login_digest(token, password)
        body = bytearray(561 - 0x14)
        raw_user = user.encode("ascii")
        body[0 : len(raw_user)] = raw_user
        body[0x20 : 0x20 + 32] = digest.encode("ascii")
        body[0x218:0x21D] = b"C0023"
        self._send(CMD_LOGIN, body)
        resp = self._recv()
        status = resp[0x0B]
        if status != 0:
            raise PermissionError(f"login rejected, status={status}")
        self.handle = struct.unpack_from("<I", resp, 0x13C)[0]
        end = resp.find(b"\x00", 0x16C)
        device = resp[0x16C:end].decode("ascii", "replace") if end > 0x16C else "?"

        self._send(CMD_KEEPALIVE, b"0" * 32)
        self._recv()
        return {"token": token, "handle": self.handle, "device": device}

    def query_files(self, begin: dt.datetime, end: dt.datetime, channel: int = 0) -> list[str]:
        body = bytearray(68 - 0x14)
        struct.pack_into("<I", body, 0, self.handle)
        struct.pack_into("<I", body, 8, channel)
        body[0x14:0x1C] = pack_dt(begin)
        body[0x20:0x28] = pack_dt(end)
        self._send(CMD_SEARCH, body)

        # The DVR may push notify (cmd 0x505) before the search result.
        while True:
            resp = self._recv()
            cmd = int.from_bytes(resp[8:12], "little")
            if cmd & 0xFFFF == CMD_SEARCH:
                break
        status = resp[0x0B]
        if status != 0:
            raise RuntimeError(f"search failed, status={status}")
        names = [m.decode("ascii") for m in re.findall(rb"qvfs_[0-9_]+", resp)]
        return names

    def download(self, name: str, out_path: str, stop_after: dt.datetime | None = None) -> int:
        """Claim `name` and write its H.264 (Annex B) to `out_path`.

        Playback uses a second TCP connection. The login socket must stay open:
        the read commands carry the handle from the login response.
        Video frames start at byte 0x40 with a 00 00 00 01 start code.
        The 544-byte replies are not video (a d0/d1 pattern) and are skipped.
        """
        media = DVR(self.host, self.port, self.timeout)
        media.connect()
        written = 0
        try:
            body = bytearray(384 - 0x14)
            struct.pack_into("<I", body, 0, self.handle)
            raw = name.encode("ascii")
            body[4 : 4 + len(raw)] = raw
            media._send(CMD_CLAIM, body)
            claim = media._recv_cmd(CMD_CLAIM)
            if claim[0x0B] != 0:
                raise RuntimeError(f"claim rejected, status={claim[0x0B]}")
            # The DVR assigns a per-file tag here. Reads that send the old
            # capture value come back with status 2 and no video.
            tag = struct.unpack_from("<I", claim, 0xA0)[0]
            started = False

            with open(out_path, "wb") as out:
                for seq in range(1, 8000):
                    body = bytearray(64 - 0x14)
                    struct.pack_into("<I", body, 0, self.handle)
                    struct.pack_into("<I", body, 4, tag)
                    struct.pack_into("<I", body, 8, seq)
                    struct.pack_into("<I", body, 12, 0x100)
                    try:
                        media._send(CMD_READ, body)
                        msg = media._recv_cmd(CMD_READ)
                    except (ConnectionError, TimeoutError, socket.timeout):
                        # The DVR closes the playback socket when the file ends.
                        break
                    if msg[0x0B] != 0:
                        break
                    if len(msg) < 0x44 or msg[0x40:0x44] != b"\x00\x00\x00\x01":
                        continue
                    plen = struct.unpack_from("<I", msg, 0x3C)[0]
                    payload = msg[0x40 : 0x40 + plen]
                    if len(payload) < 5:
                        continue
                    if not started:
                        sps = payload.find(b"\x00\x00\x00\x01\x67")
                        if sps < 0:
                            continue
                        payload = payload[sps:]
                        started = True
                    out.write(payload)
                    written += len(payload)
                    if stop_after is not None and len(msg) >= 0x38:
                        when = unpack_dt(msg[0x30:0x38])
                        if when is not None and when > stop_after:
                            break
            stop = bytearray(32 - 0x14)
            struct.pack_into("<I", stop, 0, self.handle)
            struct.pack_into("<I", stop, 4, tag)
            struct.pack_into("<I", stop, 8, 1)
            try:
                media._send(CMD_STOP, stop)
                media._recv_cmd(CMD_STOP)
            except (ConnectionError, TimeoutError, socket.timeout):
                pass
        finally:
            media.close()
        if written == 0:
            raise RuntimeError(f"no video frames in {name}")
        return written

    def _recv_cmd(self, cmd: int, limit: int = 8) -> bytes:
        for _ in range(limit):
            msg = self._recv()
            got = int.from_bytes(msg[8:12], "little") & 0xFFFF
            if got == cmd:
                return msg
        raise TimeoutError(f"no response for cmd {cmd:#x}")


def _load_env():
    env = {}
    path = os.path.join(os.path.dirname(__file__) or ".", ".env")
    if os.path.exists(path):
        for line in open(path):
            line = line.strip()
            if not line or line.startswith("#") or "=" not in line:
                continue
            k, v = line.split("=", 1)
            env[k.strip()] = v.strip()
    return env


def main():
    env = _load_env()
    ap = argparse.ArgumentParser(description="List Kenik DVR recordings over port 5801")
    ap.add_argument("--host", default=env.get("DVR_ADDRESS", "192.168.1.3"))
    ap.add_argument("--user", default=env.get("DVR_USER", "admin"))
    ap.add_argument("--password", default=env.get("DVR_PASSWORD", ""))
    ap.add_argument("--port", type=int, default=5801)
    ap.add_argument(
        "--channel",
        type=int,
        default=1,
        help="search mode/channel (the uCloud Cam app uses 1; 0 ignores the date range)",
    )
    ap.add_argument("--begin", help="YYYY-MM-DD or YYYY-MM-DDTHH:MM:SS")
    ap.add_argument("--end", help="YYYY-MM-DD or YYYY-MM-DDTHH:MM:SS")
    ap.add_argument("--out", help="download the first listed clip to this .h264 or .mp4 path")
    ap.add_argument("--file", help="download this qvfs name instead of the first search hit")
    args = ap.parse_args()
    if not args.password:
        sys.exit("missing --password (or DVR_PASSWORD in .env)")

    def parse_when(s: str | None, fallback: dt.datetime, end_of_day: bool) -> dt.datetime:
        if not s:
            return fallback
        if "T" in s:
            return dt.datetime.fromisoformat(s)
        day = dt.date.fromisoformat(s)
        t = dt.time(23, 59, 59) if end_of_day else dt.time(0, 0, 0)
        return dt.datetime.combine(day, t)

    today = dt.date.today()
    begin = parse_when(args.begin, dt.datetime.combine(today, dt.time.min), False)
    end = parse_when(args.end, dt.datetime.combine(today, dt.time(23, 59, 59)), True)

    with DVR(args.host, args.port) as dvr:
        info = dvr.login(args.user, args.password)
        print(
            f"logged in  device={info['device']}  handle=0x{info['handle']:08x}",
            file=sys.stderr,
        )
        files = dvr.query_files(begin, end, args.channel)
        if args.out:
            name = args.file or (files[0] if files else None)
            if not name:
                sys.exit("no recording in that range")
            _, clip_end = parse_qvfs_name(name)
            n = dvr.download(name, args.out if args.out.endswith(".h264") else args.out + ".h264", clip_end)
            h264 = args.out if args.out.endswith(".h264") else args.out + ".h264"
            print(f"wrote {n} bytes of H.264 from {name} to {h264}", file=sys.stderr)
            if args.out.endswith(".mp4"):
                subprocess.run(
                    ["ffmpeg", "-y", "-v", "error", "-i", h264, "-c", "copy", args.out],
                    check=True,
                )
                print(f"remuxed {args.out}", file=sys.stderr)
            return
        print(f"{len(files)} file(s)  {begin} → {end}  ch={args.channel}")
        for name in files:
            b, e = parse_qvfs_name(name)
            if b and e:
                print(f"  {b} → {e}  {name}")
            else:
                print(f"  {name}")


if __name__ == "__main__":
    main()
