from pathlib import Path
import os
import subprocess
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[2]
SOURCE = (ROOT / 'packages/msm89xx-scripts/root/usr/sbin/wifi-sta-up').read_text()
FUNCTIONS = SOURCE[SOURCE.index('\nfind_ap() {'):SOURCE.index('\nap_state() {')]
MOCKS = '''
timeout() { shift; "$@"; }
sleep() { :; }
wpa_cli() {
 case "$*" in *ping) [ "$HAS_STA" = 1 ] && echo PONG ;; *scan) echo "${SCAN_REPLY:-OK}" ;; esac
}
iw() {
 printf '%s\\n' "$*" >> "$LOG"
 case "$*" in
  dev) printf 'Interface phy9-ap0\\n'; [ "$HAS_STA" != 1 ] || echo 'Interface phy0-sta0' ;;
  'dev phy9-ap0 info') echo 'type AP' ;;
  'dev phy9-ap0 scan dump') [ "$CACHED" != 0 ] && printf 'BSS 00:11:22:33:44:55\\n SSID: Example\\n' ;;
  'dev phy9-ap0 scan') [ "$LIVE" = 1 ] || return 1; echo 'BSS 00:11:22:33:44:55' ;;
 esac
 return 0
}
run_scan
'''

class ScanTests(unittest.TestCase):
    def run_scan(self, **env):
        with tempfile.TemporaryDirectory() as tmp:
            log = Path(tmp) / 'commands'
            result = subprocess.run(['/bin/sh'], input=FUNCTIONS + MOCKS, text=True, capture_output=True,
                env={**os.environ, 'LOG': str(log), 'HAS_STA': '0', 'CACHED': '1', 'LIVE': '0', **env})
            return result, log.read_text()

    def test_supplicant_owns_station_scan(self):
        result, log = self.run_scan(HAS_STA='1')
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn('BSS ', result.stdout)
        self.assertIn('dev phy9-ap0 scan dump', log)
        self.assertNotIn('dev phy9-ap0 scan\n', log)

    def test_failed_active_scan_uses_cache(self):
        result, _ = self.run_scan()
        self.assertEqual(result.returncode, 0)
        self.assertIn('scan_source=cache', result.stdout)

    def test_failure_without_cache_is_not_empty_success(self):
        result, _ = self.run_scan(CACHED='0')
        self.assertNotEqual(result.returncode, 0)
        self.assertIn('Scan unavailable', result.stderr)

    def test_successful_scan_and_luci(self):
        result, _ = self.run_scan(LIVE='1')
        self.assertEqual(result.returncode, 0)
        self.assertNotIn('scan_source=cache', result.stdout)
        subprocess.run(['node', str(Path(__file__).with_name('scan.js')),
            str(ROOT / 'packages/luci-app-wifi-sta/htdocs/luci-static/resources/view/wifi-sta/config.js')], check=True)

if __name__ == '__main__': unittest.main()
