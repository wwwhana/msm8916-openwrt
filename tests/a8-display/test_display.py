#!/usr/bin/env python3
"""Panel bytecode and status handling, without access to a router or display."""
import hashlib
import importlib.util
import json
import os
from pathlib import Path
import subprocess
import shutil
import sqlite3
import sys
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[2]
PACKAGE = ROOT / "packages/a8-display"
spec = importlib.util.spec_from_file_location("panel", PACKAGE / "gen-panel.py")
panel = importlib.util.module_from_spec(spec)
spec.loader.exec_module(panel)


class PanelTests(unittest.TestCase):
    def test_rescue_reboots_do_not_count_as_boot_failures(self):
        patch = (ROOT / 'msm89xx/image/lk2nd-patches/001-lk2nd-recovery-key-and-boot-failover.patch').read_text()
        chunk = patch.split('@@')[2::2][-1]
        flow = '\n'.join(line[1:] for line in chunk.splitlines()
                         if line.startswith((' ', '+')))
        # Run the real call site, including the forced-Fastboot decision.
        flow = flow[flow.index('#if LK2ND_FORCE_FASTBOOT'):flow.index('normal_boot:')]
        harness = r'''
#include <assert.h>
#include <stdbool.h>
#define BOOT_FAILOVER_COUNT 3
#define dprintf(...) ((void)0)
static bool boot_into_fastboot;
static int boot_into_recovery;
static unsigned failures;
static int boot_failover_check(void) { return ++failures >= BOOT_FAILOVER_COUNT; }
static void boot(int reason) {
    boot_into_recovery = reason == 1;
    boot_into_fastboot = reason == 2;
FLOW
}
int main(void) {
    /* Intentional rescue transitions must not turn two failures into three. */
    for (int reason = 1; reason <= 2; reason++) {
        failures = 2; boot(reason); assert(failures == 2);
    }
    failures = 0; boot(0);
    assert(failures == !LK2ND_FORCE_FASTBOOT);
    assert(!boot_into_recovery);
    failures = 2; boot(0);
    assert(failures == (LK2ND_FORCE_FASTBOOT ? 2 : 3));
    assert(boot_into_recovery == !LK2ND_FORCE_FASTBOOT);
    return 0;
}
'''.replace('FLOW', flow)
        with tempfile.TemporaryDirectory() as tmp:
            c, binary = Path(tmp) / 'failover.c', Path(tmp) / 'failover'
            c.write_text(harness)
            for forced in (0, 1):
                subprocess.run([os.environ.get('CC', 'cc'), '-std=c99', '-Wall', '-Wextra', '-Werror',
                                f'-DLK2ND_FORCE_FASTBOOT={forced}', str(c), '-o', str(binary)], check=True)
                subprocess.run([str(binary)], check=True)

    def test_complete_boot_lcd_driver(self):
        source = (ROOT / 'msm89xx/image/lk2nd-a8/menu.c').read_text()
        source = '\n'.join(line for line in source.splitlines() if not line.startswith('#include '))
        harness = Path(__file__).with_name('boot-panel.c').read_text()
        marker = '/* The production driver is inserted here by test_display.py. */'
        stock = bytes.fromhex((PACKAGE / 'files/stock-init.hex').read_text())
        with tempfile.TemporaryDirectory() as tmp:
            tmp = Path(tmp)
            (tmp / 'stock-init.h').write_text('static const unsigned char stock_init[] = {' +
                ','.join(str(value) for value in stock) + '};\n')
            c, binary = tmp / 'panel.c', tmp / 'panel'
            c.write_text(harness.replace(marker, source))
            for diagnostic in (0, 1):
                subprocess.run([os.environ.get('CC', 'cc'), '-std=c99', '-Wall', '-Wextra', '-Werror',
                                '-fsanitize=address,undefined', '-fno-omit-frame-pointer',
                                f'-DA8_LCD_RAM_TEST={diagnostic}',
                                '-I' + str(ROOT / 'msm89xx/image/lk2nd-a8'),
                                '-I' + str(PACKAGE / 'src'), str(c), '-o', str(binary)], check=True)
                for scenario in range(10):
                    with self.subTest(diagnostic=diagnostic, scenario=scenario):
                        subprocess.run([str(binary), str(scenario)], check=True, timeout=10)

    def test_rescue_boot_does_not_wait_for_lcd(self):
        patch = (ROOT / 'msm89xx/image/lk2nd-patches/003-a8-one-button-menu.patch').read_text()
        chunks = patch.split('@@')[2::2]
        additions = ['\n'.join(line[1:] for line in chunk.splitlines()
                     if line.startswith('+') and not line.startswith('+++')) for chunk in chunks]
        normal, fastboot = additions[-2:]
        harness = r'''
#include <assert.h>
#include <stdbool.h>
#include "boot-menu.h"
#define A8_ONE_BUTTON_MENU 1
#define EMERGENCY_DLOAD 9
static int boot_into_recovery, boot_into_fastboot, usb, menus, splashes;
static enum a8_boot_choice selection;
enum a8_boot_choice a8_boot_menu(void) { menus++; return selection; }
int a8_boot_splash(const char *s) { (void)s; assert(usb); splashes++; return 0; }
void reboot_device(int reason) { assert(reason==EMERGENCY_DLOAD); }
static void boot(void) {
NORMAL
    if (!boot_into_fastboot) return;
    usb=1;
FASTBOOT
}
int main(void) {
    boot_into_recovery=1; boot(); assert(!menus && !splashes && usb==A8_LCD_RAM_TEST);
    boot_into_recovery=0; boot_into_fastboot=1; boot();
    assert(!menus && splashes==!A8_LCD_RAM_TEST && usb);
    boot_into_fastboot=usb=menus=splashes=0; selection=A8_RECOVERY; boot();
    assert(menus==!A8_LCD_RAM_TEST && boot_into_recovery==!A8_LCD_RAM_TEST &&
           !splashes && usb==A8_LCD_RAM_TEST);
    boot_into_recovery=usb=menus=splashes=0; selection=A8_FASTBOOT; boot();
    assert(menus==!A8_LCD_RAM_TEST && boot_into_fastboot && splashes==!A8_LCD_RAM_TEST && usb);
    return 0;
}
'''.replace('NORMAL', normal).replace('FASTBOOT\n', fastboot+'\n')
        with tempfile.TemporaryDirectory() as tmp:
            c, binary = Path(tmp) / 'rescue.c', Path(tmp) / 'rescue'
            c.write_text(harness)
            for diagnostic in (0, 1):
                subprocess.run([os.environ.get('CC', 'cc'), '-std=c99', '-Wall', '-Wextra', '-Werror',
                                f'-DA8_LCD_RAM_TEST={diagnostic}',
                                '-I' + str(ROOT / 'msm89xx/image/lk2nd-a8'),
                                '-I' + str(PACKAGE / 'src'), str(c), '-o', str(binary)], check=True)
                subprocess.run([str(binary)], check=True)

    def test_unmapped_pmic_channel_is_rejected(self):
        harness = r'''
#include <assert.h>
#include <stdint.h>
#define SPMI_CORE_V2 1
#define PMIC_ARB_REG_CHLN(n) (0x1000+4*(n))
#define CRITICAL 0
#define dprintf(...) ((void)0)
static unsigned reads;
static uint32_t mapping;
static uint32_t readl(uint32_t address) {
    assert(address>=0x1000 && address<0x1200); reads++; return mapping;
}
static void udelay(unsigned us) { (void)us; }
#include "pmic-wait.h"
int main(void) {
    mapping=0x15000;
    assert(a8_pmic_channel_matches(127, 128, 1, 0x50));
    assert(reads==1);
    assert(!a8_pmic_channel_matches(0xff, 128, 1, 0x50) && reads==1);
    /* A stale in-range channel must not redirect an L17 write to PON. */
    mapping=0x00800;
    assert(!a8_pmic_channel_matches(0, 128, 1, 0x50));
    mapping=0x0a300;
    assert(a8_pmic_channel_matches(3, 128, 0, 0xa3));
    return 0;
}
'''
        with tempfile.TemporaryDirectory() as tmp:
            c, binary = Path(tmp) / 'channel.c', Path(tmp) / 'channel'
            c.write_text(harness)
            subprocess.run([os.environ.get('CC', 'cc'), '-std=c99', '-Wall', '-Wextra', '-Werror',
                            '-I' + str(ROOT / 'msm89xx/image/lk2nd-a8'),
                            str(c), '-o', str(binary)], check=True)
            subprocess.run([str(binary)], check=True)

    def test_pmic_transactions_finish_or_time_out(self):
        harness = r'''
#include <assert.h>
#include <stdint.h>
#define CRITICAL 0
#define dprintf(...) ((void)0)
static unsigned polls, delays, ready_at;
static uint32_t readl(uint32_t address) {
    assert(address == 0x1234);
    return ++polls >= ready_at ? 1 : 0;
}
static void udelay(unsigned us) { assert(us == 1); delays++; }
#include "pmic-wait.h"
int main(void) {
    uint32_t status;
    ready_at=1;
    assert(a8_pmic_wait_status(0x1234, &status) && status == 1 && delays == 0);
    polls=delays=0; ready_at=12;
    assert(a8_pmic_wait_status(0x1234, &status) && status == 1 && delays == 11);
    polls=delays=0; ready_at=10001;
    assert(!a8_pmic_wait_status(0x1234, &status) && status == 0);
    assert(polls == 10000 && delays == 10000);
    return 0;
}
'''
        with tempfile.TemporaryDirectory() as tmp:
            c, binary = Path(tmp) / 'pmic.c', Path(tmp) / 'pmic'
            c.write_text(harness)
            subprocess.run([os.environ.get('CC', 'cc'), '-std=c99', '-Wall', '-Wextra', '-Werror',
                            '-I' + str(ROOT / 'msm89xx/image/lk2nd-a8'),
                            str(c), '-o', str(binary)], check=True)
            subprocess.run([str(binary)], check=True)

    def test_recovery_splash_reuses_the_bootloader_pixels(self):
        spec = importlib.util.spec_from_file_location('recovery_splash', ROOT / 'msm89xx/recovery/gen-splash.py')
        module = importlib.util.module_from_spec(spec)
        spec.loader.exec_module(module)
        image = module.splash((ROOT / 'msm89xx/image/lk2nd-a8/tux.h').read_text())
        header, pixels = image.split(b'\n255\n', 1)
        self.assertEqual(header, b'P6\n128 96')
        self.assertEqual(len(pixels), 128 * 96 * 3)
        # Re-encode the centered 80x80 image to the bootloader's RGB565 format.
        rgb565 = bytearray()
        for y in range(8, 88):
            for x in range(24, 104):
                r, g, b = pixels[(y * 128 + x) * 3:(y * 128 + x) * 3 + 3]
                rgb565.extend(((r >> 3) << 11 | (g >> 2) << 5 | b >> 3).to_bytes(2, 'big'))
        self.assertEqual(hashlib.sha256(rgb565).hexdigest(),
                         '25d6861490104827f51bc8b3fa0236dc30adab9bd0b32f6a6d0239b25423e6e9')

    def test_recovery_stages_each_boards_panel_and_display_modules(self):
        source = (ROOT / 'build-recovery.sh').read_text()
        functions = source[source.index('install_modules() {'):source.index('# Pack:')]
        assignments = '\n'.join(line for line in source.splitlines()
                                if line.startswith(('RECOVERY_MODULES=', 'RECOVERY_LCD_MODULES=')))
        for board, init, firmware in (
                ('mifib-v18', 'stock-init.hex', 'mifib-v18,gc9107.bin'),
                ('jz08c-v50', 'jz08c-v50-stock-init.hex', 'jz08c-v50,st7735s.bin'),
                ('jz02', None, None)):
            with self.subTest(board=board), tempfile.TemporaryDirectory() as tmp:
                root = Path(tmp)
                irfs, modules = root / 'irfs', root / 'modules'
                for path in (irfs / 'lib/firmware', irfs / 'usr/sbin', irfs / 'etc', modules):
                    path.mkdir(parents=True)
                shutil.copyfile(ROOT / 'msm89xx/recovery/irfs/usr/sbin/recovery-display',
                                irfs / 'usr/sbin/recovery-display')
                for name in ('mdt_loader', 'qcom_common', 'qcom_pil_info', 'qcom_wcnss_pil',
                             'compat', 'cfg80211', 'mac80211', 'wcn36xx', 'gpio_backlight',
                             'drm_panel_orientation_quirks', 'drm', 'drm_kms_helper',
                             'drm_dma_helper', 'drm_mipi_dbi', 'panel-mipi-dbi'):
                    (modules / (name + '.ko')).write_bytes(name.encode())
                script = ('set -e\n' + assignments + '\n'
                          'msg() { :; }\ndie() { echo "$*" >&2; exit 1; }\n'
                          'module_dir() { echo "$TEST_MODULES"; }\n'
                          'module_version() { echo test; }\n' + functions +
                          '\ninstall_modules "$BOARD"\ninstall_display "$BOARD"\n')
                subprocess.run(['bash', '-c', script], check=True,
                               env=dict(os.environ, BOARD=board, IRFS=str(irfs),
                                        REPO_DIR=str(ROOT), RECOVERY_DIR=str(ROOT / 'msm89xx/recovery'),
                                        TEST_MODULES=str(modules)))
                packed = irfs / 'lib/modules/test/panel-mipi-dbi.ko'
                self.assertEqual(packed.exists(), firmware is not None)
                if firmware:
                    self.assertEqual((irfs / 'lib/firmware' / firmware).read_bytes(),
                                     panel.convert(bytes.fromhex((PACKAGE / 'files' / init).read_text())))
                    self.assertTrue((irfs / 'etc/recovery.ppm').exists())
                else:
                    self.assertEqual(list((irfs / 'lib/firmware').iterdir()), [])

    def test_private_recovery_blobs_cannot_be_reused_for_all_boards(self):
        with tempfile.TemporaryDirectory() as tmp:
            result = subprocess.run(['bash', str(ROOT / 'build-recovery.sh'), 'all'],
                                    capture_output=True, text=True,
                                    env=dict(os.environ, RECOVERY_BLOBS_DIR=tmp))
            self.assertNotEqual(result.returncode, 0)
            self.assertIn('belongs to one board', result.stdout + result.stderr)

    def test_boot_splash_builds_from_the_installed_openwrt_package(self):
        recipe = next(line for line in (PACKAGE / 'Makefile').read_text().splitlines()
                      if line.startswith('\t') and 'gen-splash.py' in line)
        with tempfile.TemporaryDirectory() as tmp:
            top = Path(tmp) / 'openwrt'
            package = top / 'package/msm8916/a8-display'
            bsp = top / 'target/linux/msm89xx'
            build = top / 'build_dir/a8-display'
            package.mkdir(parents=True)
            build.mkdir(parents=True)
            for path in ('recovery/gen-splash.py', 'image/lk2nd-a8/tux.h'):
                destination = bsp / path
                destination.parent.mkdir(parents=True, exist_ok=True)
                shutil.copyfile(ROOT / 'msm89xx' / path, destination)
            makefile = package / 'splash.mk'
            makefile.write_text(f'TOPDIR:={top}\nPKG_BUILD_DIR:={build}\nall:\n{recipe}\n')
            result = subprocess.run(['make', '-f', str(makefile)], cwd=package,
                                    capture_output=True, text=True)
            self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
            image = (build / 'boot.ppm').read_bytes()
            self.assertTrue(image.startswith(b'P6\n128 96\n255\n'))
            self.assertEqual(len(image), len(b'P6\n128 96\n255\n') + 128 * 96 * 3)

    def test_linux_boot_splash_uses_recovery_renderer_before_menu(self):
        source = (PACKAGE / 'files/a8-display').read_text()
        function = source[source.index('show_boot_splash()'):source.index('read_value()')]
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            for path in ('var/run', 'usr/share/a8-display', 'sys/class/graphics/fb0', 'dev', 'proc'):
                (root / path).mkdir(parents=True)
            (root / 'proc/uptime').write_text('15.25 20.00\n')
            image = root / 'usr/share/a8-display/boot.ppm'
            image.write_bytes(b'P6\n128 96\n255\n' + bytes(128 * 96 * 3))
            fb = root / 'sys/class/graphics/fb0'
            for key, value in {'name': 'panel-mipi-dbid', 'bits_per_pixel': '16',
                               'stride': '256', 'virtual_size': '128,128'}.items():
                (fb / key).write_text(value + '\n')
            for path in ('/var/run', '/usr/share/a8-display', '/sys/class/graphics', '/dev/', '/proc/uptime'):
                function = function.replace(path, str(root) + path)
            tty, call, marker = root / 'dev/tty1', root / 'renderer-call', root / 'var/run/a8-display.boot'
            stub = 'fbsplash() { printf "%s\\n" "$@" > "$RENDERER_CALL"; return "$RENDERER_RESULT"; }\n'
            def run():
                subprocess.run(['sh', '-c', stub + function + '\nshow_boot_splash'], check=True,
                               env=dict(os.environ, RENDERER_CALL=str(call), RENDERER_RESULT='0'))
            run()
            self.assertEqual(call.read_text().splitlines(), ['-d', str(root / 'dev/fb0'), '-s', str(image)])
            self.assertIn(b'\x1b[?25l', tty.read_bytes())
            self.assertTrue(tty.read_bytes().endswith(b'\x1b[7;1HOpenWrt\x1b[8;1HStarting...'))
            self.assertEqual(marker.read_text(), '15.25\n')
            for condition in ('menu', 'format', 'missing-image'):
                with self.subTest(condition=condition):
                    call.unlink(missing_ok=True)
                    marker.unlink(missing_ok=True)
                    tty.write_bytes(b'menu')
                    if condition == 'menu':
                        (root / 'var/run/a8-device.json').write_text('{}')
                    elif condition == 'format':
                        (fb / 'bits_per_pixel').write_text('32\n')
                    else:
                        image.unlink()
                    run()
                    self.assertFalse(call.exists())
                    self.assertFalse(marker.exists())
                    self.assertEqual(tty.read_bytes(), b'menu')
                    (root / 'var/run/a8-device.json').unlink(missing_ok=True)
                    (fb / 'bits_per_pixel').write_text('16\n')
            image.write_bytes(b'P6\n128 96\n255\n' + bytes(128 * 96 * 3))
            subprocess.run(['sh', '-c', stub + function + '\nshow_boot_splash'], check=True,
                           env=dict(os.environ, RENDERER_CALL=str(call), RENDERER_RESULT='1'))
            self.assertTrue(call.exists())
            self.assertFalse(marker.exists())

    def test_bluetooth_pairing(self):
        lua = next((shutil.which(x) for x in ('lua5.1', 'lua', 'lua5.4', 'luajit') if shutil.which(x)), None)
        if not lua:
            self.skipTest('Lua interpreter unavailable')
        subprocess.run([lua, str(Path(__file__).with_name('pairing.lua')),
                        str(PACKAGE / 'files')], check=True)

    def test_connection_menu_backend(self):
        lua = next((shutil.which(x) for x in ('lua5.1', 'lua', 'lua5.4', 'luajit') if shutil.which(x)), None)
        if not lua:
            self.skipTest('Lua interpreter unavailable')
        subprocess.run([lua, str(Path(__file__).with_name('connections.lua')),
                        str(PACKAGE / 'files')], check=True)

    def test_actual_boot_menu_held_and_bouncing_button(self):
        source = (ROOT / 'msm89xx/image/lk2nd-a8/menu.c').read_text()
        sample = source[source.index('static void sample_button('):source.index('static void set_pin(')]
        menu = source[source.index('enum a8_boot_choice a8_boot_menu(void)'):]
        harness = r'''
#include <assert.h>
#include <stdlib.h>
#include "boot-menu.h"
#define KEY_POWER 1
static uint32_t clock_ms;
static int scenario;
static struct a8_button button;
static enum a8_button_event queued;
static uint32_t current_time(void) { return clock_ms; }
static int lk2nd_keys_pressed(int key) {
    (void)key;
    if (scenario == 1) return 1; /* Held throughout startup. */
    if (scenario == 2) return clock_ms >= 100 && clock_ms < 120; /* 20 ms bounce. */
    if (scenario == 3) return (clock_ms >= 100 && clock_ms < 200) ||
                             (clock_ms >= 500 && clock_ms < 1600);
    if (scenario == 4) return clock_ms >= 100; /* Selecting key never releases. */
    return 0;
}
static void thread_sleep(unsigned ms) {
    clock_ms += ms;
    assert(clock_ms <= 7500); /* A stuck menu fails immediately, without waiting. */
}
static void draw_menu(const struct a8_boot_state *s, uint32_t now) { (void)s; (void)now; }
static void draw_row(unsigned row, const char *text) { (void)row; (void)text; }
int a8_boot_splash(const char *mode) { (void)mode; return 1; }
'''
        checks = r'''
int main(int argc, char **argv) {
    assert(argc == 2);
    scenario = atoi(argv[1]);
    assert(a8_boot_menu() == (scenario == 3 ? A8_RECOVERY : A8_BOOT));
    return 0;
}
'''
        with tempfile.TemporaryDirectory() as tmp:
            c, binary = Path(tmp) / 'menu.c', Path(tmp) / 'menu'
            c.write_text(harness + sample + menu + checks)
            subprocess.run([os.environ.get('CC', 'cc'), '-std=c99', '-Wall', '-Wextra', '-Werror',
                            '-I' + str(ROOT / 'msm89xx/image/lk2nd-a8'),
                            '-I' + str(PACKAGE / 'src'), str(c), '-o', str(binary)], check=True)
            for scenario in range(5):
                with self.subTest(scenario=scenario):
                    subprocess.run([str(binary), str(scenario)], check=True, capture_output=True)

    def test_actual_boot_panel_power_sequence(self):
        source = (ROOT / 'msm89xx/image/lk2nd-a8/menu.c').read_text()
        helpers = source[source.index('static int read_pmic('):source.index('static void draw_row(')]
        defines = '\n'.join(line for line in source.splitlines() if line.startswith('#define '))
        harness = r'''
#include <assert.h>
#include <stdint.h>
#include <string.h>
#include "stock-init.h"
#define CRITICAL 0
#define dprintf(...) ((void)0)
#define mdelay(ms) ((void)(ms))
#define lcd_progress(stage) ((void)(stage))
#define set_pin(pin, value) ((void)(pin), (void)(value))
#define output(pin, drive, value) ((void)(pin), (void)(drive), (void)(value))
#define command(cmd, data, size) ((void)(cmd), (void)(data), (void)(size))
enum {GPIO_2MA, GPIO_8MA, GPIO_12MA};
static unsigned char regs[2][65536];
static unsigned writes;
static char previous[8][17];
static int spmi_reg_read(unsigned sid, unsigned reg, unsigned char *v, int priority)
{ (void)priority; *v=regs[sid][reg]; return 0; }
static int spmi_reg_write(unsigned sid, unsigned reg, unsigned char *v, int priority)
{ (void)priority; assert(sid==0);
  regs[sid][reg]=*v; writes++; return 0; }
'''
        checks = r'''
int main(void) {
    /* Actual SBL readings: L6 range 0, selector 4 = 1.8 V. */
    regs[1][0x4541]=4; regs[1][0x4546]=0xa0;
    regs[1][0x5041]=88; regs[1][0x5045]=0x55; regs[1][0x5046]=0x80;
    assert(panel_init());
    assert(regs[1][0x4540] == 0 && regs[1][0x4541] == 4);
    assert(regs[1][0x4546] == 0xa0);
    /* Actual stock L17 setting: range 0, selector 88 = 2.85 V. */
    assert(regs[1][0x5040] == 0 && regs[1][0x5041] == 88);
    assert(regs[1][0x5045] == 0x55 && regs[1][0x5046] == 0x80);
    assert(regs[0][0xa340] == 0x60 && regs[0][0xa34c] == 7);
    assert(regs[0][0xa346] == 0); /* Backlight waits for the first frame. */
    regs[1][0x4541]=5; writes=0;
    assert(!panel_init() && writes == 0); /* Shared voltage stays untouched. */
    regs[1][0x4540]=2; regs[1][0x4541]=4;
    assert(!panel_init() && writes == 0);
    regs[1][0x4540]=0; regs[1][0x4546]=0;
    assert(!panel_init() && writes == 0); /* Never enable a shared rail. */
    regs[1][0x4546]=0xa0; regs[1][0x5046]=0;
    assert(!panel_init() && writes == 0); /* Disabled L17 cannot strand USB. */
    regs[1][0x5046]=0x80; regs[1][0x5041]=87;
    assert(!panel_init() && writes == 0); /* Never force an L17 voltage. */
    return 0;
}
'''
        with tempfile.TemporaryDirectory() as tmp:
            c = Path(tmp) / 'panel.c'
            binary = Path(tmp) / 'panel'
            stock = bytes.fromhex((PACKAGE / 'files/stock-init.hex').read_text())
            (Path(tmp) / 'stock-init.h').write_text('static const unsigned char stock_init[] = {' +
                ','.join(str(value) for value in stock) + '};\n')
            c.write_text(harness + defines + '\n' + helpers + checks)
            subprocess.run([os.environ.get('CC', 'cc'), '-std=c99', '-Wall', '-Wextra', '-Werror',
                            '-I' + str(ROOT / 'msm89xx/image/lk2nd-a8'), str(c), '-o', str(binary)], check=True)
            subprocess.run([str(binary)], check=True)

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

    def test_screen_timeout_settings(self):
        lua = next((shutil.which(x) for x in ('lua5.1', 'lua', 'lua5.4', 'luajit') if shutil.which(x)), None)
        if not lua:
            self.skipTest('Lua unavailable')
        subprocess.run([lua, str(Path(__file__).with_name('timeouts.lua')),
                        str(PACKAGE / 'files')], check=True)

    def test_dashboard_pixels_and_transitions(self):
        lua = next((shutil.which(x) for x in ('lua5.1', 'lua', 'lua5.4', 'luajit') if shutil.which(x)), None)
        if not lua:
            self.skipTest('Lua unavailable')
        subprocess.run([lua, str(Path(__file__).with_name('dashboard.lua')),
                        str(PACKAGE / 'files')], check=True)

    def test_private_call_display_cache(self):
        lua = next((shutil.which(x) for x in ('lua5.1', 'lua', 'lua5.4', 'luajit') if shutil.which(x)), None)
        if not lua:
            self.skipTest('Lua unavailable')
        subprocess.run([lua, str(Path(__file__).with_name('calls.lua')),
                        str(PACKAGE / 'files')], check=True)

    def test_korean_sms_reader(self):
        lua = next((shutil.which(x) for x in ('lua5.1', 'lua', 'lua5.4', 'luajit') if shutil.which(x)), None)
        if not lua:
            self.skipTest('Lua unavailable')
        with tempfile.TemporaryDirectory() as tmp:
            commands = Path(tmp) / 'queries'
            subprocess.run([lua, str(Path(__file__).with_name('messages.lua')),
                            str(PACKAGE / 'files'), str(commands)], check=True)
            # Execute the exact generated SQL against the production schema.
            db = sqlite3.connect(':memory:')
            db.executescript((ROOT / 'tools/modemconfig-ko/schema.sql').read_text())
            db.execute("INSERT INTO subscriptions(id,fingerprint,slot,provider,updated_at) VALUES('sim','fingerprint',1,'test','now')")
            db.execute("INSERT INTO conversations(id,subscription_id,kind,peer,created_at) VALUES('thread','sim','direct','114','now')")
            for seq, direction in ((1, 'incoming'), (21, 'incoming'), (22, 'outgoing')):
                db.execute("INSERT INTO messages(seq,id,conversation_id,source_key,protocol,direction,sender,body,created_at) VALUES(?,?,'thread',?,'sms',?,'114','한글 본문','now')",
                           (seq, f'm{seq}', f'k{seq}', direction))
            queries = [line.split("messages.sqlite3 '", 1)[1].rsplit("' 2>/dev/null", 1)[0]
                       for line in commands.read_text().splitlines()]
            self.assertEqual([row[0] for row in db.execute(queries[0])], [21, 1])
            self.assertEqual([row[0] for row in db.execute(queries[2])], [1])
            self.assertEqual(db.execute(queries[3]).fetchone()[1], '한글 본문')
            db.close()

    def test_luci_hardware_options(self):
        node = shutil.which('node')
        if not node:
            self.skipTest('Node unavailable')
        subprocess.run([node, str(Path(__file__).with_name('settings.js')),
                        str(PACKAGE / 'files/a8-device.js')], check=True)

    def test_display_package_can_be_selected(self):
        # OpenWrt propagates the DRM packages' @DISPLAY_SUPPORT dependency
        # to a8-display, removing it entirely when this option is missing.
        for board in ('mifib-v18', 'jz08c-v50'):
            with self.subTest(board=board):
                config = (ROOT / 'diffconfigs' / board).read_text().splitlines()
                self.assertIn('CONFIG_DISPLAY_SUPPORT=y', config)
                self.assertIn('CONFIG_BUSYBOX_CONFIG_FBSPLASH=y', config)

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

    def test_jz08c_stock_sequence(self):
        stock = bytes.fromhex((PACKAGE / 'files/jz08c-v50-stock-init.hex').read_text())
        # Anchored to the preferred ST7735S property in the stock boot DTB.
        self.assertEqual(hashlib.sha256(stock).hexdigest(),
                         '5c229b075bf4b541ca526ae1c9eabf487da3643b32e74aed2cbf06ab64952de3')
        firmware = panel.convert(stock)
        self.assertEqual(firmware[16:21], bytes.fromhex('1100000178'))
        self.assertIn(bytes.fromhex('2a04000200812b0400030082'), firmware)
        self.assertEqual(firmware[-4:], bytes.fromhex('29002c00'))

    def test_malformed_records_are_rejected(self):
        for bad in (b"", b"\x00", b"\x00\x00", b"\x00\x02\x11", b"\x00\x01\x00"):
            with self.subTest(data=bad), self.assertRaises(ValueError):
                panel.convert(bad)


class StatusTests(unittest.TestCase):
    def shell(self, body, cap="", volt="", status="", **extra):
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
        env.update(extra)
        with tempfile.TemporaryDirectory() as tmp:
            env['TEST_SMS_CACHE'] = str(Path(tmp) / 'sms.json')
            env.setdefault('TEST_SMS_DB', str(Path(tmp) / 'absent.sqlite3'))
            script = script.replace('. "$DISPLAY_SOURCE"', '. "$DISPLAY_SOURCE"\nSMS_CACHE="$TEST_SMS_CACHE"\nSMS_DB="$TEST_SMS_DB"')
            return subprocess.check_output(["sh", "-c", script], env=env)

    def modem(self, state='connected', tech='lte', carrier='KOR LG Uplus', quality='62'):
        return self.shell('''
jsonfilter() { :; }
mmcli() {
    case " $* " in
        *' -L '*) printf '/org/freedesktop/ModemManager1/Modem/0 [A8]';;
        *) printf '%s' "$MODEM_DATA";;
    esac
}
modem_line
''', MODEM_DATA=f'''modem.generic.state                             : {state}
modem.generic.access-technologies.length        : 1
modem.generic.access-technologies.value[1]       : {tech}
modem.generic.signal-quality.value              : {quality}
modem.3gpp.operator-code                         : 45006
modem.3gpp.operator-name                         : {carrier}
''')

    def test_connected_carrier_and_signal_fit_the_panel(self):
        self.assertEqual(self.modem(), b'LTE  LG U+ ||||-')
        self.assertEqual(self.modem(carrier='Long Carrier Name', quality='100'),
                         b'LTE  Long  |||||')
        self.assertEqual(self.modem(carrier='통신사', quality='unknown'), b'LTE  45006 -----')
        for tech, prefix in [('hspa', b'3G '), ('edge', b'2G '), ('unknown', b'NET ')]:
            output = self.modem(tech=tech)
            self.assertEqual(len(output), 16)
            self.assertTrue(output.startswith(prefix))
        for quality, bars in [('0', b'-----'), ('1', b'|----'), ('20', b'|----'),
                              ('21', b'||---'), ('40', b'||---'), ('41', b'|||--'),
                              ('60', b'|||--'), ('61', b'||||-'), ('80', b'||||-'),
                              ('81', b'|||||'), ('100', b'|||||'), ('101', b'-----')]:
            self.assertTrue(self.modem(quality=quality).endswith(bars))

    def test_connection_failure_and_searching_keep_their_labels(self):
        for state in ('searching', 'registered', 'connecting', 'failed', 'disabled', ''):
            with self.subTest(state=state):
                self.assertEqual(self.modem(state=state), ('MODEM ' + (state.upper() or '--')).encode())

    def test_sms_arrival_and_sms_manager_acknowledgement(self):
        listing = '\n'.join(f'/org/freedesktop/ModemManager1/SMS/{i} (received)' for i in range(3))
        script = '''set -e
mmcli() { printf '%s' "$SMS_LIST"; return "$SMS_RESULT"; }
uci() { printf '%s' "$SMS_SEEN"; }
sms_line
'''
        for seen, expected in [('0', b'SMS NEW 3'), ('2', b'SMS NEW 1'),
                               ('3', b''), ('4', b''), ('bad', b'SMS NEW 3')]:
            self.assertEqual(self.shell(script, SMS_LIST=listing, SMS_SEEN=seen, SMS_RESULT='0'), expected)
        self.assertEqual(self.shell(script, SMS_LIST='No SMS messages were found', SMS_SEEN='0', SMS_RESULT='0'), b'')
        self.assertEqual(self.shell(script, SMS_LIST='', SMS_SEEN='0', SMS_RESULT='1'), b'SMS --')
        self.assertEqual(self.shell(script, SMS_LIST=listing, SMS_SEEN='0002', SMS_RESULT='0'), b'SMS NEW 1')

    def test_sqlite_messages_and_local_read_state_drive_the_lcd(self):
        body = '''set -e
mmcli() { echo 'unexpected modem inbox lookup' >&2; exit 91; }
uci() { echo 'unexpected legacy unread baseline' >&2; exit 92; }
sms_line >/dev/null
cat "$SMS_CACHE"
'''
        with tempfile.TemporaryDirectory() as tmp:
            path = Path(tmp) / 'messages.sqlite3'
            with sqlite3.connect(path) as db:
                db.executescript((ROOT / 'tools/modemconfig-ko/schema.sql').read_text())
                for slot in (1, 2):
                    db.execute('INSERT INTO subscriptions VALUES(?,?,?,?,?)',
                               (str(slot), str(slot), slot, 'SIM', 'now'))
                    db.execute("INSERT INTO conversations(id,subscription_id,kind,peer,created_at) VALUES(?,?,'direct',?,'now')",
                               (str(slot), str(slot), 'test' + str(slot)))
                for i, (slot, direction, read) in enumerate([(1, 'incoming', None),
                        (2, 'incoming', None), (1, 'incoming', 'now'), (1, 'outgoing', None)]):
                    db.execute("INSERT INTO messages(id,conversation_id,source_key,protocol,direction,sender,body,created_at,local_read_at) VALUES(?,?,?,'sms',?,'test','private','now',?)",
                               (str(i), str(slot), str(i), direction, read))
                db.commit()
                self.assertEqual(json.loads(self.shell(body, TEST_SMS_DB=str(path))),
                                 {'available': True, 'total': 4, 'new': 2})
                db.execute("UPDATE messages SET local_read_at='now' WHERE direction='incoming'")
                db.commit()
                self.assertEqual(json.loads(self.shell(body, TEST_SMS_DB=str(path))),
                                 {'available': True, 'total': 4, 'new': 0})
                db.execute('DELETE FROM messages'); db.commit()
                self.assertEqual(json.loads(self.shell(body, TEST_SMS_DB=str(path))),
                                 {'available': True, 'total': 0, 'new': 0})
            db.close()
            path.write_bytes(b'not a database')
            self.assertEqual(json.loads(self.shell(body, TEST_SMS_DB=str(path))),
                             {'available': False, 'total': 0, 'new': 0})

    def test_missing_battery_is_unknown(self):
        self.assertEqual(self.shell("battery_line"), b"BATT --")
        self.assertEqual(self.shell("battery_line", cap="101", volt="bad"), b"BATT --")

    def test_empty_battery_is_zero_not_unknown(self):
        self.assertEqual(self.shell("battery_line", cap="0", status="Charging"), b"BATT 000% CHG")

    def test_voltage_when_capacity_is_unavailable(self):
        self.assertEqual(self.shell("battery_line", volt="3920000"), b"BATT 3.92V")

    def test_missing_sim_is_a_normal_status(self):
        output = self.shell('''
read_value() { printf '{}'; }
jsonfilter() { printf no-sim; }
mmcli() { exit 91; }
modem_line
''')
        self.assertEqual(output, b"SIM NONE")

    def test_modem_unavailable_is_distinct_from_missing_sim(self):
        for state, expected in [('modem-unavailable', b'MODEM UNAVAIL'), ('waiting-sim', b'SIM WAIT')]:
            output = self.shell('''read_value() { printf '{}'; }
jsonfilter() { printf '%s' "$APN_STATE"; }
mmcli() { exit 91; }
modem_line
''', APN_STATE=state)
            self.assertEqual(output, expected)

    def test_charging_status_comes_from_the_battery_monitor(self):
        self.assertEqual(self.shell('''read_value() {
case "$1" in
    */capacity) printf 84 ;;
    */pm8916-bms-vm/status) printf Charging ;;
esac
}
battery_line
'''), b'BATT 084% CHG')

    def test_status_shows_station_switch_instead_of_apn(self):
        for enabled, expected in [('1', b'STATION ON'), ('0', b'STATION OFF')]:
            self.assertEqual(self.shell('uci() { printf "%s" "$ENABLED"; }; station_line',
                                        ENABLED=enabled), expected)

    def test_sms_menu_cache_and_modem_unavailable(self):
        body = '''mmcli() { printf '%s' "$SMS_LIST"; return "$SMS_RESULT"; }
uci() { printf 1; }
sms_line >/dev/null
test ! -e "$SMS_CACHE.new" || exit 1
cat "$SMS_CACHE"
'''
        listing = '\n'.join(f'/org/freedesktop/ModemManager1/SMS/{i}' for i in range(3))
        self.assertEqual(json.loads(self.shell(body, SMS_LIST=listing, SMS_RESULT='0')),
                         {'available': True, 'total': 3, 'new': 2})
        self.assertEqual(json.loads(self.shell(body, SMS_LIST='', SMS_RESULT='1')),
                         {'available': False, 'total': 0, 'new': 0})

    def test_status_fits_console_without_scrolling(self):
        script = '''
battery_line() { printf 'BATT 050%%'; }
wifi_line() { printf 'WIFI ON'; }
modem_line() { printf 'MODEM A-VERY-LONG-STATE'; }
sms_line() { printf 'SMS NEW 3'; }
station_line() { printf 'STATION ON'; }
ubus() { printf '%s' "$LAN_ADDRESS"; }
jsonfilter() { cat; }
awk() { printf 'UPTIME 00h01m'; }
show_status
'''
        for address, displayed in [('192.168.8.1', b'IP   192.168.8.1'),
                                   ('192.168.123.123', b'192.168.123.123')]:
            output = self.shell(script, LAN_ADDRESS=address)
            self.assertTrue(output.startswith(b"\x1b[H"))
            rows = output[3:].split(b"\r\n")
            self.assertEqual(len(rows), 8)
            self.assertTrue(all(len(row) == 16 for row in rows))
            self.assertEqual(rows[1].rstrip(), b'SMS NEW 3')
            self.assertEqual(rows[3], b"MODEM A-VERY-LON")
            self.assertEqual(rows[4].rstrip(), b"WIFI ON")
            self.assertEqual(rows[5].rstrip(), displayed)
            self.assertEqual(rows[6].rstrip(), b'UPTIME 00h01m')
            self.assertEqual(rows[7].rstrip(), b"STATION ON")
            self.assertNotIn(b'APN', output)


if __name__ == "__main__":
    unittest.main()
