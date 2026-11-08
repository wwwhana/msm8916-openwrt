#!/usr/bin/env python3
"""Pack the Korean/Latin subset of GNU Unifont .hex into fixed glyph records."""
from pathlib import Path
import sys

RANGES = ((0x20, 0xFF), (0x1100, 0x11FF), (0x2000, 0x206F),
          (0x3000, 0x303F), (0x3130, 0x318F), (0xAC00, 0xD7A3),
          (0xFF00, 0xFFEF), (0xFFFD, 0xFFFD))


def pack(source):
    glyphs = {}
    for line in source.splitlines():
        code, data = line.split(':', 1)
        glyphs[int(code, 16)] = bytes.fromhex(data)
    result = bytearray(b'A8F1')
    for first, last in RANGES:
        for code in range(first, last + 1):
            data = glyphs.get(code, glyphs[0xFFFD])
            if len(data) == 16:
                data = b''.join(bytes((row, 0)) for row in data)
            assert len(data) == 32
            result.extend(data)
    return result


if __name__ == '__main__':
    Path(sys.argv[2]).write_bytes(pack(Path(sys.argv[1]).read_text()))
