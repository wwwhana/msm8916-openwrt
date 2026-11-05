#!/usr/bin/env python3
"""Exercise the real migration functions against files; never access USB/EDL."""
import os
from pathlib import Path
import struct
import subprocess
import tempfile
import unittest
import zlib

FLASH = Path(__file__).resolve().parents[2] / "msm89xx/image/flash.sh"
RADIO = "fsc fsg modemst1 modemst2 modem persist sec".split()
NAMES = RADIO + "hyp rpm sbl1 tz aboot boot rootfs rootfs_data misc recovery".split()


def layout(start):
    return {name: (start + 4 * i, 2) for i, name in enumerate(NAMES)}


def dump(parts):
    return "\n".join(f"{n}: Offset 0x{s*512:016x}, Length 0x{c*512:016x}, Flags 0x0,"
                     for n, (s, c) in parts.items()) + "\n"


def gpt(parts):
    entries = bytearray(128 * 128)
    for i, (name, (start, count)) in enumerate(parts.items()):
        off = i * 128
        entries[off:off + 32] = b"T" * 32
        struct.pack_into("<QQ", entries, off + 32, start, start + count - 1)
        encoded = name.encode("utf-16le")
        entries[off + 56:off + 56 + len(encoded)] = encoded

    def header(my_lba, alt, entry_lba):
        h = bytearray(512)
        struct.pack_into("<8sIIIIQQQQ16sQIII", h, 0, b"EFI PART", 0x10000,
                         92, 0, 0, my_lba, alt, 34, 4062, b"G" * 16,
                         entry_lba, 128, 128, zlib.crc32(entries))
        struct.pack_into("<I", h, 16, zlib.crc32(h[:92]))
        return h

    return bytes(512) + header(1, 4095, 2) + entries + entries + header(4095, 1, 4063)


MOCK_EDL = '''#!/usr/bin/env python3
import os, pathlib, sys
t = pathlib.Path(os.environ["T"])
cmd, *args = sys.argv[1:]
name = args[0] if args else ""
with (t / "calls").open("a") as f: f.write(cmd + " " + name + "\\n")
if cmd == "r":
    counter = t / ("reads-" + name)
    count = int(counter.read_text()) + 1 if counter.exists() else 1
    counter.write_text(str(count))
    if os.environ.get("FAIL_READ") == name + ":" + str(count): sys.exit(1)
    data = (t / "device" / name).read_bytes()
    if os.environ.get("TRUNC_READ") == name + ":" + str(count): data = data[:-1]
    pathlib.Path(args[1]).write_bytes(data)
elif cmd == "w":
    if os.environ.get("FAIL_WRITE") == name: sys.exit(1)
    data = pathlib.Path(args[1]).read_bytes()
    if os.environ.get("CORRUPT_WRITE") == name: data = bytes([data[0] ^ 255]) + data[1:]
    (t / "device" / name).write_bytes(data)
elif cmd == "ws":
    (t / "gpt-written").touch()
elif cmd == "printgpt":
    new = (t / "gpt-written").exists() and not os.environ.get("STALE_GPT")
    print((t / ("target.txt" if new else "current.txt")).read_text())
elif cmd != "reset":
    sys.exit("unexpected mock EDL command")
'''


class RadioMigrationTests(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory(prefix="flash-radio-")
        self.addCleanup(self.tmp.cleanup)
        self.t = Path(self.tmp.name)
        (self.t / "device").mkdir()
        (self.t / "bin").mkdir()
        for i, name in enumerate(RADIO):
            (self.t / "device" / name).write_bytes(bytes([65 + i]) * 1024)
        (self.t / "image").write_bytes(b"image")
        self.target = layout(512)
        (self.t / "current.txt").write_text(dump(layout(128)))
        self.save_target()
        mock = self.t / "bin/edl"
        mock.write_text(MOCK_EDL)
        mock.chmod(0o755)

    def save_target(self):
        (self.t / "target.bin").write_bytes(gpt(self.target))
        (self.t / "target.txt").write_text(dump(self.target))

    def saved(self):
        (self.t / "saved").mkdir()
        for n in RADIO:
            (self.t / "saved" / (n + ".bin")).write_bytes((self.t / "device" / n).read_bytes())

    def run_flash(self, action="do_migrate", **extra):
        env = dict(os.environ, T=str(self.t), PATH=str(self.t / "bin") + os.pathsep + os.environ["PATH"])
        env.update(extra)
        script = '''source "$1"
MODE=migrate
GPT_DUMP="$(cat "$T/current.txt")"
gpt_path="$T/target.bin"
TOT_SECTORS=4096
detect_images_migrate() { :; }
encourage_backup() { :; }
extract_firmware() { :; }
verify_mbn() { :; }
confirm_migrate() { :; }
zero_misc() { :; }
flash_bootloader() { edl w aboot "$T/image"; }
flash_boot_rootfs() { edl w boot "$T/image"; edl w rootfs "$T/image"; }
flash_recovery() { edl w recovery "$T/image"; }
wipe_rootfs_data() { :; }
sleep() { :; }
''' + action
        return subprocess.run(["bash", "-c", script, "test", str(FLASH)], cwd=self.t,
                              env=env, text=True, stdout=subprocess.PIPE, stderr=subprocess.STDOUT)

    def calls(self):
        p = self.t / "calls"
        return p.read_text().splitlines() if p.exists() else []

    def no_writes(self, result):
        self.assertNotEqual(result.returncode, 0, result.stdout)
        self.assertFalse(any(c.startswith(("w ", "ws ", "reset ")) for c in self.calls()), self.calls())

    def test_success_checks_all_backups_before_gpt_and_every_restore_before_next_write(self):
        result = self.run_flash()
        self.assertEqual(result.returncode, 0, result.stdout)
        calls = self.calls()
        first_write = next(i for i, c in enumerate(calls) if c.startswith("ws "))
        self.assertEqual(calls[:first_write], ["r " + n for n in RADIO] * 2)
        for n in RADIO:
            i = calls.index("w " + n)
            self.assertEqual(calls[i + 1], "r " + n)
            self.assertEqual((self.t / "saved" / (n + ".bin")).read_bytes(),
                             (self.t / "device" / n).read_bytes())
        self.assertEqual(calls[-1], "reset ")

    def test_truncated_nonempty_backup_stops_before_gpt(self):
        self.no_writes(self.run_flash(TRUNC_READ="modemst2:1"))

    def test_second_read_failure_stops_before_gpt(self):
        self.no_writes(self.run_flash(FAIL_READ="modemst2:2"))

    def test_changed_second_read_stops_before_gpt(self):
        self.no_writes(self.run_flash(TRUNC_READ="modemst2:2"))

    def test_existing_foreign_backup_is_preserved_and_rejected(self):
        self.saved()
        p = self.t / "saved/modemst2.bin"
        p.write_bytes(b"foreign!" * 128)
        self.no_writes(self.run_flash())
        self.assertEqual(p.read_bytes(), b"foreign!" * 128)

    def test_existing_matching_backup_is_verified_and_reused(self):
        self.saved()
        result = self.run_flash()
        self.assertEqual(result.returncode, 0, result.stdout)
        self.assertEqual(self.calls().count("r modemst2"), 2)

    def test_truncated_restore_input_is_rejected_before_any_write(self):
        self.saved()
        (self.t / "saved/modemst2.bin").write_bytes(b"short")
        self.no_writes(self.run_flash("restore_radio"))

    def test_missing_gpt_length_stops_before_backup_or_write(self):
        p = self.t / "current.txt"
        p.write_text("\n".join(s for s in p.read_text().splitlines() if not s.startswith("modemst2:")))
        self.no_writes(self.run_flash())
        self.assertEqual(self.calls(), [])

    def test_smaller_target_radio_partition_stops_before_gpt(self):
        start, _ = self.target["modemst2"]
        self.target["modemst2"] = (start, 1)
        self.save_target()
        self.no_writes(self.run_flash())

    def test_invalid_target_crc_stops_before_gpt(self):
        p = self.t / "target.bin"
        data = bytearray(p.read_bytes())
        data[512 + 56] ^= 1
        p.write_bytes(data)
        self.no_writes(self.run_flash())

    def test_overlapping_target_stops_before_gpt(self):
        self.target["rootfs"] = self.target["modemst2"]
        self.save_target()
        self.no_writes(self.run_flash())

    def test_repartition_cannot_move_radio(self):
        self.no_writes(self.run_flash("MODE=repartition; flash_gpt"))

    def test_valid_repartition_keeps_radio_and_boot_regions(self):
        current = dict(self.target)
        start, count = current["rootfs_data"]
        current["rootfs_data"] = (start, count + 1)
        (self.t / "current.txt").write_text(dump(current))
        result = self.run_flash("MODE=repartition; flash_gpt")
        self.assertEqual(result.returncode, 0, result.stdout)
        self.assertEqual(len(self.calls()), 3)
        self.assertTrue(all(c.startswith("ws ") for c in self.calls()))

    def test_missing_recovery_image_rejects_migration_before_device_writes(self):
        prefix = "test-generic-mifib-v18-squashfs-"
        for suffix in ("boot.img", "system.img"):
            (self.t / (prefix + suffix)).write_bytes(b"image")
        (self.t / (prefix + "gpt_both0.bin")).write_bytes(gpt(self.target))
        self.no_writes(self.run_flash('source "$1"; BOARD_TOKEN=mifib-v18; detect_images_migrate'))

    def test_foreign_recovery_cannot_satisfy_v18_migration(self):
        (self.t / "recovery-mifib-v12.img").write_bytes(b"wrong-board")
        self.test_missing_recovery_image_rejects_migration_before_device_writes()

    def test_ambiguous_matching_recovery_images_are_rejected(self):
        for suffix in ("test", "other"):
            (self.t / ("recovery-mifib-v18-" + suffix + ".img")).write_bytes(b"image")
        result = self.run_flash('BOARD_TOKEN=mifib-v18; find_optional_image . "*recovery*.img" recovery-mifib-v18.img')
        self.no_writes(result)
        self.assertIn("multiple recovery images", result.stdout)

    def test_exact_matching_recovery_wins_over_foreign_images(self):
        for board in ("mifib-v12", "mifib-v18"):
            (self.t / ("recovery-" + board + ".img")).write_bytes(b"image")
        result = self.run_flash('BOARD_TOKEN=mifib-v18; find_optional_image . "*recovery*.img" recovery-mifib-v18.img')
        self.assertEqual(result.returncode, 0, result.stdout)
        self.assertEqual(result.stdout.strip(), "./recovery-mifib-v18.img")
        self.assertEqual(self.calls(), [])

    def test_stale_device_gpt_stops_before_partition_writes(self):
        result = self.run_flash(STALE_GPT="1")
        self.assertNotEqual(result.returncode, 0, result.stdout)
        self.assertTrue(any(c.startswith("ws ") for c in self.calls()))
        self.assertFalse(any(c.startswith(("w ", "reset ")) for c in self.calls()))

    def test_restore_corruption_stops_before_next_partition_or_boot(self):
        result = self.run_flash(CORRUPT_WRITE="modemst2")
        self.assertNotEqual(result.returncode, 0, result.stdout)
        self.assertNotIn("w modem", self.calls())
        self.assertNotIn("w boot", self.calls())
        self.assertNotIn("reset ", self.calls())

    def test_restore_read_failure_is_fatal_instead_of_skipped(self):
        result = self.run_flash(FAIL_READ="modemst2:3")
        self.assertNotEqual(result.returncode, 0, result.stdout)
        self.assertNotIn("w modem", self.calls())
        self.assertNotIn("reset ", self.calls())

    def test_large_persist_partition_is_also_verified(self):
        result = self.run_flash(CORRUPT_WRITE="persist")
        self.assertNotEqual(result.returncode, 0, result.stdout)
        self.assertNotIn("w sec", self.calls())
        self.assertNotIn("w boot", self.calls())

    def test_restore_write_failure_stops_after_retries(self):
        result = self.run_flash(FAIL_WRITE="modemst2")
        self.assertNotEqual(result.returncode, 0, result.stdout)
        self.assertEqual(self.calls().count("w modemst2"), 3)
        self.assertNotIn("w modem", self.calls())
        self.assertNotIn("reset ", self.calls())


if __name__ == "__main__":
    unittest.main()
