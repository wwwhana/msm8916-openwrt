"""Run SMS autoconfiguration against synthetic modem responses and UCI files."""
import json
from pathlib import Path
import os
import subprocess
import sys
import tempfile
import unittest

SCRIPT = Path(__file__).resolve().parents[2] / 'packages/luci-app-sms-manager/root/usr/sbin/sms_manager_autoconfig'
FAKE = r'''
import json, os, sys
from pathlib import Path
file=Path(os.environ['SMS_TEST_STATE'])
s=json.loads(file.read_text())
args=sys.argv[1:]
name=Path(sys.argv[0]).name
def save(): file.write_text(json.dumps(s))
if name == 'uci':
    args=[x for x in args if x != '-q']
    cmd=args[0]
    if cmd == 'changes':
        if s.get('pending'): print("sms_manager.test.ownnumber='staged'")
    elif cmd == 'get':
        if args[1] == 'sms_manager.@sms_manager[0]': print('sms_manager')
        else:
            key=args[1].split('].',1)[1]
            if key not in s['config']: sys.exit(1)
            print(s['config'][key])
    elif cmd == 'set':
        key,value=args[1].split('].',1)[1].split('=',1)
        s['config'][key]=value; save()
    elif cmd == 'commit': s['commits']=s.get('commits',0)+1; save()
    else: raise AssertionError(args)
elif name == 'mmcli':
    if '-L' in args: print('/org/freedesktop/ModemManager1/Modem/0 [Test] Modem')
    elif '-K' in args: print('modem.generic.ports.value[1] : wwan0qmi0 (qmi)')
    elif '-i' in args: print(json.dumps({'sim':{'properties':{'imsi':'450060000000001'}}}))
    else: print(json.dumps({'modem':{'generic':{'sim':'/org/freedesktop/ModemManager1/SIM/0', 'own-numbers':s.get('numbers',[])}}}))
elif name == 'jsonfilter':
    obj=json.load(sys.stdin)
    expr=args[args.index('-e')+1]
    if expr == '@.modem.generic.sim': value=obj['modem']['generic']['sim']
    elif expr == '@.sim.properties.imsi': value=obj['sim']['properties']['imsi']
    elif expr == '@.modem.generic["own-numbers"][0]': value=next(iter(obj['modem']['generic']['own-numbers']), '')
    else: raise AssertionError(expr)
    if value: print(value)
elif name == 'qmicli':
    s.setdefault('qmi_calls',[]).append(args[-1]); save()
    if '--dms-get-msisdn' in args and s.get('qmi_number'): print("MSISDN: '%s'" % s['qmi_number'])
    else: sys.exit(1)
else: raise AssertionError(name)
'''


class AutoconfigTests(unittest.TestCase):
    def run_script(self, state, *args):
        with tempfile.TemporaryDirectory() as tmp:
            directory=Path(tmp)
            for tool in ('uci', 'mmcli', 'jsonfilter', 'qmicli'):
                path=directory / tool
                path.write_text('#!' + sys.executable + '\n' + FAKE)
                path.chmod(0o755)
            data=directory / 'state.json'
            data.write_text(json.dumps(state))
            env={**os.environ, 'PATH':str(directory)+os.pathsep+os.environ['PATH'], 'SMS_TEST_STATE':str(data)}
            result=subprocess.run(['/bin/sh', str(SCRIPT), *args], env=env, text=True, capture_output=True, check=True)
            return json.loads(data.read_text()), result.stdout

    def test_korean_number_from_modemmanager(self):
        state,out=self.run_script({'config':{'pnumber':'+48'}, 'numbers':['+821012345678']}, '--report')
        self.assertEqual(state['config']['ownnumber'], '+821012345678')
        self.assertEqual(state['config']['pnumber'], '+82')
        self.assertEqual(state.get('qmi_calls',[]), [])
        self.assertEqual(state['commits'], 1)
        self.assertNotIn('imsi=', out)
        self.assertEqual(state['config']['readport'], '/org/freedesktop/ModemManager1/Modem/0')

    def test_qmi_msisdn_output_fallback(self):
        state,_=self.run_script({'config':{}, 'qmi_number':'+821087654321'})
        self.assertEqual(state['config']['ownnumber'], '+821087654321')
        self.assertEqual(state['qmi_calls'], ['--dms-get-msisdn'])

    def test_manual_number_and_prefix_survive_force_detect(self):
        state,_=self.run_script({'config':{'ownnumber':'+441234567890', 'pnumber':'+44', 'readport':'old'},
                                 'numbers':['+821012345678']}, '--force')
        self.assertEqual(state['config']['ownnumber'], '+441234567890')
        self.assertEqual(state['config']['pnumber'], '+44')
        self.assertEqual(state['config']['readport'], '/org/freedesktop/ModemManager1/Modem/0')

    def test_status_does_not_write(self):
        config={'pnumber':'+48'}
        state,_=self.run_script({'config':config, 'numbers':['+821012345678']}, '--status')
        self.assertEqual(state['config'], config)
        self.assertNotIn('commits', state)

    def test_pending_settings_are_not_committed(self):
        before={'config':{'ownnumber':'manual'}, 'pending':True}
        state,out=self.run_script(before, '--force', '--report')
        self.assertEqual(state, before)
        self.assertIn('error=', out)

    def test_absent_number_is_not_invented(self):
        state,_=self.run_script({'config':{}})
        self.assertNotIn('ownnumber', state['config'])


if __name__ == '__main__':
    unittest.main()
