#!/usr/bin/env python3
"""Convert stock Qualcomm SPI records to Linux panel-mipi-dbi firmware."""
import pathlib
import sys


def convert(data):
    output = bytearray(b"MIPI DBI" + b"\0" * 7 + b"\x01")
    offset = 0
    if not data:
        raise ValueError("empty panel initialization")
    while offset < len(data):
        if offset + 2 > len(data):
            raise ValueError("truncated stock command header")
        wait, size = data[offset:offset + 2]
        offset += 2
        if not size or offset + size > len(data):
            raise ValueError("invalid stock command length")
        command, *parameters = data[offset:offset + size]
        if command == 0:
            raise ValueError("NOP conflicts with the firmware delay encoding")
        output.extend((command, size - 1, *parameters))
        if wait:
            output.extend((0, 1, wait))
        offset += size
    return bytes(output)


if __name__ == "__main__":
    if len(sys.argv) != 3:
        sys.exit("usage: gen-panel.py stock-init.hex output.bin")
    source, destination = map(pathlib.Path, sys.argv[1:])
    destination.write_bytes(convert(bytes.fromhex(source.read_text())))
