#!/usr/bin/env python3
"""Reuse the bootloader's Tux in a P6 image for BusyBox fbsplash."""
import re
import sys
from pathlib import Path


def splash(header):
    def array(name):
        body = re.search(r'\b' + name + r'\[[\d* ]*\]\s*=\s*\{([^}]+)\}', header)
        if not body:
            raise ValueError('missing ' + name)
        return [int(n, 0) for n in re.findall(r'0x[0-9a-fA-F]+|\d+', body[1])]

    palette, pixels = array('tux_palette'), array('tux_pixels')
    if len(pixels) != 80 * 80 or max(pixels) >= len(palette):
        raise ValueError('invalid Tux image')
    image = bytearray(b'P6\n128 96\n255\n')
    for y in range(96):
        for x in range(128):
            color = palette[pixels[(y - 8) * 80 + x - 24]] if 24 <= x < 104 and 8 <= y < 88 else 0
            r, g, b = color >> 11, (color >> 5) & 63, color & 31
            image.extend(((r << 3) | (r >> 2), (g << 2) | (g >> 4), (b << 3) | (b >> 2)))
    return bytes(image)


if __name__ == '__main__':
    source, destination = map(Path, sys.argv[1:])
    destination.write_bytes(splash(source.read_text()))
