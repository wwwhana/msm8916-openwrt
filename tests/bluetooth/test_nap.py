"""Run the sharing CLI against temporary UCI/service/controller fixtures."""
from pathlib import Path
import os
import subprocess
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[2]
CLI = ROOT / 'packages/bluez-network/files/bt-nap'

class SharingTests(unittest.TestCase):
    def test_share_stop_and_pending_edits(self):
        with tempfile.TemporaryDirectory() as tmp:
            base=Path(tmp)
            def executable(name,text):
                p=base/name;p.write_text('#!/bin/sh\n'+text);p.chmod(0o755);return p
            executable('uci', '''
[ "$1" != -q ] || shift
case "$1 $2" in
 'changes bt-nap') [ "$PENDING" != 1 ] || echo pending ;;
 'get bt-nap.main') echo sharing ;;
 set*) printf '%s' "$2" > "$TEST_DIR/saved" ;;
 'commit bt-nap') : ;;
esac
''')
            init=executable('init', '''
[ "$FAIL" != 1 ] || exit 1
printf '%s' "$1" > "$TEST_DIR/running"
''')
            executable('bluetoothctl', '''
case "$*" in
 '--timeout 6 power on')
  [ "$POWER_FAIL" != 1 ] || exit 1
  echo yes > "$TEST_DIR/powered"
  exit 0 ;;
esac
echo "Powered: $(cat "$TEST_DIR/powered" 2>/dev/null)"
[ "$(cat "$TEST_DIR/running" 2>/dev/null)" != start ] || echo 'UUID: 00001116-0000-1000-8000-00805f9b34fb'
''')
            executable('sleep', ':\n')
            cli=executable('bt-nap',CLI.read_text().replace('/etc/init.d/bt-nap',str(init)))
            env={**os.environ,'PATH':tmp+os.pathsep+os.environ['PATH'],'TEST_DIR':tmp}
            def run(command,**extra):
                return subprocess.run([str(cli),command],text=True,capture_output=True,env={**env,**extra})
            self.assertEqual(run('on').returncode,0)
            self.assertEqual((base/'saved').read_text(),'bt-nap.main.enabled=1')
            self.assertEqual(run('status').returncode,0)
            (base/'powered').write_text('no')
            self.assertNotEqual(run('status').returncode,0)
            self.assertEqual(run('on').returncode,0)
            self.assertEqual((base/'powered').read_text().strip(),'yes')
            self.assertEqual(run('off').returncode,0)
            self.assertEqual((base/'saved').read_text(),'bt-nap.main.enabled=0')
            self.assertNotEqual(run('status').returncode,0)
            self.assertNotEqual(run('on',PENDING='1').returncode,0)
            self.assertEqual((base/'saved').read_text(),'bt-nap.main.enabled=0')
            self.assertNotEqual(run('on',POWER_FAIL='1').returncode,0)
            self.assertEqual((base/'saved').read_text(),'bt-nap.main.enabled=0')
            self.assertNotEqual(run('on',FAIL='1').returncode,0)
            self.assertNotEqual(run('invalid').returncode,0)

if __name__ == '__main__': unittest.main()
