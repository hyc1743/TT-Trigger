#!/usr/bin/env python3
"""Generate dependency-free TT Trigger PNG icons."""

import binascii
import struct
import zlib
from pathlib import Path


def chunk(kind: bytes, data: bytes) -> bytes:
    payload = kind + data
    return struct.pack(">I", len(data)) + payload + struct.pack(">I", binascii.crc32(payload) & 0xFFFFFFFF)


def make_icon(size: int) -> bytes:
    blue = (0x00, 0x2F, 0xA7, 0xFF)
    white = (0xFF, 0xFF, 0xFF, 0xFF)
    pixels = [[blue for _ in range(size)] for _ in range(size)]

    margin = max(2, size // 7)
    stroke = max(2, size // 8)
    gap = max(1, size // 12)
    total_width = size - 2 * margin
    letter_width = (total_width - gap) // 2
    top_y = margin
    bar_height = stroke
    stem_y_end = size - margin

    for letter in range(2):
        x0 = margin + letter * (letter_width + gap)
        for y in range(top_y, min(size, top_y + bar_height)):
            for x in range(x0, min(size, x0 + letter_width)):
                pixels[y][x] = white
        stem_x = x0 + (letter_width - stroke) // 2
        for y in range(top_y, stem_y_end):
            for x in range(stem_x, min(size, stem_x + stroke)):
                pixels[y][x] = white

    raw = b"".join(b"\x00" + b"".join(bytes(pixel) for pixel in row) for row in pixels)
    header = b"\x89PNG\r\n\x1a\n"
    ihdr = struct.pack(">IIBBBBB", size, size, 8, 6, 0, 0, 0)
    return header + chunk(b"IHDR", ihdr) + chunk(b"IDAT", zlib.compress(raw, 9)) + chunk(b"IEND", b"")


output = Path(__file__).resolve().parents[1] / "extension" / "icons"
output.mkdir(parents=True, exist_ok=True)
for icon_size in (16, 32, 48, 128):
    (output / f"icon-{icon_size}.png").write_bytes(make_icon(icon_size))
