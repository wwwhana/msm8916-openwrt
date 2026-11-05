#!/usr/bin/env python3
"""Exercise the actual gadget shell functions with file-backed USB endpoints."""
import os
from pathlib import Path
import shlex
import subprocess
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[2]
SOURCE = (ROOT / 'packages/uci-usb-gadget/files/usb-gadget.sh').read_text()
FUNCTIONS = SOURCE.split('case "${1:-}" in\n', 1)[0].replace('. /lib/functions.sh', ':')


class PackagePrepareTests(unittest.TestCase):
    def prepare(self, custom=False, target=True):
        source = (ROOT / 'scripts/openwrt-prepare.sh').read_text()
        body = source.split('install_package_patches() {', 1)[1].split('\n}\n', 1)[0]
        with tempfile.TemporaryDirectory() as tmp:
            base = Path(tmp)
            package = base / 'packages/example'
            (package / 'patches').mkdir(parents=True)
            (package / 'patches/test.patch').write_text('package patch')
            if custom:
                (package / 'Makefile').write_text('complete custom package')
            dest = base / 'openwrt/package/system/example'
            if target:
                (dest / 'patches').mkdir(parents=True)
                (dest / 'patches/stale.patch').touch()
            script = '''die() { echo "$*" >&2; exit 1; }
info() { :; }
PACKAGES_DIR="$1/packages"
OPENWRT_DIR="$1/openwrt"
install_package_patches() {\n''' + body + '\n}\ninstall_package_patches\n'
            run = subprocess.run(['bash', '-c', script, 'prepare-test', str(base)], capture_output=True)
            return run.returncode, (dest / 'patches/test.patch').exists(), (dest / 'patches/stale.patch').exists()

    def test_complete_package_keeps_its_own_patches(self):
        self.assertEqual(self.prepare(custom=True, target=False), (0, False, False))

    def test_patch_only_overlay_is_still_installed(self):
        self.assertEqual(self.prepare(), (0, True, False))

    def test_missing_overlay_target_still_fails(self):
        self.assertNotEqual(self.prepare(target=False)[0], 0)


class GadgetTests(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory(prefix='usb adb ')
        self.addCleanup(self.tmp.cleanup)
        self.base = Path(self.tmp.name)
        for p in ['g/functions/ncm.usb0', 'g/functions/acm.GS0', 'g/configs/c.1', 'ffs']:
            (self.base / p).mkdir(parents=True, exist_ok=True)
        (self.base / 'events').touch()
        (self.base / 'service').write_text('''#!/bin/sh
echo "service:$1" >> "$BASE/events"
if [ "$1" = start ]; then
    [ "$MODE" != fail ] || exit 1
    touch "$BASE/ffs/ep0"
    [ "$MODE" != ready ] || touch "$BASE/ffs/ep1" "$BASE/ffs/ep2"
    [ "$MODE" != partial ] || touch "$BASE/ffs/ep1"
else
    rm -f "$BASE/ffs/ep1" "$BASE/ffs/ep2"
fi
''')
        (self.base / 'service').chmod(0o755)

    def run_shell(self, body, mode='ready'):
        prefix = FUNCTIONS + '''
load_config() { :; }
log() { echo "$*" >> "$BASE/events"; }
modprobe() { :; }
lsmod() { echo 'libcomposite 1 0'; }
mountpoint() { [ -e "$2/.mounted" ]; }
mount() {
    [ "$4" != "$ADB_FFS" ] || touch "$ADB_FFS/.mounted" "$ADB_FFS/ep0"
}
umount() { echo unmount >> "$BASE/events"; rm -f "$ADB_FFS/.mounted" "$ADB_FFS"/ep*; }
sleep() { :; }
find_udc() { echo fake.udc; }
get_serial_number() { echo test; }
setup_acm() { ln -sf "$CFG_FUNCTIONS_PATH/acm.GS0" "$CFG_CONFIG_PATH/"; echo +ACM; }
setup_ncm() { ln -sf "$CFG_FUNCTIONS_PATH/ncm.usb0" "$CFG_CONFIG_PATH/"; echo +NCM; }
setup_network() { echo network >> "$BASE/events"; }
CFG_ENABLED=1
CFG_ADB=1
CFG_NCM=1
CFG_ACM=1
CFG_RNDIS=0
CFG_ECM=0
CFG_UMS=0
CFG_GADGET_NAME=g
CFG_GADGET_PATH="$BASE/g"
CFG_FUNCTIONS_PATH="$BASE/g/functions"
CFG_CONFIG_PATH="$BASE/g/configs/c.1"
ADB_FFS="$BASE/ffs"
ADB_SERVICE="$BASE/service"
ADB_READY="$BASE/ready"
GADGET_LOCK="$BASE/lock"
'''
        result = subprocess.run([os.environ.get('TEST_SHELL', '/bin/sh'), '-c', prefix + body],
                                env={**os.environ, 'BASE': str(self.base), 'MODE': mode},
                                text=True, capture_output=True, timeout=10)
        self.assertEqual(result.returncode, 0, result.stderr + result.stdout)

    def test_ready_before_initial_bind(self):
        self.run_shell('setup_gadget')
        self.assertEqual((self.base / 'g/UDC').read_text().strip(), 'fake.udc')
        self.assertEqual((self.base / 'ready').read_text().strip(), 'fake.udc')
        self.assertEqual((self.base / 'g/configs/c.1/strings/0x409/configuration').read_text().strip(),
                         'ACM+NCM+ADB')
        self.assertTrue((self.base / 'g/configs/c.1/ffs.adb').is_symlink())

    def test_partial_descriptors_keep_network_and_console(self):
        self.run_shell('setup_gadget', mode='partial')
        self.assertEqual((self.base / 'g/UDC').read_text().strip(), 'fake.udc')
        self.assertFalse((self.base / 'g/configs/c.1/ffs.adb').exists())
        self.assertFalse((self.base / 'ready').exists())
        self.assertTrue((self.base / 'g/configs/c.1/ncm.usb0').is_symlink())
        self.assertTrue((self.base / 'g/configs/c.1/acm.GS0').is_symlink())
        self.assertIn('network', (self.base / 'events').read_text())

    def test_daemon_start_failure_keeps_network(self):
        self.run_shell('setup_gadget', mode='fail')
        self.assertEqual((self.base / 'g/UDC').read_text().strip(), 'fake.udc')
        self.assertIn('network', (self.base / 'events').read_text())

    def test_reconnect_restores_unbound_gadget(self):
        self.run_shell('''setup_gadget
ip() { echo "ip:$*" >> "$BASE/events"; }
echo usb-test > "$CFG_FUNCTIONS_PATH/ncm.usb0/ifname"
echo '' > "$CFG_GADGET_PATH/UDC"
rebind_adb
''')
        self.assertEqual((self.base / 'g/UDC').read_text().strip(), 'fake.udc')
        self.assertTrue((self.base / 'g/configs/c.1/ffs.adb').is_symlink())
        self.assertIn('ip:link set usb-test master br-lan', (self.base / 'events').read_text())

    def test_dead_daemon_falls_back_then_rejoins(self):
        self.run_shell('''setup_gadget
echo '' > "$CFG_GADGET_PATH/UDC"
rm "$ADB_FFS/ep2"
rebind_adb
[ "$(cat "$CFG_GADGET_PATH/UDC")" = fake.udc ]
[ ! -L "$CFG_CONFIG_PATH/ffs.adb" ]
[ -L "$CFG_CONFIG_PATH/ncm.usb0" ]
touch "$ADB_FFS/ep2"
rebind_adb
[ -L "$CFG_CONFIG_PATH/ffs.adb" ]
''')

    def test_active_gadget_does_not_reenumerate(self):
        self.run_shell('''setup_gadget
echo still-bound > "$CFG_GADGET_PATH/UDC"
rebind_adb
[ "$(cat "$CFG_GADGET_PATH/UDC")" = still-bound ]
''')

    def test_stop_disarms_reconnect_and_unmounts(self):
        self.run_shell('''setup_gadget
stop_adb
echo '' > "$CFG_GADGET_PATH/UDC"
rebind_adb
[ ! -s "$CFG_GADGET_PATH/UDC" ] || [ -z "$(cat "$CFG_GADGET_PATH/UDC")" ]
''')
        self.assertFalse((self.base / 'ready').exists())
        self.assertFalse((self.base / 'ffs/.mounted').exists())
        events = (self.base / 'events').read_text()
        self.assertLess(events.index('service:stop'), events.index('unmount'))

    def test_watcher_skips_locked_start_or_stop(self):
        self.run_shell('''mkdir "$GADGET_LOCK"
if lock_gadget try; then exit 1; fi
[ -d "$GADGET_LOCK" ]
''')

    def test_a8_defaults_preserve_an_explicit_disable(self):
        defaults = (ROOT / 'packages/adbd/files/98-a8-adb').read_text()
        board = self.base / 'compatible'
        board.write_bytes(b'mifib-v18,generic\0qcom,msm8916\0')
        defaults = defaults.replace('/proc/device-tree/compatible', shlex.quote(str(board)))
        for exists in [False, True]:
            commands = self.base / 'uci-input'
            program = f'''uci() {{
if [ "$2" = get ]; then return {0 if exists else 1}; fi
cat > {shlex.quote(str(commands))}
}}
''' + defaults
            result = subprocess.run(['/bin/sh', '-c', program], capture_output=True, text=True)
            self.assertEqual(result.returncode, 0, result.stderr)
            if exists:
                self.assertFalse(commands.exists())
            else:
                self.assertIn("usbgadget.adb.enabled='1'", commands.read_text())
                commands.unlink()


if __name__ == '__main__':
    unittest.main(verbosity=2)
