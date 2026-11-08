#!/usr/bin/env python3
"""Exercise the actual recovery packer with small, non-device fixtures."""
import gzip
import os
from pathlib import Path
import shutil
import struct
import subprocess
import sys
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[2]
WIRELESS = 'mdt_loader qcom_common qcom_pil_info qcom_wcnss_pil compat cfg80211 mac80211 wcn36xx'.split()
LCD = 'drm_panel_orientation_quirks drm drm_kms_helper drm_dma_helper drm_mipi_dbi panel-mipi-dbi'.split()


def cpio_files(data):
    files, offset = {}, 0
    while True:
        header = data[offset:offset + 110]
        if header[:6] != b'070701':
            raise ValueError('invalid newc header')
        fields = [int(header[i:i + 8], 16) for i in range(6, 110, 8)]
        size, namesize = fields[6], fields[11]
        offset += 110
        name = data[offset:offset + namesize - 1].decode().removeprefix('./')
        offset = (offset + namesize + 3) & ~3
        if name == 'TRAILER!!!':
            return files
        files[name] = (fields[1], data[offset:offset + size])
        offset = (offset + size + 3) & ~3


@unittest.skipUnless(sys.platform.startswith('linux') and shutil.which('cpio'), 'GNU cpio/Linux required')
class RecoveryTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.recovery = self.root / 'msm89xx/recovery'
        for relative in ('irfs/lib', 'irfs/etc', 'irfs/usr/sbin', 'blobs/mifib-v18/lib/firmware',
                         'blobs/mifib-v12/lib/firmware'):
            (self.recovery / relative).mkdir(parents=True)
        for relative in ('build-recovery.sh', 'msm89xx/recovery/gen-splash.py',
                         'msm89xx/recovery/irfs/init', 'msm89xx/recovery/irfs/usr/sbin/recovery-display',
                         'msm89xx/image/lk2nd-a8/tux.h', 'packages/a8-display/gen-panel.py',
                         'packages/a8-display/files/stock-init.hex'):
            target = self.root / relative
            target.parent.mkdir(parents=True, exist_ok=True)
            shutil.copy2(ROOT / relative, target)
        (self.root / 'msm89xx/image/msm8916.mk').write_text(
            'DEVICE_DTS := msm8916-generic-mifib-v18\nDEVICE_DTS := msm8916-generic-mifib-v12\n')
        self.modules = self.root / 'modules'
        self.modules.mkdir()
        for name in WIRELESS + LCD:
            (self.modules / (name + '.ko')).write_bytes(b'vermagic=6.12.112 test\0')
        # This marker deliberately stands in for private, per-board calibration.
        for board in ('mifib-v18', 'mifib-v12'):
            (self.recovery / f'blobs/{board}/lib/firmware/board-nv').write_text(board)
        self.kernel = gzip.compress(b'Linux version 6.12.112 fixture\0' + bytes(8192))
        header = bytearray(2048)
        struct.pack_into('<8s10I', header, 0, b'ANDROID!', len(self.kernel), 0, 0, 0, 0, 0, 0, 2048, 0, 0)
        header[64:64 + 12] = b'console=tty0'
        self.boot = self.root / 'boot.img'
        self.boot.write_bytes(header + self.kernel + bytes(-len(self.kernel) % 2048))

    def build(self, board, success=True):
        result = subprocess.run(['bash', str(self.root / 'build-recovery.sh'), board],
            env={**os.environ, 'RECOVERY_BOOT_IMG': str(self.boot), 'RECOVERY_MODULE_DIR': str(self.modules)},
            capture_output=True, text=True)
        self.assertEqual(result.returncode == 0, success, result.stdout + result.stderr)
        if not success:
            return
        image = (self.recovery / (board + '.img')).read_bytes()
        ksize, rsize, page = struct.unpack_from('<I', image, 8)[0], struct.unpack_from('<I', image, 16)[0], 2048
        self.assertEqual(image[page:page + ksize], self.kernel)
        ramoffset = page + (ksize + page - 1) // page * page
        return image[64:576].split(b'\0')[0].decode(), cpio_files(gzip.decompress(image[ramoffset:ramoffset + rsize]))

    def test_lcd_image_and_tracked_inputs(self):
        before = (self.recovery / 'irfs/init').read_bytes()
        cmdline, files = self.build('mifib-v18')
        self.assertIn('fbcon=font:VGA8x16', cmdline)
        self.assertNotIn('console=tty0', cmdline)
        for name in WIRELESS + LCD:
            self.assertIn('lib/modules/6.12.112/' + name + '.ko', files)
        self.assertTrue(files['usr/sbin/recovery-display'][0] & 0o111)
        self.assertTrue(files['etc/recovery.ppm'][1].startswith(b'P6\n128 96\n255\n'))
        self.assertTrue(files['lib/firmware/mifib-v18,gc9107.bin'][1].startswith(b'MIPI DBI'))
        self.assertEqual(files['lib/firmware/board-nv'][1], b'mifib-v18')
        self.assertEqual((self.recovery / 'irfs/init').read_bytes(), before)
        self.assertFalse((self.recovery / 'irfs/lib/modules').exists())
        self.assertFalse((self.recovery / 'irfs/lib/firmware').exists())

    def test_led_board_does_not_require_lcd_modules(self):
        for name in LCD:
            (self.modules / (name + '.ko')).unlink()
        cmdline, files = self.build('mifib-v12')
        self.assertNotIn('fbcon=', cmdline)
        self.assertNotIn('etc/recovery.ppm', files)
        self.assertEqual(files['lib/firmware/board-nv'][1], b'mifib-v12')

    def test_lcd_board_refuses_incomplete_display_modules(self):
        (self.modules / 'drm_mipi_dbi.ko').unlink()
        self.build('mifib-v18', success=False)
        self.assertFalse((self.recovery / 'mifib-v18.img').exists())


if __name__ == '__main__':
    unittest.main()
