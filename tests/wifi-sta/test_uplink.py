#!/usr/bin/env python3
"""Exercise route selection without touching the host's routes or Wi-Fi."""
from pathlib import Path
import os
import shutil
import subprocess
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[2]
SCRIPTS = ROOT / 'packages/msm89xx-scripts/root'
MANAGER = (SCRIPTS / 'usr/sbin/uplink-manager').read_text().replace('. /lib/functions.sh', '')
MANAGER = MANAGER.split('\nwake=0\n', 1)[0]
ROUTE = 'default via 10.0.0.1 dev wwan0 proto static src 10.0.0.2 metric 10'
MOCK_IP = '''
ip() {
    case "$*" in
        '-4 route show default dev wwan0') printf '%s\\n' "$MODEM_ROUTES" ;;
        *) printf '%s\\n' "$*" >> "$IP_LOG"
           case "$*" in
               '-4 route replace default via 10.0.0.1 dev wwan0 metric 50')
                   return "${REPLACE_RESULT:-0}" ;;
           esac ;;
    esac
}
'''


class UplinkTests(unittest.TestCase):
    def shell(self, script, **env):
        with tempfile.TemporaryDirectory() as tmp:
            log = Path(tmp) / 'routes'
            log.touch()
            result = subprocess.run(['/bin/sh'], input=MANAGER + '\n' + MOCK_IP + '\n' + script,
                                    text=True, capture_output=True,
                                    env={**os.environ, 'IP_LOG': str(log), **env})
            self.assertEqual(result.returncode, 0, result.stderr)
            return result.stdout.strip(), log.read_text().splitlines()

    def normalize(self, routes, result='0'):
        return self.shell('modem_dev=wwan0; modem_gw=10.0.0.1; modem_metric=50\n'
                          'normalize_modem_route\n:', MODEM_ROUTES=routes, REPLACE_RESULT=result)[1]

    def test_lower_modem_metric_removed_only_after_replacement(self):
        calls = self.normalize(ROUTE + '\ndefault via 10.0.0.9 dev wwan0 metric 20')
        self.assertEqual(calls, [
            '-4 route replace default via 10.0.0.1 dev wwan0 metric 50',
            '-4 route del default via 10.0.0.1 dev wwan0 metric 10'])

    def test_no_deletion_when_replacement_fails(self):
        self.assertEqual(len(self.normalize(ROUTE, '2')), 1)

    def test_default_metric_zero_and_existing_metric_fifty(self):
        calls = self.normalize('default via 10.0.0.1 dev wwan0')
        self.assertTrue(calls[-1].endswith('metric 0'))
        self.assertEqual(self.normalize(ROUTE.replace('metric 10', 'metric 50')), [])
        self.assertEqual(self.normalize(''), [])

    def test_policy_selects_route_and_falls_back(self):
        script = '''
read_modem() { modem_ok=1; modem_dev=wwan0; modem_gw=10.0.0.1; }
read_sta() { sta_ok=1; sta_dev=phy0-sta0; sta_gw=192.0.2.1; sta_dns=192.0.2.1; }
probe() {
    case "$1" in wwan0) return "$MODEM_FAILED" ;; *) return "$STA_FAILED" ;; esac
}
apply_dns() { :; }
write_json() { :; }
logger() { :; }
policy=$TEST_POLICY; fail_threshold=3; override_metric=40; modem_metric=50; sta_metric=100
cycle; cycle; cycle
printf '%s' "$active"
'''
        cases = [('auto', '0', '0', 'modem'), ('sta', '0', '0', 'sta'),
                 ('sta', '0', '1', 'modem'), ('auto', '1', '0', 'sta'),
                 ('modem', '1', '0', 'none'), ('sta', '1', '1', 'none')]
        for policy, modem_failed, sta_failed, expected in cases:
            with self.subTest(policy=policy, modem_failed=modem_failed, sta_failed=sta_failed):
                active, calls = self.shell(script, MODEM_ROUTES=ROUTE, TEST_POLICY=policy,
                                           MODEM_FAILED=modem_failed, STA_FAILED=sta_failed)
                self.assertEqual(active, expected)
                if expected == 'sta':
                    self.assertEqual(calls[-1], 'route replace default via 192.0.2.1 dev phy0-sta0 metric 40')
                else:
                    self.assertEqual(calls[-1], 'route del default metric 40')

    def test_ethernet_is_selected_only_as_a_wan_and_falls_back(self):
        script = '''
read_modem() { modem_ok=$MODEM_UP; modem_dev=wwan0; modem_gw=10.0.0.1; }
read_sta() { sta_ok=1; sta_dev=phy0-sta0; sta_gw=192.0.2.1; sta_dns=192.0.2.1; }
read_ethernet() { ethernet_ok=$ETHERNET_UP; ethernet_dev=eth0; ethernet_gw=198.51.100.1; }
probe() { case "$1" in eth0) return "$ETHERNET_FAILED" ;; *) return 0 ;; esac; }
apply_dns() { :; }; write_json() { :; }; logger() { :; }
policy=$TEST_POLICY; fail_threshold=3; override_metric=40; modem_metric=50; sta_metric=100
cycle; cycle; cycle
printf '%s' "$active"
'''
        for policy,modem_up,ethernet_up,failed,expected in [
            ('ethernet','1','1','0','ethernet'), ('auto','1','1','0','modem'),
            ('auto','0','1','0','ethernet'), ('sta','1','1','0','sta'),
            ('ethernet','1','1','1','modem'), ('ethernet','0','1','1','sta'),
            ('ethernet','1','0','0','modem'), ('modem','0','1','0','none')]:
            with self.subTest(policy=policy,expected=expected):
                active,calls=self.shell(script, MODEM_ROUTES=ROUTE, TEST_POLICY=policy,
                    MODEM_UP=modem_up, ETHERNET_UP=ethernet_up, ETHERNET_FAILED=failed)
                self.assertEqual(active,expected)
                if expected=='ethernet':
                    self.assertEqual(calls[-1], 'route replace default via 198.51.100.1 dev eth0 metric 40')

    def test_stopping_station_preserves_ap_and_wakes_manager(self):
        init = (SCRIPTS / 'etc/init.d/wifi-sta-late').read_text()
        script = init + '''
iw() {
    if [ "$*" = dev ]; then printf 'Interface phy0-sta0\\nInterface phy1-ap0\\n'
    else printf 'IW %s\\n' "$*"; fi
}
rm() { printf 'RM %s\\n' "$*"; }
cat() { printf 4242; }
kill() { printf 'KILL %s\\n' "$*"; }
stop_service
'''
        output, _ = self.shell(script)
        self.assertEqual(output.splitlines(), ['IW dev phy0-sta0 del',
                         'RM -f /tmp/wifi-sta-lease', 'KILL -USR1 4242'])

    def test_luci_toggle_and_priority_choices(self):
        node = shutil.which('node')
        if not node:
            self.skipTest('Node unavailable')
        subprocess.run([node, str(Path(__file__).with_name('settings.js')),
                        str(ROOT / 'packages/luci-app-wifi-sta/htdocs/luci-static/resources/view/wifi-sta')],
                       check=True)

    def test_usb_lan_wan_backend(self):
        lua = shutil.which('lua')
        if not lua:
            self.skipTest('Lua unavailable')
        subprocess.run([lua, str(Path(__file__).with_name('network.lua')),
                        str(ROOT / 'packages/a8-network/root/usr/lib/lua/a8_network.lua')], check=True)

    def test_station_renew_does_not_replace_the_active_wan_dns(self):
        dhcp = (SCRIPTS / 'usr/sbin/wifi-sta-dhcp.sh').read_text().split('\ncase "$1" in', 1)[0]
        for running in ('0', '1'):
            output, _ = self.shell(dhcp + '''
cat() { printf 42; }
kill() { return "$MANAGER_STOPPED"; }
uci() { printf 'uci %s\\n' "$*"; }
write_dns 198.51.100.53
:
''', MANAGER_STOPPED=running)
            if running == '0':
                self.assertEqual(output, '')
            else:
                self.assertIn('uci commit dhcp', output)


if __name__ == '__main__':
    unittest.main()
