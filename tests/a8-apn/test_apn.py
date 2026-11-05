#!/usr/bin/env python3
"""Offline provider database, Lua selection and existing-service integration."""
import importlib.util
import json
from pathlib import Path
import shutil
import subprocess
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[2]
PACKAGE = ROOT / 'packages/a8-apn'
spec = importlib.util.spec_from_file_location('providers', PACKAGE / 'gen-providers.py')
providers = importlib.util.module_from_spec(spec)
spec.loader.exec_module(providers)
XML = '''<serviceproviders><country code="kr"><provider><name>LG U+</name><gsm>
<network-id mcc="450" mnc="06"/>
<apn value="internet.lguplus.co.kr"><usage type="internet"/></apn>
<apn value="mms.example"><usage type="mms"/></apn>
<apn value="ims"><usage type="ims"/></apn>
<apn value="bad,extra=1"/>
</gsm></provider></country><country code="gb"><provider><name>Test</name><gsm>
<network-id mcc="234" mnc="015"/>
<apn value="old.example"><name>Legacy</name></apn>
<apn value="lte.example"><name>LTE Internet</name><username>user</username><password>pass</password></apn>
</gsm></provider></country></serviceproviders>'''


class APNTests(unittest.TestCase):
    def test_internet_only_and_leading_zero_mnc(self):
        db = providers.compile_database(XML)
        self.assertEqual(list(db), ['45006', '234015'])
        self.assertEqual([p['apn'] for p in db['45006']], ['internet.lguplus.co.kr'])

    def test_lte_preference_and_authentication(self):
        p = providers.compile_database(XML)['234015'][0]
        self.assertEqual((p['apn'], p['auth'], p['username'], p['password']),
                         ('lte.example', 'both', 'user', 'pass'))

    def test_database_files_are_written_and_readable(self):
        with tempfile.TemporaryDirectory() as tmp:
            source = Path(tmp) / 'providers.xml'
            source.write_text(XML)
            dest = Path(tmp) / 'db'
            subprocess.run(['python3', str(PACKAGE / 'gen-providers.py'), str(source), str(dest)],
                           check=True, capture_output=True)
            self.assertEqual(json.loads((dest / '45006.json').read_text())[0]['apn'], 'internet.lguplus.co.kr')

    def test_actual_lua_model_and_file_changes(self):
        lua = next((shutil.which(x) for x in ('lua', 'lua5.1', 'lua5.3', 'lua5.4', 'luajit') if shutil.which(x)), None)
        if not lua:
            self.skipTest('Lua interpreter unavailable; run tests/a8-apn/model.lua with Lua on the build host')
        with tempfile.TemporaryDirectory() as tmp:
            written = Path(tmp) / 'network'
            subprocess.run([lua, str(Path(__file__).with_name('model.lua')),
                            str(PACKAGE / 'files/model.lua'), str(written)], check=True, capture_output=True)
            self.assertIn('private.example\nipv4\n1\npap,chap', written.read_text())

    def test_existing_daemon_is_the_only_apn_worker(self):
        daemon = (ROOT / 'packages/qcom-carrier-autocfg/files/qcom-carrier-autocfg.sh').read_text()
        hook = daemon.split('while true; do', 1)[1].split('# Check if ModemManager', 1)[0]
        self.assertIn('/usr/sbin/a8-apn apply', hook)
        self.assertIn('continue', hook)  # includes disabled/manual mode
        self.assertNotIn('a8-apn.init', (PACKAGE / 'Makefile').read_text())
        self.assertNotIn('while true', (PACKAGE / 'files/a8-apn').read_text())
        service = (PACKAGE / 'files/service.lua').read_text()
        self.assertIn('custom-apns.tsv', service)
        self.assertIn('qcom-carrier-autocfg/apns.tsv', service)


if __name__ == '__main__':
    unittest.main()
