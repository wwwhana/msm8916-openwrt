"""Exercise the opt-in setting, generated BlueZ config and failure recovery."""
import configparser
import os
from pathlib import Path
import subprocess
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[2]


class CompatibilityTests(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.base = Path(self.tmp.name)
        self.conf = self.base / 'main.conf'
        self.original = '[General]\n#SecureConnections = on\nDebugKeys = false\nControllerMode = dual\n[Policy]\nAutoEnable = true\n'
        self.conf.write_text(self.original)
        (self.base / 'board_name').write_text('mifib-v18,generic\n')
        (self.base / 'compat').write_text('0')
        (self.base / 'powered').write_text('yes')
        (self.base / 'mode').write_text('01')
        (self.base / 'run').mkdir()
        self.env = {**os.environ, 'PATH': str(self.base) + os.pathsep + os.environ['PATH'],
                    'TEST_DIR': str(self.base)}
        self.executable('uci', r'''
[ "$1" != -q ] || shift
case "$1 $2" in
 'get bt-nap.main') echo sharing ;;
 'get bt-nap.main.pairing_compat') cat "$TEST_DIR/compat" ;;
 'changes bt-nap') [ "$PENDING" != 1 ] || echo pending ;;
 set*)
  [ "$SAVE_FAIL" != 1 ] || [ "${2##*=}" != 1 ] || exit 1
  printf '%s' "${2##*=}" > "$TEST_DIR/compat" ;;
 'commit bt-nap'|'revert bt-nap.main.pairing_compat') : ;;
 *) exit 1 ;;
esac
''')
        self.executable('bluetoothctl', r'''
case "$*" in
 '--timeout 9')
  while IFS= read -r command; do
   case "$command" in
    'power off') echo no > "$TEST_DIR/powered" ;;
    'sc off') [ "$MODE_FAIL" = 1 ] || echo 00 > "$TEST_DIR/mode" ;;
    'sc on') echo 01 > "$TEST_DIR/mode" ;;
    info)
     settings='bondable ssp br/edr le'
     [ "$(cat "$TEST_DIR/mode")" != 01 ] || settings="$settings secure-conn"
     echo "current settings: $settings " ;;
   esac
  done ;;
 *show) [ "$NO_CONTROLLER" != 1 ] || exit 1; echo "Powered: $(cat "$TEST_DIR/powered")" ;;
 *'power on') echo yes > "$TEST_DIR/powered" ;;
 *'power off') echo no > "$TEST_DIR/powered" ;;
 *) exit 1 ;;
esac
''')
        self.executable('hcitool', 'echo "  01 79 0C 00 $(cat "$TEST_DIR/mode") "\n')
        self.executable('sleep', ':\n')
        self.executable('bluetoothd', r'''
path=$("$TEST_DIR/bt-compat" config) || exit 1
# BlueZ enables missing SC but does not disable SC that is already enabled.
if [ "$(cat "$TEST_DIR/mode")" = 00 ] && ! grep -q '^SecureConnections = off$' "$path"; then
 echo 01 > "$TEST_DIR/mode"
fi
echo restart >> "$TEST_DIR/restarts"
echo yes > "$TEST_DIR/powered"
''')
        self.executable('bt-pairing', ':\n')
        self.executable('bt-nap', ':\n')
        text = (ROOT / 'packages/bluez-network/files/bt-compat').read_text()
        text = text.replace('/etc/bluetooth/main.conf', str(self.conf))
        text = text.replace('/var/run', str(self.base / 'run'))
        text = text.replace('/tmp/sysinfo/board_name', str(self.base / 'board_name'))
        text = text.replace('/etc/init.d/', str(self.base) + '/')
        self.cli = self.executable('bt-compat', text.removeprefix('#!/bin/sh\n'))

    def executable(self, name, text):
        path = self.base / name
        path.write_text('#!/bin/sh\n' + text)
        path.chmod(0o755)
        return path

    def run_cli(self, command, **extra):
        return subprocess.run([str(self.cli), command], env={**self.env, **extra},
                              text=True, capture_output=True, timeout=5)

    def test_default_off_and_explicit_toggle(self):
        self.assertEqual(self.run_cli('status').stdout.strip(), '0')
        self.assertEqual(self.run_cli('config').stdout.strip(), str(self.conf))
        for command, saved, mode in (('on', '1', '00'), ('off', '0', '01')):
            result = self.run_cli(command)
            self.assertEqual(result.returncode, 0, result.stderr)
            self.assertEqual((self.base / 'compat').read_text().strip(), saved)
            self.assertEqual((self.base / 'mode').read_text().strip(), mode)
            self.assertEqual(self.conf.read_text(), self.original)
            self.assertFalse((self.base / 'run/bt-compat.lock').exists())
            if saved == '1':
                config = configparser.ConfigParser()
                config.read_string((self.base / 'run/bluetooth-compat.conf').read_text())
                self.assertEqual(config['General']['SecureConnections'], 'off')
                self.assertEqual(config['General']['DebugKeys'], 'false')
                self.assertEqual(config['General']['ControllerMode'], 'dual')
                self.assertEqual(config['Policy']['AutoEnable'], 'true')
        self.assertEqual(len((self.base / 'restarts').read_text().splitlines()), 2)

    def test_power_off_is_preserved(self):
        (self.base / 'powered').write_text('no')
        self.assertEqual(self.run_cli('on').returncode, 0)
        self.assertEqual((self.base / 'powered').read_text().strip(), 'no')

    def test_rejected_edits_and_wrong_board_do_not_write(self):
        for command, extra in (('on', {'PENDING': '1'}), ('on', {'NO_CONTROLLER': '1'}),
                               ('on; reboot', {})):
            self.assertNotEqual(self.run_cli(command, **extra).returncode, 0)
            self.assertEqual((self.base / 'compat').read_text(), '0')
        (self.base / 'board_name').write_text('mifib-v12,generic\n')
        self.assertNotEqual(self.run_cli('on').returncode, 0)
        self.assertEqual(self.run_cli('config').stdout.strip(), str(self.conf))
        self.assertFalse((self.base / 'restarts').exists())

    def test_apply_or_save_failure_restores_previous_mode(self):
        for extra in ({'MODE_FAIL': '1'}, {'SAVE_FAIL': '1'}):
            with self.subTest(extra=extra):
                result = self.run_cli('on', **extra)
                self.assertNotEqual(result.returncode, 0)
                self.assertIn('previous setting restored', result.stderr)
                self.assertEqual((self.base / 'compat').read_text().strip(), '0')
                self.assertEqual((self.base / 'mode').read_text().strip(), '01')
                self.assertFalse((self.base / 'run/bt-compat.lock').exists())


if __name__ == '__main__':
    unittest.main()
