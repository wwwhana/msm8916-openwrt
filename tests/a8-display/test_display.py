#!/usr/bin/env python3
"""Panel bytecode and status handling, without access to a router or display."""
import hashlib
import importlib.util
import os
from pathlib import Path
import subprocess
import unittest

ROOT = Path(__file__).resolve().parents[2]
PACKAGE = ROOT / "packages/a8-display"
spec = importlib.util.spec_from_file_location("panel", PACKAGE / "gen-panel.py")
panel = importlib.util.module_from_spec(spec)
spec.loader.exec_module(panel)


class PanelTests(unittest.TestCase):
    def test_stock_sequence(self):
        stock = bytes.fromhex((PACKAGE / "files/stock-init.hex").read_text())
        self.assertEqual(len(stock), 140)
        # Anchored to the GC9107 property in this board's stock boot DTB.
        self.assertEqual(hashlib.sha256(stock).hexdigest(),
                         "b31d68670100454a86e2d4540696da6cfb429aff459b02f3a078ec746ccb5299")
        firmware = panel.convert(stock)
        self.assertEqual(firmware[:16], b"MIPI DBI" + bytes(7) + b"\x01")
        self.assertEqual(firmware[-12:], bytes.fromhex("110000017829000001322c00"))
        self.assertIn(bytes.fromhex("2a040000007f2b040000007f"), firmware)

    def test_parameters_and_post_command_delays(self):
        self.assertEqual(panel.convert(bytes.fromhex("0003b00102320129"))[16:],
                         bytes.fromhex("b00201022900000132"))

    def test_malformed_records_are_rejected(self):
        for bad in (b"", b"\x00", b"\x00\x00", b"\x00\x02\x11", b"\x00\x01\x00"):
            with self.subTest(data=bad), self.assertRaises(ValueError):
                panel.convert(bad)


class StatusTests(unittest.TestCase):
    def shell(self, body, cap="", volt="", status=""):
        # Source via an environment variable so set -- can select source-only.
        script = '''set -- --source-only
. "$DISPLAY_SOURCE"
read_value() {
    case "$1" in
        */capacity) printf '%s' "$TEST_CAP" ;;
        */voltage_now) printf '%s' "$TEST_VOLT" ;;
        */status) printf '%s' "$TEST_STATUS" ;;
    esac
}
''' + body
        env = dict(os.environ, DISPLAY_SOURCE=str(PACKAGE / "files/a8-display"),
                   TEST_CAP=cap, TEST_VOLT=volt, TEST_STATUS=status)
        return subprocess.check_output(["sh", "-c", script], env=env)

    def test_missing_battery_is_unknown(self):
        self.assertEqual(self.shell("battery_line"), b"BAT --")
        self.assertEqual(self.shell("battery_line", cap="101", volt="bad"), b"BAT --")

    def test_empty_battery_is_zero_not_unknown(self):
        self.assertEqual(self.shell("battery_line", cap="0", status="Charging"), b"BAT 0% CHG")

    def test_voltage_when_capacity_is_unavailable(self):
        self.assertEqual(self.shell("battery_line", volt="3920000"), b"BAT 3.92V")

    def test_status_fits_console_without_scrolling(self):
        output = self.shell('''
battery_line() { printf 'BAT 50%%'; }
wifi_line() { printf 'WIFI ON'; }
modem_line() { printf 'MODEM A-VERY-LONG-STATE'; }
ubus() { printf '192.168.123.123'; }
jsonfilter() { cat; }
awk() { printf 'UP 0h 01m'; }
show_status
''')
        self.assertTrue(output.startswith(b"\x1b[H"))
        rows = output[3:].split(b"\r\n")
        self.assertEqual(len(rows), 8)
        self.assertTrue(all(len(row) == 16 for row in rows))
        self.assertEqual(rows[4], b"MODEM A-VERY-LON")
        self.assertEqual(rows[5].rstrip(), b"192.168.123.123")


if __name__ == "__main__":
    unittest.main()
