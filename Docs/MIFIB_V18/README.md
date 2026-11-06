# A8 / HM-MiFiB-V18 — experimental port

The AliExpress **A8** examined here is a battery-powered USB-C pocket router
with an LCD and PCB marking **HM-MiFiB-V18**. The build target is
`mifib-v18`, the image profile is `generic-mifib-v18`, and the first DT
compatible is `mifib-v18,generic`. A8 is a sales name; this target does not
cover every product sold under that name.

**Installed baseline:** the first complete OpenWrt build, offline inspection
and EDL installation passed. All 20 final device read-back checks matched,
including all seven radio partitions. The user subsequently reported running
OpenWrt at `192.168.8.1`.
Agent SSH access to that address timed out. Cellular operation, charging and
recovery on this A8 have not been verified.

**Installed update, 2026-11-06:** complete image build
[42187058705](https://github.com/wwwhana/msm8916-openwrt/actions/runs/42187058705)
and [tests 42187062701](https://github.com/wwwhana/msm8916-openwrt/actions/runs/42187062701)
passed at `0c75e22`. The downloaded image includes `a8-display` **1.0-r5**,
LuCI display/button choices, ADB, APN profiles and the menu-enabled aboot.
Offline inspection and 13 checks in that image's ARM64 userspace passed.
EDL writes to aboot, boot and rootfs were read back successfully; all seven
radio partition hashes and both GPT copies stayed unchanged. Overlay and
the existing recovery were preserved.

The A8 subsequently exposed USB ACM and provided a root OpenWrt console:
Linux **6.12.112**, board `mifib-v18,generic`, `adbd` and `a8-display` present.
The 128×128 RGB565 framebuffer, backlight brightness 1, display services and
power input were registered. **The user sees a white LCD**, so visible menus
and physical navigation initially remained unverified. ADB failed to enumerate
because this BusyBox rejects `sleep 0.1`; the manager now uses the available
`usleep`. The source fix passes 13 USB checks. Applying it through Recovery's
console to the production overlay, verifying its SHA-256 and rebooting into
normal OpenWrt produced a working root ADB shell and file transfer.

Live LCD diagnosis found GPIO11 held **low** after initialization. Linux 6.12's
[`mipi_dbi_hw_reset()`](https://github.com/torvalds/linux/blob/v6.12/drivers/gpu/drm/drm_mipi_dbi.c#L654)
sets logical 0 then 1. The original `GPIO_ACTIVE_LOW` descriptor inverted that
sequence and kept the physical active-low reset asserted. Patch 869 now uses
`GPIO_ACTIVE_HIGH` for this driver's convention. Releasing GPIO11 high,
replaying the stock initialization through the driver's debug command interface
and redrawing produced **visible text, confirmed by the owner**. This live
diagnosis is temporary until the corrected DTB is installed; boot-menu output
and physical button navigation still need checking.
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
| LCD | The compared MTP DTB prefers the 128×128 GC9107 through `qcom,spi-pref-prim-pan`; it also contains an ST7735S description. SPI5, GPIO11/30 reset/DC, L17/L6 supplies and the MPP4 current sink are described below. Experimental display support is implemented; actual pixels remain unverified. |
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
are not claimed by the LED pin group. The A8 profile additionally selects
`a8-display` for the stock SPI LCD. SDC2 remains disabled.

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

The installed update is stored separately in `../artifacts/update/`.
Its seven downloaded files, `build-info.json` and `SHA256SUMS` record source
`0c75e22`, Actions artifact `13494680704`, image/component hashes and runtime
checks. The downloaded archive SHA-256 is
`0dc28532798788bcb88ed7841d1fa9471754541617a27042185d54a95332ecfc`.
It contains no private radio backup or new recovery image. Installation kept
the existing GPT and private recovery, with a verified update to only
aboot/boot/rootfs. Before-write snapshots of those three partitions are kept
in `../analysis/openwrt-port/update-*-before.bin`. The migration installer is
not the update procedure for this already migrated unit.

For a bootloader-only rebuild, select the same board and set
`bootloader_only=true` in Board Build. This uses the existing firmware
generator and Docker builder, without rebuilding OpenWrt. The separate
`experimental-mifib-v18-bootloader-<commit>` artifact contains only the ZIP
and installer; production images and a matching recovery image are still
required for migration. The workflow checks required output files before
uploading, and `flash.sh`/`firmware.zip` names do not contain `-squashfs-`.

The font-index follow-up at `215dc88` passed bootloader-only
[run 42204398538](https://github.com/wwwhana/msm8916-openwrt/actions/runs/42204398538).
Its inspected bundle is `../artifacts/bootloader/`: aboot is 337,696 bytes,
SHA-256 `1a4d7f90850147c9c1937466d6e19d1832f961486b4333b6473af5e41e703f3f`.
The other four components match the installed bundle byte for byte.
This follow-up has not been installed; the installed full image is `0c75e22`.

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
that every boot-chain feature works on V18. This chain has now reached the
OpenWrt root console; visible boot-menu output remains unverified.

## LCD support

The A8 profile uses Linux's existing
[`panel-mipi-dbi` driver](https://github.com/torvalds/linux/blob/v6.12/drivers/gpu/drm/tiny/panel-mipi-dbi.c)
and the OpenWrt `kmod-drm-panel-mipi-dbi` package. It does not need the older
GC9107 driver in `TBR/`. That driver's MF68E initialization, address offsets
and PWM backlight setup differ from this A8's stock description.

The compared stock DTB establishes the following configuration:

| Signal | Stock setting used in patch 869 |
| --- | --- |
| Panel | Preferred GC9107; 128×128, RGB565, address window 0–127 on both axes |
| SPI | QUP at `0x78b9000`, 16 MHz; GPIO16/17/19 at 12 mA, CS GPIO18 at 2 mA |
| Reset / data-command | GPIO11 active low / GPIO30 active high; 8 mA drive |
| Panel / logic supply | L17 at 2.85 V / shared L6 at 1.8 V |
| Backlight | PM8916 MPP4 current sink, stock maximum 40 mA, manually enabled |

The reset GPIO descriptor intentionally uses `GPIO_ACTIVE_HIGH`: this DBI
helper pulses logical 0 and finishes at 1, so that setting produces the required
physical low pulse followed by high. It does not mean the panel's reset wire
is physically active high. A live measurement with the earlier active-low
descriptor found GPIO11 stuck low; releasing it and replaying initialization
restored visible text.

The driver is configured write-only because panel readback wiring is not
verified. It always resets and initializes the panel. Its standard reset
pulse is used; it does not reproduce the stock three-element reset timing
exactly. The 23 mm physical size is display metadata inherited from the TBR
panel description, not a measurement of this unit.

[`stock-init.hex`](../../packages/a8-display/files/stock-init.hex) is the
exact 140-byte GC9107 initialization property from the compared A8 stock DTB:
SHA-256 `b31d68670100454a86e2d4540696da6cfb429aff459b02f3a078ec746ccb5299`.
[`gen-panel.py`](../../packages/a8-display/gen-panel.py) converts its command
records into the driver's firmware format at build time. It retains all
register/gamma/window values and the waits after sleep-out (120 ms) and
display-on (50 ms). The installed file is
`/lib/firmware/mifib-v18,gc9107.bin`.

Backlight uses `gpio-backlight` over MPP4 in **sink** mode, with binary on/off
control. It is not a digital output driving the LED directly. Linux 6.12's
[`pinctrl-spmi-mpp`](https://github.com/torvalds/linux/blob/v6.12/drivers/pinctrl/qcom/pinctrl-spmi-mpp.c)
writes `drive-strength` straight to the sink-control register: the DTS value
`7` encodes the stock 40 mA. `kmod-a8-backlight` enables that MPP controller
and GPIO backlight support. The target already compiles GPIO backlight and
the PMIC GPIO/MPP driver (`CONFIG_PINCTRL_QCOM_SPMI_PMIC`) into the kernel;
their separate `.ko` files are therefore absent from the rootfs. Image checks
accept the required built-in drivers or their modules. PWM routing and
automatic dimming are not implemented in this version.

### One-button navigation

The accessible power button is used in both menus: **short press = next**,
**hold for one second = select**. Releasing after a long press never counts
as another short press. A key already held during startup is ignored until
released. Both sides share the tested 30 ms debounce/1,000 ms hold logic in
[`one-button.h`](../../packages/a8-display/src/one-button.h).

The V18-only lk1st module in
[`lk2nd-a8`](../../msm89xx/image/lk2nd-a8) initializes the stock GC9107 and
first draws **Tux** from Linux v6.12, with the requested mode and `Starting...`
below it. The indexed RGB565 asset preserves the original 80×80 pixels and
credits Larry Ewing and the GIMP in [`tux.h`](../../msm89xx/image/lk2nd-a8/tux.h).
It is centered on the 128×128 panel, leaving two text rows. The light is
enabled after drawing, and the splash stays for 750 ms. Direct Recovery and
Fastboot entry also initialize and draw the splash; these paths previously
skipped the LCD menu and initialization entirely. A panel/PMIC failure still
lets the requested boot mode proceed. This is a bootloader change; it does
not add LCD/ADB drivers to the existing recovery image.

Normal boot then shows OpenWrt, Recovery, Fastboot USB and EDL. Without input it starts
OpenWrt after five seconds; the first button interaction cancels automatic
boot. Explicit recovery/fastboot reboot requests, reset-key recovery and
automatic boot-failure recovery retain priority. The selecting key must be
released before handing control to the selected mode.

The boot display uses conservative, write-only GPIO SPI mode 0 on
MOSI16/CS18/CLK19, the same 140-byte panel sequence as Linux, the stock
reset timing and MPP4 current sink. It checks that shared L6 is already
at 1.8 V rather than changing its voltage; L17 is set to the stock 2.85 V.
PMIC writes are read back. If the required shared-rail setting or PMIC
access is unavailable, it logs the failure and continues normal boot.
This is an experimental driver: cold-boot output, orientation and actual
button wiring still need checking on this unit.
The hardware-free Tux test compiles the actual pixel helper and checks its
RGB565 output, exact image hash and black margins. A preview was inspected;
visible Tux output on the unit is pending installation and hardware checks.

After OpenWrt starts, a press opens the LCD menu. It offers status, automatic
APN selection or an existing valid saved APN profile, reboot, Recovery,
Fastboot, EDL and power-off. Profile changes and power actions first show
**Cancel**, followed by **Confirm**. Create/edit profiles through LuCI's
Network → APN profiles page. Choosing a profile saves only `a8_apn`
settings; the existing carrier worker applies it with its normal no-SIM
handling. No extra APN worker is started.
Menu labels use the panel's 16 ASCII columns. Long profile names are clipped
and non-ASCII characters are replaced for display.
If UCI exposes unapplied APN edits, the LCD keeps them unchanged and asks
to apply or discard them in the web interface before choosing a profile.

Linux reads the PMIC's `KEY_POWER` through evdev, resynchronizes after
`SYN_DROPPED`, ignores autorepeat and retries if the input device disappears.
It uses an exclusive input grab. On V18, `pwrkey-monitor` starts `a8-menu`
instead of the legacy byte-stream bridge. This single controller routes a
press either to the visible LCD menu or to the original power-button handler.
Menu clicks cannot also trigger double-click reboot/long-press shutdown.
Other boards keep their existing bridge. The hardware's much longer power-key
reset is unchanged. Modem status queries run separately so they
cannot block button timing. Lua separates menu state, display layout and
status/APN/power operations in `a8_display` modules.

### LuCI display and button choices

**System → 화면·버튼** stores the two independent choices in
`/etc/config/a8_device`:

| Setting | Values and behaviour |
| --- | --- |
| Display | **LCD menu** (this board's default), or **LED only**. LED only clears the LCD menu and uses the existing power-button behaviour; it does not assign any LED pins. |
| Menu controls | **Power only**: short = next, one-second hold = select. **Side + power**: side keys move up/down, power selects. The side choice is offered only when the kernel exposes both side keys, or when preserving an existing side preference. |

Side navigation accepts only `KEY_VOLUMEUP` and `KEY_VOLUMEDOWN` together on
an evdev device named `gpio-keys`. It never interprets `KEY_RESTART` as a
navigation key. This V18 DTS does not yet expose the two side keys; no guessed
WPS GPIO is enabled. With a saved side preference but missing side input, the
controller uses the power-only menu. Losing a device, changing input mode or
resynchronizing discards pending confirmations. Navigation keys ignore a held
startup key and move once per debounced press.

When the framebuffer/terminal is missing or cannot be opened, the controller
uses the original power handler instead of an invisible menu. It preserves
the two-short-press reboot and four-second release shutdown behaviour. A press
started in a different display/input state is discarded; switching modes also
clears any old double-click state. The RST handler's five-second factory reset
is unchanged, and RST is not used for menu navigation.

Save alone stages edits. The controller loads the absolute config path so
unapplied UCI deltas cannot change the button mode. Save & Apply reloads the
two services through the `a8_device` trigger. The status collector stops in
LED mode; the single power-key controller remains active. See
[libuci's absolute-path load](https://github.com/openwrt/uci/blob/master/file.c)
for the committed-config behaviour.

These choices affect OpenWrt userspace. They do not switch DTS pin mappings,
change the bootloader menu or identify a physical LCD: the GC9107 is configured
write-only. The reported LED-only sales variants are not identified as V12 or
given a new board profile without their own PCB/stock-firmware evidence.

The new boot menu is part of **`aboot.mbn`** in the firmware ZIP. Updating
only boot/rootfs adds the OpenWrt menu but cannot add the boot menu to an
older installed bootloader. A later bootloader update must target aboot
specifically and keep the existing GPT and radio partitions. The `0c75e22`
installation updated aboot as well as boot/rootfs and verified all three.
The installed aboot partition is **1 MiB**, and the payload is 337,696 bytes.
The follow-up source also corrects the LK font lookup to use its full ASCII
table; that correction has not yet been installed.

The `a8-display` service shows battery capacity when available, otherwise
battery voltage, charging state, Wi-Fi up/down, ModemManager state, LAN IPv4
address and uptime. Missing readings display `--`; an unavailable capacity
is never presented as 100%. It refreshes every five seconds plus the time
needed for bounded status queries. Linux's framebuffer console supplies the
font and handles framebuffer pixel format and row stride; `a8-menu` writes
the collected status as eight fixed-width rows to `/dev/tty1`. It leaves the serial login available
and keeps the screen lit. Both service and executable check the A8 board
identity before running.

The V18 diffconfig explicitly enables `CONFIG_DISPLAY_SUPPORT=y`: OpenWrt
propagates this prerequisite from DRM to `a8-display`; omitting it silently
removes the display package during configuration. The panel package brings
in framebuffer console support and the 8x16 font through `kmod-fb`.
The target kernel config explicitly disables `CONFIG_DRM_MSM` and
`CONFIG_EC_ACER_ASPIRE1`, which the OpenWrt generic config omits. Enabling
DRM/backlight without these settings prompts for the unrelated Qualcomm
display/GPU pipeline and Acer laptop controller, stopping unattended builds
at `syncconfig`. This SPI panel uses `panel-mipi-dbi` instead. A separate
Linux 6.12.112 Kconfig check combines the installed baseline, OpenWrt's
generic/target config and the display packages' recursive KCONFIG settings;
it detects the missing Acer option before the fix and no new prompts after
the fix. This checks configuration, not hardware operation.

After installing a display-enabled build, check:

```sh
cat /sys/class/graphics/fb0/name
cat /sys/class/graphics/fb0/virtual_size
cat /sys/class/backlight/backlight/brightness
dmesg | grep -E 'mipi.dbi|drm|backlight|mpp|spi'
/etc/init.d/a8-display restart
/etc/init.d/pwrkey-monitor restart
```

Check visible text, orientation, cold boots and power consumption on the
actual unit. A successful compile does not establish that the LCD works.
For an A8 already running this OpenWrt layout, the Linux display/menu uses a
boot/rootfs update. The boot menu additionally needs the new aboot described
above; neither feature needs GPT migration or radio writes.
Run `python3 tests/a8-display/test_display.py` for the hardware-free checks.

## USB ADB

The first installed image configured USB NCM networking and an ACM serial
console but did not contain `adbd`. The V18 profile now includes the
`adbd` package alongside `a8-display`. The kernel already selects
`CONFIG_USB_CONFIGFS_F_FS`; the package also selects the FunctionFS module.

The daemon is built from pinned AOSP 34.0.5 source, using the Linux/musl
adaptation credited in [the package notes](../../packages/adbd/README.md).
The existing gadget manager adds `ffs.adb` to the same gadget as NCM/ACM.
It waits for both FunctionFS data endpoints before binding USB, falls back
to network/serial if ADB fails, and watches for a gadget unbound by a cable
reset or daemon restart. Start/stop and reconnect share a lock.

On the installed `0c75e22` image, the real boot log showed `sleep: invalid
number '0.1'` during endpoint polling, followed by the ADB fallback. All board
diffconfigs already select BusyBox `usleep`; the manager's sub-second waits
now use `usleep 100000`. A delayed-endpoint test rejects fractional `sleep`
and checks that ADB joins after readiness. This source correction and the
attempted live application are separate from the downloaded image.
Using that image's actual ARM64 BusyBox, the old polling function fails
immediately; the corrected function waits and links endpoints that appear
after 200 ms. This fixture simulates endpoints rather than a USB controller.

On V18, a one-time defaults script enables ADB even when upgrading with the
old USB configuration preserved. An existing explicit ADB setting is kept.
The default is **USB root access without a host-key prompt** (`auth=open`).
No TCP/vsock listener is compiled into the Linux startup path. For host-key
authentication or disabling ADB, see the package notes; no host private key
or device-specific key is included in the image or repository.

After installing this update and reconnecting a data-capable USB cable:

```sh
adb devices
adb shell id
adb shell 'cat /tmp/sysinfo/board_name'
adb shell 'logread -e adbd'
```

Expected results are a device entry, `uid=0(root)` and the V18 board name.
ADB shell/file transfer, unplug/replug, simultaneous NCM/ACM and visible LCD
output remain **unverified on this device**. This update uses the normal
boot/rootfs update path; adding ADB does not require changing GPT, the
bootloader, recovery or any radio/NV partition.

## Travel APN profiles and SMS

The installed baseline already contains `luci-proto-modemmanager`,
`qcom-carrier-autocfg` and `luci-app-sms-manager`. The first provides manual
APN/authentication/IP settings under Network → Interfaces; the second reads
the SIM's home operator and applies an APN from its TSV tables. Its shipped
table has no Korean entries, so its generic fallback is insufficient here.

The V18 profile adds `a8-apn`, a profile UI and offline database extension
for **the existing `qcom-carrier-autocfg` daemon**. There is no second APN
daemon. Other boards retain their original provisioning path. On V18 the
extension uses netifd's existing ModemManager protocol and updates only the
cellular interface's APN, credentials, authentication, IP type and roaming
permission when values change. It does not load carrier MCFG, reset the
baseband, compare a roaming network with the home operator, or write raw NV.

Open **Network → APN profiles**:

- Automatic mode uses the SIM's home MCC+MNC and provider name. A matching
  saved profile takes precedence; an explicitly selected database candidate
  is next, followed by the existing custom TSV override, an exact provider
  name match, the original carrier table and the remaining database entries.
  A selection from the previous SIM is ignored when absent from the new
  SIM's candidates. The visited network never selects the APN.
- Save named profiles for travel SIMs/MVNOs, including credentials and IP
  type, then select one manually or associate it with a home operator and
  optional provider-name condition. Automatic discovery cannot distinguish
  every reseller/plan; a saved override handles those cases.
- Turning **APN setting management** off preserves the normal Interfaces
  page's manual configuration. The old daemon path does not overwrite it.
- Data roaming defaults off and can be explicitly allowed on this page.
  A missing/locked SIM or missing profile causes no network configuration
  writes. Existing non-default APNs are retained as the selected `previous`
  profile during first installation; the old `jionet` default is also saved
  without deleting it while a SIM is absent.

The worker also defers while its UCI cursor sees unapplied `network` or
`a8_apn` changes. It reports `pending-changes` / `APN EDITING` and performs
no interface restart. A private UCI savedir alone does not exclude the
[default staging path](https://github.com/openwrt/uci/blob/master/delta.c);
pending changes are checked again after modem queries.
The LCD likewise refuses to commit staged APN edits with a profile choice.

The bundled Internet APNs come from the public-domain
[GNOME provider database](https://github.com/GNOME/mobile-broadband-provider-info/tree/c6796724e78483ee34fd999fe1532f3c12d59fa6),
pinned and hash-checked at build time: 802 home networks and 1,494 Internet
APN entries. MMS/IMS-only entries are excluded. LG U+ (`45006`) resolves to
`internet.lguplus.co.kr`; KT/SKT entries are also present. Lookup works
without internet access. `/etc/config/a8_apn` is mode 0600 and preserved as
package configuration; status responses omit usernames and passwords.

The status screen includes `APN AUTO`, `APN PROFILE` or `APN MANUAL` and
shows `SIM NONE` for an absent card. SMS remains managed by the existing
SMS Manager page; this change does not send or delete messages. The owner
removed the SIM for other use, so LTE data and incoming SMS tests are
**not run**, rather than treated as device failures. UI/ADB/LCD checks do
not require a SIM.

Hardware-free checks: five APN tests passed, including actual fixture-file
writes; the selection/apply checks also passed under a standalone Lua 5.1.5
interpreter matching OpenWrt's Lua generation. Fifteen display tests are
covered across macOS/Linux, and thirteen USB/package-preparation tests passed.
The latest Linux board harness with
self-check passed 57 checks, with zero failures and three expected skips.
The CI host skips Lua-only cases when no interpreter is installed; menu and
APN operations are also checked with the image's actual Lua 5.1/libuci in an
isolated ARM64 rootfs, using simulated modem responses and no network access.
Run `python3 tests/a8-apn/test_apn.py` and
`python3 tests/a8-display/test_display.py`. The Lua selection checks run
when an interpreter is available; database and integration checks always run.
Actual SIM/APN switching, data connectivity and SMS reception remain unverified.

## Bluetooth and USB host limits

The installed baseline contains BlueZ 5.83, `bluetoothctl`, the Qualcomm
SMD driver and `kmod-bluetooth`. The baseline rootfs was inspected and
contains `bnep.ko`, so the PAN kernel component is present. Actual controller
detection, radio operation and pairing on this A8 remain unverified.
`bt-spp` configures a serial shell; it is not an Internet sharing service.
BlueZ supports NAP (share A8's Internet) and PANU (use a phone's tethering),
but neither PAN networking role is configured here. A pairing UI alone does
not configure the network bridge, DHCP or firewall. The external
`sbwml/luci-app-bluetooth` package is not included; its default dependencies
also include audio packages. OpenWrt's `bluez-tools` package installs only
`bt-agent`, not the upstream `bt-network` utility.

V18 inherits `dr_mode = "peripheral"` from V12. USB Ethernet host drivers
are absent from the installed baseline. A wired-uplink Wi-Fi AP would
require working USB host mode, adapter power, the matching driver and a
WAN interface. The connector's host-role wiring, 5 V output and concurrent
charging are unverified; changing the device tree alone is not proof of OTG
support. This build retains the USB device role used by NCM/ACM/ADB.

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
| Board-profile checks, including the harness self-check on Linux | Latest run passed; the Lua-only APN model check is skipped in the builder image and passes separately on macOS. GNU sed is required by the harness's mutation checks. |
| Shell syntax of the changed firmware generator and installer | Passed |
| GNU Make evaluation of V18 profile inheritance | Passed; correct identity, capacity, recovery layout and battery package; recovery flag resets for the next board |
| Patches 801 and 869 | Passed both `git apply --check` and zero-fuzz `patch --dry-run` |
| Standalone DTB compile on Linux 6.12.94 DTS inputs | Passed; 12 relevant shared/board DTS patch subsets applied with zero fuzz |
| DTB extracted from the installed baseline boot image | 54,998 bytes, 461 nodes; byte-identical to the standalone compile; V18 identity, L6 at 1.8 V with both power flags, reset GPIO102 active-low, 4.2 V battery profile, only SIM pulse LED output, SDC2 disabled |
| Generated GPT against original A8 radio sizes/offsets | Passed using backup metadata and sparse test files; no radio contents or device were written |
| Radio migration failure/success tests | Passed: 22 tests on both Linux and macOS |
| GitHub Actions test suites for `ce14a4f` | [Passed](https://github.com/wwwhana/msm8916-openwrt/actions/runs/42040239138): modem guard, 22 radio migration tests, and 54 board-profile checks |
| GitHub Actions bootloader build for `23d3beb` | [Passed](https://github.com/wwwhana/msm8916-openwrt/actions/runs/42044504211); downloaded artifact digest and ZIP CRCs match, all five components fit the V18 GPT, installer matches tested source, and aboot contains recovery-key/failover/EDL code markers |
| First complete OpenWrt build | [Passed on GitHub Actions](https://github.com/wwwhana/msm8916-openwrt/actions/runs/42040234977) for `ce14a4f`; Linux 6.12.112, GCC 14.3.0 |
| Downloaded baseline production artifact | GitHub archive SHA-256 and ZIP CRCs match; Android boot header, V18 DTB, firmware metadata/CRC and both GPT header/entry-array CRCs passed |
| Baseline image sizes against the generated GPT | Boot 6,603,036 bytes / 64 MiB; system 28,364,763 bytes / 128 MiB; private recovery 11,913,216 bytes / 16 MiB; all fit |
| Baseline sysupgrade content against standalone images | Kernel byte-identical; rootfs bytes identical with the expected 321 zero bytes added to a 1 KiB boundary by OpenWrt's sysupgrade packer |
| Actual generated GPT with the installer's radio preflight | Passed against original A8 layout metadata and sparse files; all seven radio lengths preserved, `modemst2` moves to partition 5 at LBA 141314 |
| Private V18 recovery build and offline inspection | Passed; production kernel and eight matching modules, this unit's firmware/NV, initramfs contents and userspace library checks; 11.36 MiB image |
| Combined local bundle with the real installer | Correct V18 GPT, boot, system and private recovery files selected; radio preflight passed against original GPT metadata and actual local `saved/` file lengths, with device access blocked |
| EDL installation on this A8 | Passed: original device GPT and all seven live radio files matched the backup; 20 write commands completed; a final 20-region read-back matched GPT, boot chain, boot/rootfs/recovery, cleared regions and all seven radio backups before reset |
| First software reset after installation | Reset command succeeded; USB network/console did not reappear during that initial observation. The user subsequently reported running OpenWrt at `192.168.8.1`; agent SSH access timed out. A root console was later observed after the `0c75e22` update. |
| Wi-Fi, Bluetooth, LTE traffic | Not run |
| Charging, temperature, reset/recovery, cold boots, traffic soak | Not run |
| LCD source and initialization checks | Twelve tests passed on macOS, including menu controls; standalone Linux 6.12.112 DTB compile passed. Actual pixels remain unverified. |
| Display kernel configuration | Linux 6.12.112 `listnewconfig` detects the missing Acer EC setting before the fix; no new prompts after the fix, and `syncconfig` succeeds with stdin closed. Required framebuffer/panel/backlight options and both unrelated-driver exclusions verified. |
| USB ADB source checks | Thirteen tests passed: ten startup/reconnect/fallback/default-setting checks, including delayed descriptors with integer-only `sleep`, plus three package-preparation checks. Linux board harness with mutation checks also passed. |
| Travel APN source checks | Five tests passed on macOS, including the actual Lua model and staged-settings guard. Database generation, home-operator selection, manual profiles and existing-daemon integration checked; the final image also passes actual Lua 5.1/libuci configuration writes in the isolated rootfs. ModemManager replies and interface restarts are simulated. |
| Previous LCD/ADB/APN/menu build | Source `12ea725` [built successfully](https://github.com/wwwhana/msm8916-openwrt/actions/runs/42163596055); tests [42163630238](https://github.com/wwwhana/msm8916-openwrt/actions/runs/42163630238) passed. Its `a8-display` is `1.0-r4`; this image was inspected but not installed. |
| Installed LuCI display/button build | Source `0c75e22`: [build 42187058705](https://github.com/wwwhana/msm8916-openwrt/actions/runs/42187058705) and [tests 42187062701](https://github.com/wwwhana/msm8916-openwrt/actions/runs/42187062701) passed. Archive/ZIP checks, metadata CRC and standalone/sysupgrade comparisons passed. `a8-apn` is `1.0-r2`, `a8-display` is `1.0-r5`. Thirteen checks pass in the actual ARM64 image without source overlays. |
| Updated image sizes and GPT | Boot 6,648,092 bytes / 64 MiB; rootfs 29,369,037 bytes / 128 MiB. Primary/backup GPT CRCs pass and all 17 partition geometries match the installed baseline, including `modemst2` at partition 5, LBA 141314–144385. Generated GUIDs differ; installation retained the existing GPT. |
| Updated LCD DTB and kernel configuration | DTB 56,444 bytes / 469 nodes, byte-identical to the standalone LCD compile. V18 identity, panel pins/timing/supplies, MPP4 sink drive 7, disabled SDC2 and Bluetooth description verified. Framebuffer console, 8x16 font, PMIC power-key/evdev, FunctionFS, built-in GPIO backlight and PMIC GPIO/MPP controller verified in the actual kernel configuration. |
| Previous r4 ARM64 userspace | All 22 checks passed using that image's Lua 5.1/libuci and musl programs, without source overlays, network access or physical devices. Covers real APN configuration writes/migration, home-vs-visited operator selection, no-SIM/locked-SIM handling, staged edits (including edits during modem queries), retry, RPC, menu actions/confirmations, legacy button suppression and input-reader disconnect/ticks. Modem responses and `ifup` are simulated; power commands are not executed. |
| One-button menus | Twelve display tests pass, including shared short/long/debounce/startup-held timing, boot-mode selection/timeout, Lua navigation/confirmation and APN settings actions. Host Linux and ARM64 input-reader compilation passes with warnings treated as errors. The deployed ARM64/musl reader stays alive and emits ticks without an input device in the sandbox. Physical LCD/button operation remains unverified. |
| LuCI display/button follow-up source (`a8-display` 1.0-r5) | Fifteen source checks covered across macOS and Linux (the Linux evdev case runs separately from the macOS Lua/Node cases); Linux board harness: 57 passed, 0 failed, 3 skipped. Thirteen isolated OpenWrt Lua 5.1/libuci checks pass with the new source overlaid on the previous rootfs: real staged/committed settings, original power handler, RPC and V18/V12 init routing. Power commands and devices are simulated. This is not an inspection of a newly built image or a physical LuCI/browser test. |
| Menu-enabled bootloader | [42155142274](https://github.com/wwwhana/msm8916-openwrt/actions/runs/42155142274) passed for `c0a772d`; all five components are byte-identical in the installed `0c75e22` ZIP. The 337,696-byte aboot fits the actual **1 MiB** partition and was read back after installation. The font-index correction at `215dc88` also [built successfully](https://github.com/wwwhana/msm8916-openwrt/actions/runs/42204398538) and its artifact checks passed; it is not installed. Actual boot-menu output remains unverified. |
| Standalone USB adbd build | AOSP daemon and bundled libraries compiled and linked on ARM64 Alpine/musl; missing FunctionFS exits with an error. Ubuntu 22.04 host Go 1.18 generates the same BoringSSL error table. This is not an OpenWrt firmware build or device connection test. |
| `0c75e22` EDL update | Passed: only aboot/boot/rootfs written. Each full partition read-back verified payload, sector padding and untouched tail. All seven radio hashes, including `modemst2`, and both GPT copies stayed unchanged; reset succeeded. |
| Updated OpenWrt on this A8 | Root USB ACM/ADB observed: Linux 6.12.112, V18 identity, 128×128 RGB565 fb0 and connected power input. GPIO11 was held low by the inverted reset descriptor. Releasing it high and replaying stock initialization restored text, confirmed by the owner. Corrected DTB installation and physical navigation checks are pending. |
| USB ADB on this A8 | The `usleep` fix was written to the production overlay through Recovery and its hash verified. After a normal reboot, ADB enumerated and root shell, file push and exact framebuffer read-back passed. Unplug/replug remains untested. |
| Removable SD, WPS and SIM hot-swap | Unverified; SD/WPS support not enabled |

The installed baseline DTB is 54,998 bytes, SHA-256
`0bd0e5936294b2331b4e7d17671c86a6b54dc59b3e2bb2071b3a50d69d4a856d`.
It matches the earlier standalone compile. The V12 baseline and V18 standalone
compile both report the same four warnings in shared upstream DTS nodes.
Production GPT GUIDs differ from the standalone generator run, so file hashes
are not interchangeable; both layouts passed the header/array CRC, partition
bounds and radio-size checks. The inspected production files and their current
hashes are in the local bundle's `build-info.json` and `SHA256SUMS`.

The updated LCD/menu image DTB is 56,444 bytes, SHA-256
`1bee2441587ac7511f7ada67ada6d507248e167b0bc7272ee49e7f4dbffb7191`.
Its five boot-chain components are byte-identical to the separately inspected
menu-enabled bootloader build; aboot is 337,696 bytes, SHA-256
`110b368fe181fbc502859e49163d489946542065357fab0dc581eb187cfa87b9`.
The current update's complete checks and file/component hashes are recorded in
`../artifacts/update/build-info.json` and `SHA256SUMS`.

Use [the board sign-off checklist](../../tests/board-profile/signoff.sh) to
record runtime results before calling this a hardware-supported board.
Build success alone is not device validation.
