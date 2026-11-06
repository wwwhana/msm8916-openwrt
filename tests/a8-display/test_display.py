#!/usr/bin/env python3
"""Panel bytecode and status handling, without access to a router or display."""
import hashlib
import importlib.util
import os
from pathlib import Path
import subprocess
import shutil
import sys
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[2]
PACKAGE = ROOT / "packages/a8-display"
spec = importlib.util.spec_from_file_location("panel", PACKAGE / "gen-panel.py")
panel = importlib.util.module_from_spec(spec)
spec.loader.exec_module(panel)


class PanelTests(unittest.TestCase):
    def test_tux_boot_splash_pixels(self):
        with tempfile.TemporaryDirectory() as tmp:
            source = Path(tmp) / 'splash.c'
            binary = Path(tmp) / 'splash'
            source.write_text('''#include <stdio.h>
#include "tux.h"
int main(void) {
    unsigned x, y;
    for (y=0; y<128; ++y) for (x=0; x<128; ++x) {
        unsigned short pixel=a8_splash_pixel(x, y);
        putchar(pixel >> 8); putchar(pixel & 255);
    }
    return 0;
}
''')
            subprocess.run([os.environ.get('CC', 'cc'), '-std=c99', '-Wall', '-Wextra', '-Werror',
                            '-I' + str(ROOT / 'msm89xx/image/lk2nd-a8'),
                            str(source), '-o', str(binary)], check=True)
            pixels = subprocess.check_output([str(binary)])
        self.assertEqual(len(pixels), 128 * 128 * 2)
        rows = [pixels[y*256:(y+1)*256] for y in range(128)]
        for y, row in enumerate(rows):
            if 8 <= y < 88:
                self.assertEqual(row[:48] + row[208:], bytes(96))
            else:
                self.assertEqual(row, bytes(256))
        icon = b''.join(row[48:208] for row in rows[8:88])
        # Anchored to the original Linux v6.12 Tux after RGB565 conversion.
        self.assertEqual(hashlib.sha256(icon).hexdigest(),
                         '25d6861490104827f51bc8b3fa0236dc30adab9bd0b32f6a6d0239b25423e6e9')

    @unittest.skipUnless(sys.platform.startswith('linux'), 'Linux evdev headers required')
    def test_evdev_reader_protocol(self):
        with tempfile.TemporaryDirectory() as tmp:
            binary = str(Path(tmp) / 'reader')
            subprocess.run([os.environ.get('CC', 'cc'), '-std=c99', '-Wall', '-Wextra', '-Werror',
                            str(Path(__file__).with_name('reader.c')), '-o', binary], check=True)
            subprocess.run([binary], check=True)

    def test_shared_button_and_boot_menu_timing(self):
        with tempfile.TemporaryDirectory() as tmp:
            binary = str(Path(tmp) / 'button')
            subprocess.run([os.environ.get('CC', 'cc'), '-std=c99', '-Wall', '-Wextra', '-Werror',
                            '-I' + str(PACKAGE / 'src'),
                            '-I' + str(ROOT / 'msm89xx/image/lk2nd-a8'),
                            str(Path(__file__).with_name('button.c')), '-o', binary], check=True)
            subprocess.run([binary], check=True)

    def test_menu_and_apn_actions(self):
        lua = next((shutil.which(x) for x in ('lua5.1', 'lua', 'lua5.4', 'luajit') if shutil.which(x)), None)
        if not lua:
            self.skipTest('Lua unavailable; run menu.lua with the router Lua 5.1 runtime')
        subprocess.run([lua, str(Path(__file__).with_name('menu.lua')),
                        str(PACKAGE / 'files'), str(ROOT / 'packages/a8-apn/files/model.lua')], check=True)

    def test_controller_lcd_and_led_routing(self):
        lua = next((shutil.which(x) for x in ('lua5.1', 'lua', 'lua5.4', 'luajit') if shutil.which(x)), None)
        if not lua:
            self.skipTest('Lua unavailable')
        subprocess.run([lua, str(Path(__file__).with_name('controller.lua')),
                        str(PACKAGE / 'files')], check=True)

    def test_luci_hardware_options(self):
        node = shutil.which('node')
        if not node:
            self.skipTest('Node unavailable')
        subprocess.run([node, str(Path(__file__).with_name('settings.js')),
                        str(PACKAGE / 'files/a8-device.js')], check=True)

    def test_display_package_can_be_selected(self):
        # OpenWrt propagates the DRM packages' @DISPLAY_SUPPORT dependency
        # to a8-display, removing it entirely when this option is missing.
        config = (ROOT / 'diffconfigs/mifib-v18').read_text().splitlines()
        self.assertIn('CONFIG_DISPLAY_SUPPORT=y', config)

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

    def test_missing_sim_is_a_normal_status(self):
        output = self.shell('''
read_value() { printf '{}'; }
jsonfilter() { printf no-sim; }
mmcli() { exit 91; }
modem_line
''')
        self.assertEqual(output, b"SIM NONE")

    def test_apn_mode_and_missing_profile(self):
        for state, mode, expected in [('ready', 'auto', b'APN AUTO'),
                                      ('ready', 'profile', b'APN PROFILE'),
                                      ('disabled', 'auto', b'APN MANUAL'),
                                      ('no-match', 'auto', b'APN NEED SETUP')]:
            body = '''read_value() { printf '{}'; }
jsonfilter() { case "$2" in '@.state') printf '%s';; '@.mode') printf '%s';; esac; }
apn_line
''' % (state, mode)
            with self.subTest(state=state, mode=mode):
                self.assertEqual(self.shell(body), expected)

    def test_status_fits_console_without_scrolling(self):
        output = self.shell('''
battery_line() { printf 'BAT 50%%'; }
wifi_line() { printf 'WIFI ON'; }
modem_line() { printf 'MODEM A-VERY-LONG-STATE'; }
apn_line() { printf 'APN AUTO'; }
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
        self.assertEqual(rows[7].rstrip(), b"APN AUTO")


if __name__ == "__main__":
    unittest.main()
