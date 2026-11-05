# A8 / HM-MiFiB-V18 — experimental port

The AliExpress **A8** examined here is a battery-powered USB-C pocket router
with an LCD and PCB marking **HM-MiFiB-V18**. The build target is
`mifib-v18`, the image profile is `generic-mifib-v18`, and the first DT
compatible is `mifib-v18,generic`. A8 is a sales name; this target does not
cover every product sold under that name.

**Status:** the complete OpenWrt build and offline image inspection passed.
Board support is ready for hardware testing. An OpenWrt boot, cellular
connection, charging test and recovery test on this A8 have not been verified.
V12's feature matrix does not establish working features on V18.

## Documentation reviewed before implementation

- [Adding a board](../ADDING_A_DEVICE_BOARD.md), [architecture](../ARCHITECTURE.md),
  [build guide](../BUILD_SYSTEM_GUIDE.md), [patches and packaging](../PATCHES_AND_PACKAGING.md),
  and [Docker setup](../Docker/README.md): edit the tracked BSP, register the
  DTB/profile, and inspect the produced image before installation.
- [V12 hardware](../MIFIB_V12/README.md), [battery](../BATTERY.md), and
  [power button](../POWER_BUTTON.md): reuse the matching power/SIM description,
  while keeping V12's measured lamp layout separate from the A8 LCD board.
- [Recovery](../RECOVERY/README.md) and
  [first-boot firmware extraction](../Sysupgrade/FIRST_BOOT_FIRMWARE_DUMP.md):
  preserve this unit's radio partitions and pair recovery with its own firmware,
  production kernel and matching modules.
- [Modem stability](../Modem%20Stability/README.md),
  [patch scope audit](../Modem%20Stability/PATCH_MODEM_REVISION_SCOPE_AUDIT.md),
  and [thermal findings](../Thermal/README.md): old modem binary patching advice
  was retracted. HMUF02 measurements do not establish A8 behaviour.

## Original firmware and backups

The supplied complete eMMC image has **7,634,944 sectors of 512 bytes**
(3,909,091,328 bytes), with 31 Android partitions. Its primary and backup
GPT headers and entry-array CRCs were checked. The separate 64 MiB FAT16 modem
image matches the modem partition in both complete backups.

| Local input | SHA-256 |
| --- | --- |
| `backup.bin` | `f4080e72b9d8256c70561257981b40010be488f4864f983038e2d383bc1ba0af` |
| `a8-emmc.img` | `4c5f7e1c01650691d4c6998e707659d78dff3d42f96f738bf1e7cc2d44fe79a1` |
| `modem.bin` | `be1d63f2266d4df0a7eaa67a944233d8928a724d1b1472b753345f8dbec5189b` |

The two complete backups have identical layouts and 28 matching partition
hashes; `persist`, `cache` and `userdata` differ. They are writable partitions,
but the exact reason for the differences was not established. The `system`
partition matches between both files; an earlier device-side hash differed,
and that discrepancy remains unresolved. **A successful restore has not been
tested.** Never replace this unit's NV/calibration with another unit's backup.

## DTB comparison

The original boot image carries a QCDT v3 table: 72 records referencing 23
distinct DTBs. Boot and recovery have identical DTB regions. The closest
match to the repository's V12 stock DTB is the MTP entry at **DT-region offset
`0x51000`**, with `qcom,board-id = <8 0>` and model `Qualcomm Technologies,
Inc. MSM 8916 MTP`. Both have **885 nodes**. Comparing node properties,
including phandles, found only three differences:

| Node/property | A8 difference from V12's stock DTB |
| --- | --- |
| `qcom,mdss_spi_gc9107_128128_cmd` / `qcom,mdss-spi-on-command` | Different 140-byte LCD initialization sequence |
| `rpm-regulator-ldoa6/regulator-l6` / `regulator-boot-on` | Present on A8 |
| Same L6 node / `regulator-always-on` | Present on A8 |

The compared V12 stock DTB has SHA-256
`f52928530a367590bb0b42ae2277191aafd4c73e58115e4072315386bc5301e6`.
This is strong evidence for reusing V12's board description. It does not
identify which DTB the original bootloader selected at runtime. The pack also
contains a 512 MB MTP variant with board-id `<8 0x100>`.

## Hardware: evidence and remaining questions

| Hardware | Evidence and limit |
| --- | --- |
| SoC / RAM | MSM8916 marking; original Android identifies `msm8916_32_512`. Confirm available RAM from the running port. |
| eMMC | SK hynix package; complete image and GPT establish the sector count above. |
| Battery | Stock profile: 4.2 V maximum, 3.35 V cutoff, 2150 mAh design capacity and 1 A charge current. These are firmware settings, not a measurement of the fitted cell. |
| L6 | Stock rail is 1.8 V with boot-on and always-on flags. Its consumers include display/PLL nodes and other stock nodes; it is not proven to power only the LCD. |
| Reset | Stock DT describes GPIO102, active low. Physical RST behaviour remains untested in OpenWrt. |
| WPS | Stock DT describes GPIO27, active low. V12's physical WPS button did not produce that GPIO event; A8 wiring is also unverified. No WPS key is enabled here. |
| SIM | Matching stock description for GPIO119/112/12 selection and GPIO114 re-announce. Registration and hot-swap remain untested. |
| LCD | Stock DT includes GC9107 and ST7735S descriptions. GC9107 is described as 128×128; the selected panel remains unconfirmed. Reset/DC are described on GPIO11/30. Display support is not implemented. |
| Wi-Fi bands | The initial mainline description selects `qcom,wcn3620`, inherited from V12. [Linux treats WCN3620 as 2.4 GHz only](https://github.com/torvalds/linux/blob/v6.12/drivers/net/wireless/ath/wcn36xx/main.c#L1345). This is a port configuration, not a physical RF-chip identification or a measured A8 channel list. |
| Bluetooth | The mainline DT contains the WCNSS Bluetooth device over SMD, and the image includes `btqcomsmd`, `btqca` and BlueZ. The A8 antenna arrangement, controller discovery, scanning and pairing remain unverified. |
| SD | Eight contact pads and mounting pads below the SIM socket suggest an unpopulated microSD socket. The separate round `D0`–`D3`/`CMD`/`CLK` pads are SD/MMC test points; whether they connect to eMMC or SDC2 is unverified. Stock SDC2 is enabled with a four-bit bus, but that alone does not prove the footprint is wired or supports removable cards. Check continuity, supplies and missing passives before fitting a socket. SDC2 stays disabled here. |

After booting OpenWrt, these read-only checks can record the wireless state;
they cannot run while the device is in EDL:

```sh
iw list
bluetoothctl list
dmesg | grep -E 'wcn36xx|wcnss|Bluetooth|btqcomsmd'
```

[`iw list`](https://wireless.docs.kernel.org/en/latest/en/users/documentation/iw.html#getting-device-capabilities)
reports the bands exposed by the current driver/configuration; it is not an
independent identification of the physical RF chip.
[`bluetoothctl list`](https://github.com/bluez/bluez/blob/master/doc/bluetoothctl.rst)
lists controllers known to BlueZ. An empty list also requires checking the
service, firmware and kernel messages; a listed controller still needs scanning
and pairing tests before Bluetooth can be marked working on this A8.

The PCB marking is a hardware revision, not a firmware version. Conflicting
case/board IMEI labels do not establish the chipset or board identity; use
the PCB, original firmware and runtime observations instead.

## Implementation

Patch **869** includes `msm8916-generic-mifib-v12.dts` and changes the board
identity, L6 power policy and unverified lamp outputs. The GPIO114 SIM
re-announce output remains. V12's status lamp nodes are removed and their pins
are not claimed by the LED pin group. No LCD or SD driver is enabled.

V12's battery, charger, reset key, SIM selection and modem A2 hold are
inherited. A2 hold is an initial compatibility choice, not an A8 stability
measurement. The separate `himi-ok` NV-write allow-list is unchanged: this new
board does not gain that write merely by inheriting V12's DTS.

The profile inherits V12's packages, **7,634,944-sector GPT**, and
`GPT_RECOVERY=1` (17 OpenWrt partitions). Patch 801 registers the new DTB.
`diffconfigs/mifib-v18` and the manual build workflow select the new profile.
The firmware generator selects GPIO102, active low, as the initial recovery
key mapping; this still requires a hardware check.

## Build and image inspection

For the A8 fork, use GitHub Actions **Board Build** (
[`board-build.yml`](../../.github/workflows/board-build.yml)), select the branch
containing this port, and enter `mifib-v18` as `device`. A successful manual
run retains an `experimental-mifib-v18-<commit>` artifact for 14 days. The
test workflow uses a disposable package signing key and does not publish a
release or package feed. It does not supply a V18 recovery image. The separate
**OpenWrt Builder** release-style workflow requires `APK_SIGNING_KEY`.

From the repository root, the normal entry point is:

```sh
./build.sh build mifib-v18
```

Follow the [build guide](../BUILD_SYSTEM_GUIDE.md) and local resource/locking
rules. The script's default parallelism is the builder CPU count plus one;
use an appropriately limited builder on a shared machine. Avoid `clean` when
continuing an existing build.

Expected files share the prefix
`msm8916-openwrt-msm89xx-msm8916-generic-mifib-v18-`, with suffixes
`squashfs-boot.img`, `squashfs-system.img`, `squashfs-sysupgrade.bin`,
`squashfs-gpt_both0.bin`, `flash.sh` and `firmware.zip`.
Check the actual boot DTB, upgrade metadata and GPT rather than filenames
alone. The first compatible must be `mifib-v18,generic`.

The first complete build, [run 42040234977](https://github.com/wwwhana/msm8916-openwrt/actions/runs/42040234977)
at `ce14a4f`, passed after about 1 hour 55 minutes. Its original upload pattern
retained the four production images and package manifest, but missed the
installer and firmware ZIP. These two files were built separately by
[run 42044504211](https://github.com/wwwhana/msm8916-openwrt/actions/runs/42044504211)
at `23d3beb`. The board, package and kernel inputs are unchanged between these
commits. The later workflow fixes the upload pattern and checks all required
outputs. Download both artifacts when using this first build.

The combined local bundle is `../artifacts/` relative to this repository.
Its six public build files and package manifest retain their original names.
`build-info.json` records source runs, file sizes, SHA-256 values and offline
checks; `SHA256SUMS` also covers the private recovery image and this record.
`saved/` and `recovery-mifib-v18.img` contain this unit's private radio data
and are kept locally, outside Git and GitHub artifacts.

For a bootloader-only rebuild, select the same board and set
`bootloader_only=true` in Board Build. This uses the existing firmware
generator and Docker builder, without rebuilding OpenWrt. The separate
`experimental-mifib-v18-bootloader-<commit>` artifact contains only the ZIP
and installer; production images and a matching recovery image are still
required for migration. The workflow checks required output files before
uploading, and `flash.sh`/`firmware.zip` names do not contain `-squashfs-`.

`firmware.zip` includes all five boot-chain components: `sbl1.mbn`,
`rpm.mbn`, `tz.mbn`, `hyp.mbn` (qhypstub) and `aboot.mbn` (the compiled
lk2nd/lk1st build). It is part of the migration, not a replacement for the
production `boot.img` or the separate rescue image. Check the actual ZIP
contents and component sizes against the target GPT before installation.

The bundle replaces the A8's original boot chain. Its `sbl1`, `rpm` and `tz`
come from the project's DragonBoard 410c Linux-176 firmware package, not this
unit's backup; `aboot` and `hyp` are newly built. The downloaded `tz.mbn` is
605,312 bytes: larger than Android's 524,288-byte `tz` partition, but within
the new 1,048,576-byte partition. Do not write this bundle using the Android
GPT. Matching entry addresses or fitting the new partitions does not verify
that this boot chain starts on V18; that hardware check remains outstanding.

## Installation and recovery constraints

**Android and OpenWrt use different partition layouts.** Android's `boot` is
partition 22 and `modemst2` is partition 14; OpenWrt uses partition 13 for
`boot` and 14 for `rootfs`. Writing an OpenWrt rootfs to Android partition 14
destroys radio NV. Installation therefore needs the complete reviewed
migration in [the main README](../../README.md), a V18 bundle and this unit's
own saved radio data. Writing a boot image alone is not an installation.

The original 32 MiB boot/recovery sizes are not the OpenWrt layout: the new
layout uses 64 MiB for boot and 16 MiB for recovery. Migration also replaces
bootloader components. Do not reuse Android's partition numbers or infer
compatibility from eMMC capacity alone.

No hardware-verified V18 recovery image is published by this change.
`build-recovery.sh mifib-v18` discovers the new profile automatically, but
requires its production boot image, matching modules and private
`msm89xx/recovery/blobs/mifib-v18/` inputs. Keep unit-specific calibration out
of Git. **Do not rename V12's frozen recovery image to V18**: it carries a V12
DTB and that unit's Wi-Fi calibration. The release workflow's frozen-image
allow-list stays limited to its existing verified boards.

After installing both the patched bootloader and a matching V18 recovery
image, the intended button entry is: power off completely, hold **RST**,
then turn the device on while keeping RST held for about two seconds.
With a battery fitted, disconnecting USB alone may leave the device powered. The bootloader maps the initial
GPIO102 active-low choice to the recovery key and also enables recovery after
three failed boots (`BOOT_FAILOVER_COUNT=3`). Physical button entry and
automatic failover on this A8 remain unverified; the setting alone is not a
successful recovery test.

Local recovery preparation extracted this unit's nine WCNSS firmware files
from `modem.bin`. The 29,816-byte Wi-Fi NV file extracted from `persist` is
identical in both supplied complete backups, even though the complete
`persist` partitions differ. These inputs stay in the git-ignored
`msm89xx/recovery/blobs/mifib-v18/` directory. Hostapd, iw, their libraries
and all eight recovery modules were taken from this build's production
rootfs; the kernel payload was copied verbatim from its production boot
image. The existing recovery builder ran in a temporary staging tree,
without changing the tracked frozen recovery files.

The resulting private `../artifacts/recovery-mifib-v18.img` is **11,913,216
bytes (11.36 MiB)**, within the new 16 MiB recovery partition. Its SHA-256 is
`bde1ada6fe193391ea8c9b65c76f6cdf0c47b5fe9808a4b4854a09dd4791afa8`.
Offline checks verified the packed kernel, initramfs contents, module release
and origin, executable/library dependencies, and byte identity of this unit's
firmware and Wi-Fi calibration. Hostapd, iw and Dropbear version/help commands
also ran in the ARM64 inspection container. Booting this image, its USB/Wi-Fi
interfaces and the RST recovery path still require device tests. Private
calibration is not part of the GitHub Actions artifact.

First-boot firmware extraction uses the preserved original `modem` and
`persist` partitions. Early missing-firmware messages can precede extraction;
consult [the first-boot guide](../Sysupgrade/FIRST_BOOT_FIRMWARE_DUMP.md) before
diagnosing permanent radio failure.

## Radio data preservation

The original radio partitions move during migration. These are **512-byte
sector addresses**, obtained from the original backup and the generated V18
GPT; they are not commands to write raw sectors by hand.

| Partition | Android start LBA | OpenWrt start LBA | Size in sectors |
| --- | ---: | ---: | ---: |
| `fsc` | 540672 | 4096 | 2 |
| `fsg` | 655424 | 4098 | 3072 |
| `modem` | 131072 | 7170 | 131072 |
| `modemst1` | 532480 | 138242 | 3072 |
| `modemst2` | 535552 | 141314 | 3072 |
| `persist` | 2490368 | 144386 | 65536 |
| `sec` | 658496 | 209922 | 32 |

The local installation folder's `saved/` directory is pre-populated from
this unit's EDL `backup.bin`: all seven files have the recorded partition
lengths and SHA-256 values, totaling 105,399,296 bytes. These are private
installation inputs, not public build artifacts. The original complete
backups remain in the parent A8 directory. Comparison with the connected
device has not been run in this build phase. A mismatch must be investigated;
do not use `--refresh-backup` merely to force past it.

The installation script now enforces this sequence:

1. Require all seven original GPT lengths. Read each radio partition to
   `saved/` and require the exact length, then read again and compare every
   byte. Existing complete backups are retained and compared with the current
   device too; a filename or nonempty file alone is insufficient.
2. Before GPT writes, validate the target headers, entry CRCs/copies, bounds
   and non-overlapping regions. Require all seven radio sizes to remain
   unchanged and match the backups. A required recovery image must exist for
   this board. Another board's sole image is not accepted as a fallback;
   ambiguous matching candidates are rejected unless the exact release
   filename selects one.
3. After changing GPT, require EDL's reported partition locations and sizes
   to match the target before bootloader/radio writes use partition names.
4. Restore each radio partition and immediately read it back. A failed read,
   short read, failed write or byte mismatch prevents subsequent partition
   writes, OpenWrt image writes and reboot. `modem` and `persist` are checked
   along with the smaller NV partitions.

`--yes` does not bypass these checks. A normal `repartition` operation must
keep radio/boot regions unchanged; it may only shrink the overlay tail for
`misc` and `recovery`.

These are installer checks. During normal OpenWrt operation,
[`rmtfs.init`](../../packages/rmtfs/files/rmtfs.init) creates partition-label
links from sysfs and starts `rmtfs -P -s`; modem storage resolves
`modemst1`/`modemst2` by label using the
[pinned upstream storage implementation](https://github.com/linux-msm/rmtfs/blob/586372e575f5cc1a7cbb170219ab6df98f394cae/storage.c).
The normal service permits NV writes. Preserve the original backups for
runtime recovery too; passing migration checks does not establish that modem
operation on this A8 is safe or stable.

If a migration is interrupted, keep the complete backup and `saved/` files
unchanged and remain in EDL. Reusing `saved/` will stop if the current radio
contents differ; it will not silently replace the good backup with the
partially written device state. Recovery then requires a reviewed restore
using this unit's known-good backup. These checks reduce preventable errors;
power loss and bootloader compatibility still require recovery planning and
hardware tests.

The [migration tests](../../tests/flash-radio/test_flash_radio.py) run the real
script functions against a file-backed EDL substitute. They cover short and
failed reads, stale/foreign backups, changed target sizes, bad CRCs, overlapping
partitions, a stale device GPT, restore corruption/failure, and missing,
foreign or ambiguous recovery images.
Run them with `python3 tests/flash-radio/test_flash_radio.py`; they require no
device and perform no real EDL operations. They are also included in CI.

## Verification record

| Check | Result |
| --- | --- |
| Board-profile checks, including the harness self-check on Linux | Passed: 54 checks; zero failures; three skips |
| Shell syntax of the changed firmware generator and installer | Passed |
| GNU Make evaluation of V18 profile inheritance | Passed; correct identity, capacity, recovery layout and battery package; recovery flag resets for the next board |
| Patches 801 and 869 | Passed both `git apply --check` and zero-fuzz `patch --dry-run` |
| Standalone DTB compile on Linux 6.12.94 DTS inputs | Passed; 12 relevant shared/board DTS patch subsets applied with zero fuzz |
| DTB extracted from the actual production boot image | 54,998 bytes, 461 nodes; byte-identical to the standalone compile; V18 identity, L6 at 1.8 V with both power flags, reset GPIO102 active-low, 4.2 V battery profile, only SIM pulse LED output, SDC2 disabled |
| Generated GPT against original A8 radio sizes/offsets | Passed using backup metadata and sparse test files; no radio contents or device were written |
| Radio migration failure/success tests | Passed: 22 tests on both Linux and macOS |
| GitHub Actions test suites for `ce14a4f` | [Passed](https://github.com/wwwhana/msm8916-openwrt/actions/runs/42040239138): modem guard, 22 radio migration tests, and 54 board-profile checks |
| GitHub Actions bootloader build for `23d3beb` | [Passed](https://github.com/wwwhana/msm8916-openwrt/actions/runs/42044504211); downloaded artifact digest and ZIP CRCs match, all five components fit the V18 GPT, installer matches tested source, and aboot contains recovery-key/failover/EDL code markers |
| Complete OpenWrt build | [Passed on GitHub Actions](https://github.com/wwwhana/msm8916-openwrt/actions/runs/42040234977) for `ce14a4f`; Linux 6.12.112, GCC 14.3.0 |
| Downloaded production artifact | GitHub archive SHA-256 and ZIP CRCs match; Android boot header, V18 DTB, firmware metadata/CRC and both GPT header/entry-array CRCs passed |
| Actual image sizes against the generated GPT | Boot 6,603,036 bytes / 64 MiB; system 28,364,763 bytes / 128 MiB; private recovery 11,913,216 bytes / 16 MiB; all fit |
| Sysupgrade content against standalone images | Kernel byte-identical; rootfs bytes identical with the expected 321 zero bytes added to a 1 KiB boundary by OpenWrt's sysupgrade packer |
| Actual generated GPT with the installer's radio preflight | Passed against original A8 layout metadata and sparse files; all seven radio lengths preserved, `modemst2` moves to partition 5 at LBA 141314 |
| Private V18 recovery build and offline inspection | Passed; production kernel and eight matching modules, this unit's firmware/NV, initramfs contents and userspace library checks; 11.36 MiB image |
| Combined local bundle with the real installer | Correct V18 GPT, boot, system and private recovery files selected; radio preflight passed against original GPT metadata and actual local `saved/` file lengths, with device access blocked |
| Device boot, USB network, Wi-Fi, LTE traffic | Not run |
| Charging, temperature, reset/recovery, cold boots, traffic soak | Not run |
| LCD, removable SD, WPS and SIM hot-swap | Unverified; LCD/SD/WPS support not enabled |

The actual production DTB is 54,998 bytes, SHA-256
`0bd0e5936294b2331b4e7d17671c86a6b54dc59b3e2bb2071b3a50d69d4a856d`.
It matches the earlier standalone compile. The V12 baseline and V18 standalone
compile both report the same four warnings in shared upstream DTS nodes.
Production GPT GUIDs differ from the standalone generator run, so file hashes
are not interchangeable; both layouts passed the header/array CRC, partition
bounds and radio-size checks. The inspected production files and their current
hashes are in the local bundle's `build-info.json` and `SHA256SUMS`.

Use [the board sign-off checklist](../../tests/board-profile/signoff.sh) to
record runtime results before calling this a hardware-supported board.
Build success alone is not device validation.
