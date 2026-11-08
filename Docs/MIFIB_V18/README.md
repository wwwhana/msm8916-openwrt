# A8 / HM-MiFiB-V18 — experimental port

The AliExpress **A8** examined here is a battery-powered USB-C pocket router
with an LCD and PCB marking **HM-MiFiB-V18**. The build target is
`mifib-v18`, the image profile is `generic-mifib-v18`, and the first DT
compatible is `mifib-v18,generic`. A8 is a sales name; this target does not
cover every product sold under that name.

## Live overlay update, 2026-11-07

The repaired standalone LCD bootloader is now installed after RAM normal-boot
testing and the owner's approval. **The owner confirmed Tux and Boot menu
before Linux during a plain reboot into the installed bootloader.** OpenWrt
returned normally. Full aboot read-back verified the payload, sector padding
and untouched tail; the original recovery image is unchanged. Installed
Fastboot responds over USB and completes LCD initialization; the installed
recovery boots to its A8 USB console. The known-working previous aboot is
preserved as a full 1 MiB backup. Cold power-on of this revision remains
unverified. These updates have **not** been rebuilt into a complete firmware
image; the installed aboot is the separate `aboot.mbn` below.

| Feature | Current result |
| --- | --- |
| LCD navigation | Status is now the SCR01-inspired card dashboard, physically confirmed by the owner. The first page has Status, Network, SMS and Power; the second has Display for both screen timers. Submenus, pairing and action confirmations use the same card style. Back keeps the parent selection; the fixed clock screensaver is retained. The current display suite passes 36 of 37 checks, with Linux-only evdev skipped on macOS. |
| SIM / LTE | A wrong qmi-proxy path in the live SMS libqmi overlay prevented modem discovery. The persistent path fix restored ready USIM, home registration and a connected bearer; two pings through `wwan0` passed. APN cache now reports ready. |
| Battery / USB ADB | The dashboard shows battery, LTE signal, actual LTE interface traffic, Wi-Fi clients, Bluetooth and LAN IP. Battery and signal have five levels; charging animates the next battery segment. Battery charge limiting is enabled (stop 85%, resume 80%), explaining charge disabled above the limit. ADB shell works and the owner confirmed the web checkbox is visible. |
| Internet choice | LCD and LuCI share the persisted Relay switch and LTE-first / Wi-Fi-first / LTE-only policy. Actual relay off/on and priority changes were checked; the AP remained up and LTE-bound ping passed during that check. Later the station was the active uplink while the modem was registered without a data interface. |
| Nearby networks | The helper resolves the current interface each time and uses the supplicant's scan/cache when present. Cached networks were returned on this unit; a failed scan without results is shown as an error. A cache result is not proof of a fresh full-band scan. |
| Tailscale / VPN | The LCD shows the actual Tailscale state, including LOGIN, and toggles the service. Actual off/on through the LCD backend passed and retained a test file in its state directory; the previous enabled setting was restored. Tailscale currently reports NeedsLogin; an authenticated tunnel is unverified. Configured WireGuard interfaces get menu entries; this unit has no WireGuard configuration to test. |
| SMS / IMS | The LCD reads the existing incoming archive, Korean text and actual picture attachments. The owner confirmed the photo on the LCD; native text/image reads passed. This display update did not retest message transmission. Earlier modem/IMS transport work is recorded below. |
| Bluetooth controller / UI | BlueZ 5.83 detects and powers hci0. The adapted sbwml LuCI app, bluetoothctl and the LCD confirmation agent are installed. A three-minute pairing window is available from LuCI and the LCD. |
| Bluetooth Internet sharing | bt-network registers NAP (UUID 1116) on br-lan. LCD Sharing, the LuCI switch and bt-nap control the saved setting. Sharing on powers Bluetooth on; readiness requires both Bluetooth power and NAP registration. An iPhone paired, created bnep0 on br-lan and passed the owner's Internet test with Wi-Fi/cellular data off. The owner also confirmed iPad pairing and Internet access with compatibility enabled. Other clients remain unverified. |
| iPhone pairing | Initial pairing fails with HCI authentication status 0x05 when Secure Connections is enabled, including a BR/EDR-only trial. SC off, with numeric comparison and encryption retained, produced pairing success and a stored bond. Restoring SC on still allowed encrypted reconnection and the owner's Internet test. The requested Pairing Compatibility option is installed in LCD/web controls, defaults off, and passed actual controller on/off checks. |
| Package manager / USB core | The actual LuCI HTTPS install path executes with the package-manager ACL. `kmod-usb-core` installation is rejected because the feed's kernel build ID differs from this experimental image. USB core is already built into the running kernel, so this package is unnecessary for this unit. The owner's login-session permission error still needs confirmation after signing in again. |

The LCD confirmation starts on **Reject** and requires an explicit button
selection. It expires after 45 seconds; stale replies cannot approve a new
request. The wrapper also closes a pending prompt when the peer disconnects. Once a
reply has been written successfully, the LCD remembers that request ID so a
slow file cleanup cannot reopen the same prompt. Failed reply writes remain
retryable; a new request ID still requires confirmation.
A typed legacy passkey still requires interactive `bluetoothctl`.

The live `bt-network` executable was taken from Alpine's aarch64 musl
`bluez-tools-0_git20201025-r0` package and its dependencies were checked on the
router. The new OpenWrt `bluez-network` package instead builds the pinned
upstream bluez-tools source at `f65321736475429316f07ee94ec0deac8e46ec4a`.
Live overlay installation is therefore distinct from an OpenWrt package build.
The SMS check also changed the modem's domain preference through QMI, as
recorded below; that is separate from installing the userspace files.

### Card dashboard, menus and Korean SMS reader, 2026-11-07

The owner selected the [Galaxy SCR01 home-screen reference](https://www.au.com/mobile/product/data/scr01/)
for the **A8 128×128 LCD**, confirmed the first dashboard on the device, and
requested that it replace Status. `a8-display` 1.0-r20 now uses this dashboard
as Status and removes the duplicate Dashboard menu entry. White cards, cyan
selection highlights and small icons also cover Network, VPN, USB, Bluetooth,
APN profiles, priority, Power, pairing, action confirmations and result notices.
The first four root cards remain **Status, Network, SMS, Power**. A second
card page contains **Display**, with Screen saver and Screen off settings.
Both timers start at the last button activity; defaults are 30 seconds for the
fixed card clock and five minutes for actual backlight off. Each accepts Never,
15/30 seconds or 1/2/5/10/30 minutes. The timers are independent: screen off
takes priority if its time arrives before the clock. It stays off and framebuffer updates stop until input or a
pairing request wakes it. The carrier, large clock and battery stay in fixed
positions. The first gesture only wakes the screen and retains the previous
menu selection. The GPIO backlight is controlled through its brightness
attribute, with actual brightness checked after every change. Pairing starts
on Reject; restart/shutdown actions still require the separate confirmation.

The same timer values are available under LuCI **System → 화면·버튼**. LCD
changes commit the selected timer and take effect immediately; the current
choice has a check mark. Web Save alone stays pending until Apply, and the LCD
refuses to overwrite pending web display edits. Missing or invalid timer values
fall back to the defaults. Display mode and button mode are preserved.

The two dashboard gauges show LTE interface RX+TX and the five-level signal.
Traffic is the **current interface session total**, reset when the LTE interface
is recreated; it is not a monthly plan quota. Wi-Fi counts authorized clients
from active AP interfaces, and unavailable values are shown as unknown. These
values come from the existing background cache, keeping menu input responsive.

SMS now lists incoming SMS/MMS from the same SQLite archive as the web inbox,
newest first, with sender and local date/time. Messages containing an image have an **IMG** marker. Hold to open the selected message.
The body supports Korean and Latin text with five 16-pixel lines per page;
short presses advance pages, and a one-second hold returns to the same inbox
selection. Image pages appear first, followed by the text pages; each picture
is fitted to 112×80 pixels with white letterboxing. Side-key mode uses up/down
for pages and Power to return. Older
messages load in groups of 20 with their own Back destination. This reader uses
read-only SQL: it does not send/delete messages or change web read flags. The separate `a8-image` helper supports JPEG, PNG, GIF first frames and WebP,
including JPEG EXIF orientation, transparency and aspect ratio. Broken images
show an error and remain navigable. Other file types show their count. The
helper accepts validated attachment IDs from the private parts store, limits
encoded data to 8 MiB and decoded dimensions to 16 megapixels, and reads no
filename supplied by the message. Unsupported text glyphs are visible boxes. The 389,284-byte Korean/Latin font subset
is packed by `packages/a8-display/gen-font.py` from GNU Unifont 18.0.01; its
source URL, source hash and OFL license are included in `unifont-OFL.txt`.

Display/helper/license and web settings files are checked against their source hashes during installation. Native Lua 5.1
compilation, dashboard generation and real archive list/body reads passed.
The live Status framebuffer exactly matched all 16,384 expected RGB565 pixels;
its enlarged capture is `A8/artifacts/dashboard.png`. Actual menu and clock
captures are `A8/artifacts/lcd-menu.png` and `A8/artifacts/screensaver.png`. The archive returned eight
inbox entries, and the first real body wrapped into nine pages without exporting
its content to the host. The display suite passes **36 of 37 checks**, with the
Linux-only evdev check skipped on macOS, including Korean glyphs, Unicode
wrapping, actual SQL against the production schema, inbox/body navigation and
pairing interrupts. Six image-helper tests cover the supported codecs,
letterboxing/alpha, malformed dimensions, eight orientations, private part
access and partial GIF first frames. The ARM64 static helper was cross-compiled
locally and passed actual attachment decoding on the A8. The owner physically confirmed the attached photo and the new large clock
with battery after installation. Native framebuffer checks also passed for
all menu families and photo output. The running controller was also exercised
with its idle counter advanced to the real sleep threshold: actual backlight
brightness changed from 1 to 0, stayed at 0 for four checks with no framebuffer
updates, then returned to 1 on the first button gesture without selecting the
saved Power item. The previous fixed-time version also passed a natural
five-minute interval: the backlight stayed off and the clock image stopped
changing. Configurable settings use the same sleep/wake path. The owner selected
15 seconds for the clock and 30 seconds for backlight off; those values are
stored and confirmed in the restarted screen service. Power is ordered **Power off, Reboot,
Recovery, Fastboot, EDL / USB 9008, Back**, with Cancel selected when confirming.
This is a live display-file update; no OpenWrt package/ROM build, device reboot
or bootloader write was performed for it.

### Recovery and bootloader LCD repair, 2026-11-07

The recovery LCD repair is **installed and physically confirmed** on this unit.
It shows Tux above `Recovery` and `192.168.10.1`. Framebuffer readback is
128×128 RGB565, `/dev/tty1` is 8 rows × 16 columns, the backlight is on, and
`/tmp/display.err` is empty. No kernel console is attached to `tty0`.

`build-recovery.sh` now includes the production DRM/MIPI-DBI modules for V18,
generates its GC9107 firmware from the same stock sequence as normal OpenWrt,
and reuses the bootloader's Tux pixels. A detached `recovery-display` helper
loads the modules, waits for the framebuffer, and draws the two text rows
without scrolling. It exits on other board variants. Recovery USB, network
and shell startup run independently of the display helper. Packing uses a
temporary tree, so per-unit Wi-Fi calibration never replaces tracked files.

The private recovery image is `../artifacts/recovery-mifib-v18.img`,
**12,255,232 bytes**, SHA-256
`a07e7a0cd6e421ccec8d63657f89bdccbcee404cb6c051018d074fdc221e468f`.
Its kernel and corrected reset-polarity DTB are copied verbatim from the
installed production boot image. It fits the 16 MiB recovery partition.
The written bytes matched on device readback, the remaining partition bytes
were unchanged, and both GPT copies and all seven radio partitions matched
their pre-write hashes. The image includes this unit's Wi-Fi calibration and
must remain private.

The first local bootloader attempt (291,492 bytes, SHA-256
`faaf6a409d4899a37fc53e4aa85f5f7fc5b6559c65edb70f1e069f2bcbff7fa6`)
added a 10 ms PMIC-transfer limit and logging. Its installed bytes were checked,
but the owner reported a blank screen and USB did not return. That limit did
**not** resolve the observed failure. Its exact runtime cause remains unknown.

The subsequent code repair was locally built, tested in RAM and then installed:

* Recovery entry, including automatic boot failover and the recovery button,
  bypasses the bootloader LCD driver. The confirmed recovery helper owns the
  rescue display. A failure in that driver must not also block rescue boot.
* Fastboot starts its USB command thread and controller before drawing its
  LCD splash. Previously both rescue modes called the LCD before continuing.
* V18's SPMI lookup table starts with invalid entries. Both read and write paths
  reject missing channels and verify the channel's actual slave/peripheral
  mapping before issuing a transfer. Upstream left missing entries uninitialized;
  a timeout alone cannot prevent a write through the wrong peripheral channel.
  This source defect is established; its involvement in this unit's failure
  is not confirmed.
* Text cells are padded and truncated directly. LK's formatter interprets
  `%-16.16s` as a width of 1616 because it ignores the precision separator.
  The normal libc used by the older test harness did not expose that difference.

The first installed LCD bootloader was **346,184 bytes**, payload SHA-256
`db6289a9f5c00728ccfd288dad54f3fe72ec055a2f864ca8ddf5fe1aacc1ee62`.
It includes the same read-only L6/L17 handling as the successful RAM diagnostic
below. The local build uses GCC 10.3.1, and its normal boot display is
physically confirmed. Its exact full-partition backup is
`../analysis/openwrt-port/aboot-lcd-known-good.bin` (SHA-256
`2bf66c4debb77a5013513dbd9815adaf1cc8c13dc9184f9f2f46e444708f2278`).
The artifact path `../artifacts/bootloader/aboot.mbn` now holds the installed
counter-order correction described below. Its LCD source is unchanged.
All four project patches apply to LK source commit
`3e86db90765468c55e1be507cb897efd20876b71` without offsets. ARM compilation,
MBN segment/hash checks, and the 1 MiB partition-size check passed. The complete
LCD driver runs under address/undefined-behavior sanitizers with simulated
GPIO/SPI, PMIC and time: its transmitted Tux pixels and display bounds are
checked, and PMIC failures return to boot. The rescue-flow test checks that
Recovery avoids the LCD and Fastboot starts USB first. The current display suite passes
35 of 36 checks; the Linux-only evdev check is skipped on macOS. Three recovery
packing checks pass separately.

Device restoration used the known-working `0c75e22` aboot from the
existing `../artifacts/update/*-firmware.zip`: 337,696 bytes, SHA-256
`110b368fe181fbc502859e49163d489946542065357fab0dc581eb187cfa87b9`.
Only aboot was written. Payload, sector padding and the untouched partition tail
were checked, and `modemst2` plus both GPT copies stayed unchanged. The owner
confirmed normal OpenWrt after disconnecting both battery and USB. Fastboot
product `lk1st-msm8916` and `oem log`/`get_staged` were subsequently checked.
The matching RAM LCD path was physically confirmed before the primary image
was installed. The installed normal boot now shows Tux and Boot menu before
Linux, also confirmed by the owner. Installation records
and the separate prepared image are recorded in `../analysis/openwrt-port/update.json`
under `lcd_recovery_bootloader_fix`. These are local builds, not a new GitHub
Actions run. No duplicate full-device backup was made.

The first RAM LCD image (SHA-256
`934071a3834ea0fcb41a752eed9a54f2dac1df85e58b8139efb8fff8ed171298`)
was accepted by `fastboot boot`, but USB did not return. The acknowledgment
precedes the handoff and does not establish that the new LK reached Fastboot.
A physical power cycle restored normal OpenWrt. Full aboot and recovery hashes
still matched the restored baseline; no permanent image write command was used.

The next RAM diagnostic reached Fastboot product `lk2nd-a8-lcd` within 0.5 s.
Its boot path skips the LCD, including the forced-reset shortcut that previously
could bypass `LK2ND_FORCE_FASTBOOT`. `fastboot oem a8-lcd` then reported
`Checking shared L6` and lost USB before the next phase. The exact failing
register operation was not yet identified. The owner restored normal OpenWrt
with a physical power cycle. No installed aboot replacement has been attempted.

The subsequent image (`7d211b09…`) reached RAM Fastboot and completed the
three shared-L6 reads. USB stopped responding after its first direct L17
write (`1:5045=80`). Normal OpenWrt was observed again later; whether the
owner power-cycled at that point is unconfirmed. Full aboot and recovery
hashes were unchanged. The exact failure inside that write remains unresolved.

The current `../artifacts/bootloader/lk2nd-lcd-ram.img` is **432,144 bytes**,
SHA-256 `2ea8fbb23ae5b8da0a2e42068d820136c374434213352827cdd0ec4e0ea28e60`.
Its project is maintained in
[`ram.mk`](../../msm89xx/image/lk2nd-a8/ram.mk). It reports each PMIC register
access and its returned value. Both L6 and L17 are read-only: L6 must already
be enabled at 1.8 V and L17 at 2.85 V. Linux controls L17 through RPM; this
diagnostic does not replace that with direct LDO writes. MPP4 remains the
only PMIC write target in the display driver.
It has no boot-failover counter. ARM compilation, the exact four-patch
application, ELF relocation bounds, boot-image/QCDT layout, and DTB separation
passed. On the owner's subsequent request this revision reached RAM Fastboot
product `lk2nd-a8-lcd`. `fastboot oem a8-lcd` completed in 2.312 seconds:
both supply checks passed, the SPI Tux/text transfer completed, and MPP4's
backlight enable values were read back. Fastboot remained responsive, and a
later `oem log` / `get_staged` check confirmed `Display ready`. This establishes
completion of the driver. A repeat RAM boot then showed **Tux and text,
physically confirmed by the owner**, with Fastboot still responsive. The first
repeat transition mistakenly used BusyBox's ignored `reboot bootloader` operand;
the verified transition uses `/sbin/reboot-bootloader -f`.

A plain `fastboot reboot` was acknowledged. A8 returned as Linux composite USB
`1d6b:0104`; ADB and CDC ACM initially remained unavailable. They later returned,
and OpenWrt 6.12.112 plus both full aboot/recovery hashes were checked again
against the restored baseline. No image write was issued during the RAM tests
or return attempts. The known-working aboot remained installed during those
RAM trials; it was later backed up before the approved primary replacement.

The next RAM trial used `A8_LCD_RAM_TEST=0` in the same RAM project to execute
the production LCD/menu branch. Forced Fastboot and the normal lk2nd 512 KiB
boot-partition reservation were disabled for this trial, so it read the
installed OpenWrt boot image from byte zero. The owner confirmed **Tux and
Boot menu before Linux**. ADB returned after 45.53 seconds; the running
kernel's `chosen/lk2nd,project` was `lk2nd-a8-lcd`, and the runtime LCD menu
was active. Full aboot/recovery hashes remained unchanged. The normal RAM
image is `../artifacts/bootloader/lk2nd-normal-ram.img`; it is for temporary
`fastboot boot`, not installation. The 346,184-byte primary candidate passed
its MBN v3 header/segment hash and bounds checks again. A full 1 MiB backup
of the previously working aboot was verified before installation. The owner
approved the replacement; only the first 346,624 bytes of aboot were written.
The whole 1 MiB read-back matched the expected new payload, padding and old
tail (SHA-256 `2bf66c4debb77a5013513dbd9815adaf1cc8c13dc9184f9f2f46e444708f2278`).
After a plain reboot, the owner confirmed Tux and Boot menu before Linux;
normal OpenWrt ADB returned after 59.41 seconds and its `lk2nd,project` was
`lk1st-msm8916`. A fresh-kernel aboot hash check matched the installed bytes.
Installed Fastboot USB and LCD initialization then passed, followed by the
unchanged recovery's A8 USB console and kernel 6.12.112. The old aboot backup
is `../analysis/openwrt-port/aboot-known-good.bin`. Cold power-on remains
unverified.

The consecutive Fastboot / Recovery checks exposed an existing counter-order
bug: `boot_failover_check()` ran before explicit reboot reasons were decoded,
so these intentional rescue boots counted toward the three-failure threshold.
The third transition returned to Recovery. An explicit `reboot-mode-raw normal`
then returned to OpenWrt after 46.35 seconds; the fresh kernel verified both
installed partition hashes and the healthy userspace reset the counter to zero.

The minimum correction moves the existing counter check after reboot-reason
and forced-Fastboot handling, while retaining its position before LCD startup.
The executable call-site test verifies that Recovery, Fastboot and forced
Fastboot do not count, and failed normal boot attempts still trigger rescue.
All four patches apply exactly and the ARM build and MBN v3 checks pass.
The final `../artifacts/bootloader/aboot.mbn` is **346,184 bytes**, SHA-256
`000aefc6cb8e7635ef2c265bc539a2da31a4c89a845fc6f36f3ac462a1bf0d54`.
Automatic approval review initially blocked the changed image without new
explicit authorization; that attempt never started. The owner then explicitly
approved installation. Only the padded 346,624-byte aboot prefix was written,
and the entire 1 MiB read-back matched the new payload, padding and untouched
tail, SHA-256
`aa2c2f9179d119e64d9f061e8b62dc16c02da1910ebc0f1d6633f0d47d6f4a5e`.
Normal OpenWrt returned after 56.45 seconds, with a fresh-kernel hash check.

The same Fastboot -> Recovery -> plain Recovery reboot sequence was then
repeated. Fastboot USB responded in 1.08 seconds and the LCD log reached
`Display ready`; it contained no normal-failure count. The recovery console
verified the new full aboot hash, the unchanged recovery hash and an all-zero
counter after both intentional rescue boots. Its plain BusyBox forced reboot
returned directly to normal OpenWrt after 64.64 seconds. The LCD button menu
was active, the normal userspace reset the counter, and Bluetooth's saved
compatibility option remained enabled. The final counter-only revision's LCD
source is byte-identical to the physically confirmed initial LCD revision;
physical viewing was not repeated after this correction. Cold power-on is
still untested. This is a standalone aboot update: the full `firmware.zip`
was not rebuilt and retains its older bootloader.

### Linux startup display, 2026-11-07

The owner observed `Linux ON -> blinking underscore -> menu`. The display
service now starts at S12 rather than S98. Its boot instance hides the tty1
cursor before waiting for the framebuffer, then draws the same Tux above
`OpenWrt / Starting...`. The existing S94 button menu takes over normally.
Manual service starts do not replay the boot screen, and an already active
menu prevents the early helper from overwriting it.

The shared recovery Tux generator produces a 128×96 P6 image for BusyBox
`fbsplash`, leaving two text rows below it. The helper checks panel name,
16-bit pixels, 256-byte stride and resolution. The first `dd` implementation
matched framebuffer bytes but showed only text on the physical panel; those
bytes alone did not prove that the picture was displayed. The existing
recovery `fbsplash` then showed Tux, physically confirmed by the owner.
The actual BusyBox `sleep` rejects fractions, so framebuffer polling uses
whole seconds. The successful draw time is recorded in
`/var/run/a8-display.boot`. A helper failure leaves the existing menu startup
independent.

The display program, init script, P6 image and recovery's existing static
BusyBox helper (installed as `/sbin/fbsplash`) were installed in the persistent
overlay, and their hashes matched local inputs. The V18 profile selects
`CONFIG_BUSYBOX_CONFIG_FBSPLASH=y` for future firmware builds. The startup link
is `S12a8-display`. This is a live userspace change; no new bootloader, kernel,
recovery or full firmware image was installed. Source checks cover shared image
pixels, renderer invocation/failure, unsupported formats and an active menu;
the three recovery packing checks also still pass.

The corrected P6/fbsplash helper was then checked in real normal boots.
It completed at 16.04 seconds in the first run, which the owner missed.
After waiting for the owner's explicit readiness, a second normal reboot
completed the draw at 15.84 seconds and the owner confirmed both Tux and text.
OpenWrt, ADB and LTE returned; full aboot/recovery hashes stayed unchanged.

### Clock and battery screensaver, 2026-11-07

After 30 seconds without button input, the LCD shows the carrier and five
signal bars on the top row, a large `HH:MM:SS` clock in the center and battery
percentage with five bars on the bottom row. Each clock digit is 16×32 pixels.
All three positions are fixed. Charging uses the same alternating
bar animation as Status. A live first version with small text and moving
battery was physically confirmed, then revised to the owner's requested
large clock and fixed bottom battery. Clock movement was then removed at the
owner's follow-up.

The view produces a monochrome P6 clock using seven-segment digits. The device
helper uses `fbsplash` with `IMG_TOP=16`, preserving the top and bottom text
rows while updating only the middle 96 pixels. Images are
replaced in `/var/run` (RAM), never written to persistent storage each second.
If the graphics helper fails, the clock falls back to normal-sized text with
the battery still on the bottom row. Leaving the screensaver clears the
graphics before restoring the saved menu and selection.

The first button gesture only wakes the screen, including a held power key
and its release. Further presses use the ordinary menu controls. Pairing
requests wake the screen; pairing and action-confirmation screens never time
out into the screensaver. Model/controller checks cover these cases, fixed
battery position, clock bounds and graphics failure. The complete firmware
and BusyBox package have not been rebuilt for these userspace changes.
The final layout survived normal reboot; all nine installed display files
matched the source inputs. A readback matched the clock's actual RGB565 pixels
in the middle 96 rows, with `LG U+`/signal above and battery percentage below.

### SIM discovery, charging and web ADB follow-up, 2026-11-07

The inserted SIM was being shown as absent because ModemManager exposed no
modem. Its log said `at least a QMI port is required`. The local SMS libqmi
build contained `/usr/local/libexec/qmi-proxy`, but the installed helper is
`/usr/libexec/qmi-proxy`; QMI clients could not spawn it. A compatibility symlink
at the compiled path restored QMI access. Restarting ModemManager then exposed
the modem, a present/ready USIM, home registration and a connected bearer.
Two actual pings bound to `wwan0` succeeded. The symlink is in the persistent
overlay. Rebuilding this standalone library must use `--prefix=/usr` and the
installed libexec directory; its old default `/usr/local` is unsuitable here.

The APN service now distinguishes an unavailable modem, a SIM object still
being probed and an explicitly reported missing SIM. The LCD uses separate
labels for those states. Battery information is on Status; Power retains its
power actions. The status page reads `pm8916-bms-vm/status`, where this kernel
reports charging, with the charger status as a fallback. USB input alone is
not treated as proof of charging. The installed charge limiter stops at 85%
and resumes at 80%. At the observed 100% estimate it had disabled charging;
this setting was retained.

Status order is **BATT / LTE / WIFI / IP / UPTIME**. Battery percentage is
zero-padded to three digits. BATT and LTE share columns 12–16 for five bars:
0% is empty, 1–20% is one bar, and each further 20% adds a bar. Charging
alternates the next segment once per second; stopped charging is static.
Actual LCD readback included `BATT 099%  |||||` and `LTE  LG U+ ||||-`.
The default address row is `IP   192.168.8.1`; longer IPv4 addresses use
the whole row without a label so no address digit is lost.

The web USB configuration omitted ADB despite a valid `usbgadget.adb` section.
It now includes an ADB checkbox and lists ADB in enabled functions. The saved
ADB value was left at `0` during deployment. The owner then enabled ADB through
the button menu; restarting the Mac ADB server restored actual shell access.
The userspace/web files were installed atomically and
their device hashes match their sources. APN inspection and its cache report
`ready`. Both the status service and the
resident button menu (`pwrkey-monitor`) have restarted successfully.
The HTTPS server returns the exact updated USB configuration script; the
owner's confirmed page path matches that view. Live file replacement leaves
LuCI's package timestamp unchanged, so the header now uses an `a8-adb2`
resource version and uhttpd was restarted. The owner then confirmed the ADB
checkbox is visible. Source checks pass: display 30 with
one Linux-only skip, APN 8, USB/ADB 14. This is a live overlay update, separate
from a complete ROM/package build and the separately installed bootloader LCD.

### LCD menu hierarchy, 2026-11-07

The next userspace revision (`a8-display` 1.0-r9) uses four root entries:
**Status, Network, SMS, Power**. APN profiles move into Network and the status
screen replaces its APN row with the Station switch. Network contains separate
AP Wi-Fi and Station switches, VPN, USB, APN profiles, uplink priority and
Bluetooth. VPN contains Tailscale on/off, its cached status and configured
WireGuard interfaces. USB contains ADB, USB networking, serial and reconnect.
SMS shows cached new/stored counts; Power contains the existing reboot,
Recovery, Fastboot, EDL and shutdown actions with confirmation.

AP Wi-Fi changes only AP interfaces, preserving the station setting on the
same radio. USB changes reuse the existing gadget configuration, preserve its
other functions and reject pending web edits. Back and action-result dismissal
return to the immediate parent and keep its selection. Menu opening reads
cached status rather than waiting for modem, Bluetooth or VPN commands.
Twenty-eight local display/menu checks pass; the Linux-only evdev check is
skipped on macOS. The menu model, controller and connection checks also pass
with Lua 5.1.5 on ARM64. **This menu revision is installed as a live overlay.**
All six runtime files match their source hashes; the display and power-key
services restarted, and the status cache now ends with `STATION ON` instead
of APN. The device's Lua model returns the four requested root entries.
An actual AP Wi-Fi off/on check passed, retained Station settings and restored
the original AP settings. Physical menu confirmation is pending. During the
subsequent ADB off/on check, USB disconnected and did not return within the
observation window; ADB restoration and USB menu behavior remain unverified.
In the later follow-up above, the owner's ADB menu action applied successfully
and Mac shell access was restored after restarting the host ADB server.

The later owner clarification is **incoming SMS**: another phone sent an
ordinary SMS to A8. It is not an A8 outgoing-message error. On reconnecting
before this recovery test, ModemManager returned `No modems were found`
despite the modem remote processor and `/dev/wwan0qmi0` being present.
Successful SMS reception remains unverified; a draft-creation check does not
demonstrate delivery.

### Package-manager check, 2026-11-07

`PermissionError: Exec permission denied` is the CGI helper's session-permission
check; `apk`, `package-manager-call` and `cgi-io` have executable permissions.
A temporary session restricted to the installed package-manager ACL reproduced
HTTP 403 without `cgi-io exec`, then listed 333 installed packages over the same
HTTPS endpoint after that permission was granted. The actual install request
reached `apk add kmod-usb-core=6.12.112-r1` and failed its kernel dependency
check. The temporary test session was destroyed. The owner's browser-session
state has not been verified; signing in again is the pending check.

The installed kernel package is
`6.12.112~2667ff797ff9ca465688098855e44f13-r1`, while the available USB-core
package requires `6.12.112~8efa871abf779b5591d7c27483980dfd-r1`.
The failed install left `/etc/apk/world` unchanged and did not install the
package. Running-kernel `/proc/config.gz` reports `CONFIG_USB=y`,
`CONFIG_USB_COMMON=y`, `CONFIG_USB_OTG=y` and `CONFIG_USB_CHIPIDEA_HOST=y`;
`modules.builtin` contains `usbcore.ko` and `usb-common.ko`.
USB core therefore needs no separate package. Other optional kernel-module
packages require a feed built for this image's exact kernel build ID.


**Historical installed baseline:** the first complete OpenWrt build, offline inspection
and EDL installation passed. All 20 final device read-back checks matched,
including all seven radio partitions. The user subsequently reported running
OpenWrt at `192.168.8.1`.
Agent SSH access initially timed out. Later normal-mode ADB, USB NCM and LTE
traffic checks are recorded above; charging and physical RST entry are still
unverified.

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
power input were registered. The LCD was initially white; the owner has now
confirmed readable status and runtime menus, including after returning from
Fastboot. ADB failed to enumerate
because this BusyBox rejects `sleep 0.1`; the manager now uses the available
`usleep`. The source fixes pass 14 USB checks. Applying it through Recovery's
console to the production overlay, verifying its SHA-256 and rebooting into
normal OpenWrt produced a working root ADB shell and file transfer.

Live LCD diagnosis found GPIO11 held **low** after initialization. Linux 6.12's
[`mipi_dbi_hw_reset()`](https://github.com/torvalds/linux/blob/v6.12/drivers/gpu/drm/drm_mipi_dbi.c#L654)
sets logical 0 then 1. The original `GPIO_ACTIVE_LOW` descriptor inverted that
sequence and kept the physical active-low reset asserted. Patch 869 now uses
`GPIO_ACTIVE_HIGH` for this driver's convention. Releasing GPIO11 high,
replaying the stock initialization through the driver's debug command interface
and redrawing produced **visible text, confirmed by the owner**. A minimal
boot-image update derived from `0c75e22` now contains the corrected DTB and the
`aefba08` command line; its full boot partition was read back after EDL writes.
After reboot GPIO11 was high, the terminal was 8 rows × 16 columns, and the
kernel console list contained no `tty0`. Runtime button navigation was observed.
Bootloader LCD output remains a separate check.

The PM8916 correction at `570da06` passed the
[bootloader build](https://github.com/wwwhana/msm8916-openwrt/actions/runs/42216038698)
and [CI checks](https://github.com/wwwhana/msm8916-openwrt/actions/runs/42216043094).
Only its 345,008-byte aboot was installed; the entire 1 MiB partition, both GPT
copies and all seven radio partitions were checked after writing. Reset
succeeded, but normal USB has not yet returned. The physical screen report is
pending. The previously working aboot is prepared from the existing local
artifact if recovery is needed.

**Bootloader recovery, 2026-11-06:** hardware EDL returned as
`QHSUSB__BULK` (`05c6:9008`). The USB path had changed, so the initial watcher
did not select it. After matching both GPT copies and the installed aboot,
the previously working 337,696-byte aboot from `0c75e22` was restored. The
entire aboot partition was read back; all seven radio partitions and both GPT
copies remained unchanged. Verification reads were temporary and removed.
After reset, `OpenWrt Recovery` (`1d6b:0104`) and its root USB ACM console
were observed, running Linux 6.12.112. The owner reported no recovery display;
the recovery init does not start the production LCD status/menu service.
Normal OpenWrt boot was unverified at that restoration checkpoint; it was
subsequently restored and is running in the live update above. No firmware
write is active for the current overlay update.

The LCD also received kernel messages through `console=tty0`, overwriting the
16-column status/menu. The V18 boot command line now keeps only the UART
console; other boards retain their command lines. The live check temporarily
lowered console verbosity and redrew the menu; the kernel log remains readable
through ADB. The observed default font made a 16×16 terminal, whereas the menu
uses eight rows of sixteen characters. The installed V18 boot image selects
`fbcon=font:VGA8x16` explicitly, matching the observed 16×8 grid.

USB NCM initially existed as `usb1` but was down and absent from `br-lan`.
Both ECM and NCM were enabled in UCI. Gadget creation deliberately prefers NCM,
but network setup still tried the nonexistent ECM function and exited under
`set -e` before attaching NCM. Network setup now applies the same priority and
returns success for non-NCM configurations. Four actual shell-function cases
cover RNDIS/ECM/NCM combinations, bringing the USB suite to 14 checks.
On this A8, bringing up and bridging NCM restored the Mac's DHCP lease and
an HTTP response from the management page over its USB interface. That Mac
network check was repeated after the minimal boot update: the Mac's active
`en20` received DHCP and a USB-bound request followed the management redirect
to HTTP 200. This was before the later PM8916 bootloader update above.
Cellular internet was not yet working at this checkpoint on the LG U+ SIM;
later LTE-bound ping passed, as recorded in the live update above.
The owner confirmed that this same SIM provided LTE internet on stock A8.
V12's feature matrix does not establish working features on V18.

### Live SIM, network selection and SMS findings

The modem reported `sim-pin2` while already registered. The APN service
incorrectly treated every nonempty lock as blocking: this produced `SIM LOCKED`
and prevented provisioning. It now follows
[ModemManager's PIN2/PUK2 handling](https://github.com/linux-mobile-broadband/ModemManager/blob/main/src/mm-iface-modem.c),
while PIN1, PUK1 and network locks still block configuration. The fix was
installed through ADB; the LCD left `SIM LOCKED` and the APN worker applied the
profile. Tests feed actual modem responses through the service's lock parser.

APN profiles now expose the current technology preference and a supported-mode
selector. This unit reports LTE preferred, LTE only, 3G preferred and 3G only;
readback was checked with LTE preferred and LTE only. Unsupported combinations, including
3G+LTE without a preference on this firmware, are not offered. The default keeps
the existing network setting. Explicit choices use netifd's `allowedmode` and
`preferredmode`; the standard interface editor's mode ordering is preserved.

SMS Manager's autodetection now uses ModemManager's SIM and own-number data
first, with a corrected QMI `MSISDN:` fallback. On this A8, `+82` was written
and the saved number was checked against the modem without logging the number.
Manual numbers survive detection, staged web edits are preserved, and the
Detect button refreshes the affected fields. No SMS was sent or deleted.
The messaging interface reports SIM/device storage support and responds to
message-list queries. IMS/IMSA queries return `InvalidServiceType`, so IMS
registration and fresh SMS reception are still unverified.

**Korean SMS overlay, 2026-11-07:** the sending page no longer removes the `+`
from international numbers or silently truncates Korean text at 70 UTF-16
units. With a Korean `+82` prefix, domestic numbers such as `010-1234-5678`
are normalized to international form; short service numbers and other
international numbers are retained. The page displays automatic encoding and
an estimated multipart-SMS count. Multipart SMS is not carrier MMS/LMS support.
The old GSM-7 conversion control could erase Hangul and has been removed.

The shared SMS data helper passes the complete UTF-8 text through a mode-0600
temporary file to `mmcli --messaging-create-sms-with-text`, instead of embedding
it in the comma-separated property string. This preserves spaces, commas,
both kinds of quotes and newlines. Both individual and group sends use this
path. Temporary text files are removed on success and command failure.
The receiving page consumes `mmcli -s ID -J` and retains the decoded text and
newlines, rather than parsing and flattening the human-readable output.

SMS settings now offer an optional SMSC for outgoing messages. Empty means
the existing SIM/modem setting; no carrier centre number is hardcoded and
the modem's global SMSC is not rewritten at boot. A custom value is passed
as the new SMS object's `smsc` property. It does not establish IMS registration
or prove incoming SMS service. The inherited Polish `+48` form default was
removed; SIM detection and manually entered values remain in place.

The five changed runtime files were installed through ADB and their SHA-256
values matched the source. Six modem/SIM autoconfiguration tests and the Node
send/receive UI checks passed. On this unit's ModemManager 1.24.0, the actual
HTTPS file RPC created an **unsent** 115-unit Korean draft containing spaces,
commas, both quote types and a newline; its complete text and normalized number
matched on readback. Temporary-file removal and deletion of only the test draft
were checked. A separate unsent draft retained a manually supplied SMSC.
No SMS send command was executed, and existing messages were not deleted.
The modem was connected and registered at home on `45006`. The owner sent an
ordinary SMS, but no incoming message was observed. Outgoing delivery and IMS
registration remain unverified.
This is a live overlay update, not a complete firmware/package build.

**SMS-list and transport diagnosis:** the installed libqmi 1.36.0 serializes
WMS List Messages with Message Tag TLV `0x11` and Message Mode TLV `0x12`.
This firmware returns `MissingArgument (17)` for that request. The Qualcomm
[WMS IDL](https://github.com/os-vector/wire-os/blob/a6e4c3a88da42a08085b084791444f736164b62d/qmi/services/wireless_messaging_service_v01.c)
uses `0x10` and `0x11`, respectively. Direct requests using those IDs succeeded
and found zero 3GPP messages in both SIM and device storage. The repository's
`003-wms-list-messages-tlv-ids.patch` applies those two corrections to libqmi's
message definitions; future image builds receive it through `openwrt-overlay`.

The patched ARM64 musl library was built from the upstream `1.36.0` tag with
MBIM and QRTR enabled, then installed as an overlay file. The same compiled
probe failed with the original library and succeeded with the patched one for
both storage areas, with and without the unread filter. Upstream `test-message`
and `test-generated` passed. ModemManager was restarted, its loaded-library
path checked, and LTE returned to connected/home in 15 seconds. A ping bound
to `wwan0` passed. Both real message lists and the ModemManager list remained
empty after that restart.

The WMS service reports 3GPP/3GPP2 ready, but its transport-layer and transport
network-registration queries return `DeviceNotReady (52)`. Its domain
preference was `CS only (2)`; changing it to `PS preferred (1)` succeeded and
the value remained after the ModemManager restart. No incoming message was
observed after that change. This result does not identify the exact carrier
or IMS provisioning failure. Modem-reset persistence and successful carrier
SMS reception still need verification.

### Korean IMS / TTA investigation, 2026-11-07

**Status: LG U+ IMS SMS works in both directions. ISIM AKA, SIP registration,
private incoming-message storage, the SMSC's acceptance of one outgoing
"하이" message, and the owner's confirmation of its arrival are verified.
Continuous operation, SQLite message/contact storage and the conversation UI
are installed. SKT/KT remain unverified; VoLTE calls, MMS transport and RCS
transport are not implemented.** The attached `mifib-v18,generic` runs OpenWrt
`25.12.6-r8`, ModemManager `1.24.0` and qmicli `1.36.0`. The last completed OpenWrt query reported
`connected`, home registration and operator `45006` (LG U+). Initially neither
QMI CTL nor QRTR advertised IMS Settings (`0x12`) or IMSA (`0x21`), and client
allocation returned `InvalidServiceType`. After the PDC change below, both
services and IMS Presence are available. The original firmware therefore
contains a usable IMS control interface; a missing standalone IMS image was
not the cause of the missing services.

The original `../modem.bin` and `../artifacts/saved/modem.bin` are identical
64 MiB FAT16 images, SHA-256
`be1d63f2266d4df0a7eaa67a944233d8928a724d1b1472b753345f8dbec5189b`.
All nonempty segments described by `MODEM.MDT` are present at the expected
size. Firmware strings include IMS registration, settings and SMS paths.
This is structural inspection, not signature verification or proof of working
IMS. The installed `modem.mdt` and `modem.b17` match the corresponding original
files, with SHA-256 `c2183f38b2c1419b245290ef51d5512d35622259356e2e46566fedaa815ba11a`
and `f90af09f644b8ac9e264ccb4fd2c0565b064623dadeb50861f7fe9fb2938ef4d`.
The live DMS revision is `HM02_MODEM_V1.2`, dated November 4, 2016.

#### Installed configuration differs from the active PDC configuration

The installed `/lib/firmware/MCFG_SW.MBN` is the same `Gen_3GPP` profile as
`packages/qcom-carrier-autocfg/files/mcfg/generic/common/row/gen_3gpp/mcfg_sw.mbn`
and the profile in the original modem image: 24,772 bytes, SHA-256
`51c4a67cbb90a13ed3b8a16ad67a2ab6c5bc483e6afe2fe654e7c4b9f4abd83c`.
`/lib/firmware/mcfg_sw.mbn` is a symlink to that installed file. Its
`/nv/item_files/ims/IMS_enable` record contains a one-byte `01` payload.
That file's presence does not mean that PDC selected it.

The original active PDC entry was **`ROW_Generic_3GPP`, 8,984 bytes, version
`0x02010801`**, ID
`1FF9BC00348CA8376B8F73815A84485E4B914E04`. The other four loaded entries are
Airtel and Chinese carrier profiles. The installed 24,772-byte profile was
absent from that list. Its load completed, and metadata identified it as
`Gen_3GPP`, version `0x02010809`, ID
`8BA00EFD8D33CDB19E930D194D71DF567064ABC9` (SHA-1 of the complete file).
One activation was executed. Readback confirms **Gen_3GPP active** and the
original ROW entry inactive but retained for rollback; no entry was deleted.
The modem reset once, and ordinary LG U+ LTE data recovered with a successful
ping through `wwan0`. QMI and QRTR now advertise IMS Settings and IMSA.

Do not restore automatic activation in the provisioning loop: the existing
reset-loop guard remains necessary. This generic profile restores the IMS
interfaces; it does not establish Korean TTA provisioning or successful SMS.

#### Host support and current live result

The modem predates the modern IMSS `0x0090` settings request, which returns
QMI error 58. The status reader now falls back to the original v1 read-only
requests: registration manager `0x0026`, SMS `0x0027` and QIPCALL `0x0037`.
Live readback gives IMS test mode off, SMS over IP on and VoLTE on. The UI
also avoids rendering a literal `null` child and translates service states.

The `81voltd` 1.2.0 package provides QRTR IMS Data service 770, version 1.
Its A8 patch accepts ModemManager's `wwan0qmi0` QMI port in addition to
`qrtr0`, protects the pre-discovery removal path and adds missing C boolean
headers. A musl/aarch64 native build passed in the build VM under the shared
build lock; the stripped binary is 68,880 bytes, SHA-256
`41ed1f21941e0bb89fa1b1de92dd9f97900b645d58ad8370fe6593fe7b5e54ea`.
It is installed as `/usr/sbin/81voltd` with a procd service. The process is
running and QRTR advertises 770 on the AP. An OpenWrt package/image build
has not been executed.

The earlier modem-only status reader reported settings `yes/yes/yes`, registration
`not-registered`, and SMS/voice `unavailable`. An explicit ModemManager test
with `apn=ims,apn-type=ims,ip-type=ipv4v6` succeeded on `wwan1`: the network
assigned a non-link-local IPv6 address with a /64 prefix. The ordinary
`internet.lguplus.co.kr` bearer on `wwan0` remained connected and its ping
passed. The test IMS bearer was then disconnected and deleted. No modem
Start Connection request to 81voltd was observed.

A legacy registration off/on request was accepted and read back with test
mode off. A DMS reset request was accepted and operating mode returned online.
Neither produced IMS registration. The deprecated APCS complete request was
rejected. Enabling client provisioning/VoLTE for 25 seconds was accepted but
also produced no IMS data request; the original provisioning flags were
restored and read back. IMSA reports registration error code zero, without
a failure reason. These are separate observations: enabling settings and
establishing an IMS PDN do not establish SIP registration. Nine APN/IMS tests
passed, including fragmented
QMI frames, client-ID release on success/failure, and UI status rendering.

The host tool is named **ModemConfig-KO**, the shared entry point for Korean
VoLTE, IMS and later RCS work. The visible menu now separates **메시지**
(`/admin/messages`), **모뎀 설정 → APN / 문자 설정 / USSD / AT 명령**, and
**상태 → Cellular Network (ModemManager) → IMS**. The former mobile-network
page is now an IMS tab in the existing ModemManager page. Operator and modem
connection state use the original modem tab, and data APN appears there once
for the A8's single modem. IMS registration, SMS/voice/RCS availability and the
existing actions are in the IMS tab. There is no second mobile-network menu.
Hidden aliases preserve previous URLs; message connection-status links open
`admin/status/modemmanager#ims`. The shared `a8-ims.js` component replaces the
old standalone view. If the component fails to load, the IMS tab now displays
a warning instead of disappearing. IMS remains visible when ModemManager
returns no devices, and polling preserves the selected tab after the initial
deep link.

The existing `a8-apn` configuration and RPC interface are unchanged. The IMS
reader uses the active host process's private status file, falling back to QMI
when the host process is absent. Eleven APN/IMS checks passed, including the new
tab, hidden aliases, duplicate removal, refresh, empty-modem handling, load
failure visibility and browser menu-cache invalidation. Initial live updates
left the old JavaScript URL and the session-stored menu unchanged. The core
header overlay now includes the A8 web/menu file modification times in LuCI's
resource version and clears the stored menu when that version changes. This
updates both on a normal page reload. The device rendered the updated template;
authenticated HTTPS responses confirmed the new version, matching asset hashes
and one visible mobile-network route. The installed LuCI class loader loaded
and rendered the installed IMS component with mocked RPC/UI dependencies.
Actual IMS RPC reads passed. The user confirmed the IMS tab is visible after
reloading. Browser automation remains unavailable; other interactive actions
were not exercised. The LCD's owner-selected 15/30-second timers were checked
again and preserved. This was a live web/template update; full package/ROM
builds were not run.

The standalone `tools/modemconfig-ko` diagnostic uses pinned `wwan-go v0.0.1`,
`ims-go v0.1.2` and Go 1.26.3. `probe-sim` reads the SIM's IMS capabilities without printing
identities, phone numbers or authentication material. It observed an LG U+
ISIM with private/public identities and a home domain, five physical IMPU
records (three populated), and an SMSC address. The P-CSCF file exists with
five linear-fixed records, with three populated domain-name records. The
corrected decoder resolves those names through the DNS servers supplied by the
IMS PDN, binding the DNS socket to its IMS source address and `wwan1`. This
returned two IPv4 and two IPv6 addresses; the latter are usable on this IPv6
IMS connection. Resolving through the ordinary system DNS before opening the
PDN returned IPv4 only and could not establish SIP. The library's original PSI lookup returned no
value; additional direct ISIM/USIM file requests failed. Those results do not
establish that every SIM PSI path is empty.

After the earlier reboot, the saved WDS profile list contained only profile 1
with an empty APN. On the next OpenWrt connection, `configure-ims` **created
and read back profile 2**, the separate owned `A8 IMS` profile for home PLMNs
45005/45008/45006, with APN `ims`, IPv4v6, P-CSCF-through-PCO and IMCN. It
ran a second time and reused profile 2; registration tests also reused it.
The later multi-SIM implementation leaves that original profile 2 intact and
uses a distinct owned IMS profile per SIM. A8 rejects long profile names and
truncates the final character in profile-list responses. The tool therefore
uses a 15-character name derived from the SIM fingerprint and confirms the
complete name through profile-settings readback before reusing a candidate.
Profile 3 was created and repeatedly reused successfully. Two profiles created
by this test before that truncation fix (4 and 5) were removed after ownership
checks; the final list is 1, 2 and 3. The default Internet profile and working
`wwan0` bearer were preserved. **Post-reboot retention of profile 3 has not
been tested**; the continuous client also verifies or creates its own profile
at startup.

`probe-pdn` is an explicit write/connection test: it creates an owned
`modemconfig-ko-probe` IPv6 profile (previously `a8-ims-probe`) with
P-CSCF-through-PCO and IMCN flags, reads the
flags back, opens its own WDS session on RMNET1, closes the session and deletes
only that profile. It does not replace the default profile or alter host
addresses/routes. A8 requires legacy SIO binding followed by Set Client IP
Family IPv6; omitting that selection failed, while the corrected sequence
connected. The last real run returned IPv6 and confirmed VoPS, but **zero
P-CSCF addresses** and runtime IMCN false, despite both profile flags reading
true. Closing the session and deleting the temporary profile succeeded.
Removing the explicit APN from the start request did not change that result;
that unsuccessful workaround was removed. The diagnostic tests cover
including connection ordering, isolation from RMNET0, private-log suppression,
P-CSCF decoding, normal IMPU selection, serving-cell formatting, private SMS
storage, private SIP metadata, TTA version/algorithm pairing, and cleanup of
only successful network and firewall additions.

`probe-registration` is a bounded Linux IMS registration test, now executed
on the A8. It uses ISIM AKA when present, the normal public identity,
actual device IMEI and serving-cell information without printing them. The
initial path requests an IPv6 IMS PDN on RMNET1, supports SIM P-CSCF records
and PCO, and keeps the existing Internet interface/routes. The default is
TTA-VoLTE/4.0 with SHA-256-128 integrity and null encryption; optional
`tta4-aes`, `tta3-null` and `tta3-aes` diagnostic arguments select AES-CBC or
the R6-defined TTA 3.0 / SHA-1-96 fallback. These four modes retain IPsec
integrity. The local `ims-go-ko.patch` adds an explicit `SMSOnly` configuration
for this diagnostic, retaining `+g.3gpp.smsip` and the IMEI instance while
preserving the library's normal voice Contact features for other callers. One encryption
proposal keeps the bootstrap REGISTER below the library's 1,300-byte TCP
switch threshold (synthetic cases: 1,189–1,196 bytes); two proposals produce
1,318 bytes and are not used. `build.py` tests the patch in a temporary
module view, links unchanged files, and preserves the pinned module cache.

An additional, explicitly selected `android-carrier` comparison follows the
baseline Joan CarrierSettings rather than claiming R6 conformance. In
`common/hardware/lineage/generic-ims/carriersettings/configs`, the LG U+ and
SKT profiles set `ims.sip_over_ipsec_enabled_bool=false`; KT has no explicit
disable and sets SIP port 5080. Android's default is IPsec enabled, port 5060.
These files precede vendor overrides and runtime provisioning, so they do not
prove a live carrier's policy. The inspected Android ImsStack source at
`packages/modules/ImsStack`, HEAD
`972e83adfa58204a9be2f551639185e790196a58`, can complete AKA without creating
an IPsec association when security negotiation is disabled. A missing
Security-Server header is therefore not by itself proof that Android fails.

The new mode keeps AKA authentication, uses SHA-1/AES and port 5080 for KT,
and explicitly disables SIP IPsec only for the SKT/LG U+ baseline comparison.
It does not advertise `TTA-VoLTE/4.0` or silently weaken the strict modes after
a failure. It rejects a conflicting server Security-Server or `sec-agree`
requirement. Synthetic tests passed initial AKA, cached and challenged
renewal, deregistration, unchanged UDP socket ports and rejection of invalid
server authentication proof during renewal. The root configuration mapping
and the complete upstream registration test suite also passed. On the actual
LG U+ SIM, this explicit mode received 401/AKAv1-MD5, performed ISIM AKA,
sent the authenticated REGISTER and received **200**. Registration-event
SUBSCRIBE received 489; the library treats an unsupported event as nonfatal.
This result establishes this observed carrier path, not strict TTA R6
conformance or SKT/KT interoperability.

The test adds owned addresses, DNS/P-CSCF host routes and a separate routing
table selected only by the owned IMS source address on an otherwise unused
`wwan1`. LG U+ SMS replies can target another IPv6 server named in Via; with
only the P-CSCF host routes, that response failed with ENETUNREACH (101),
disrupting the session before SMS storage. The source-specific IMS route
fixed this failure while leaving the main default route unchanged. The code
checks that its routing table is unused, adds instead of replacing, and
removes its source rule before removing its route. LG U+ responses arrive from a different proxy address than
the request destination, so the existing firewall's established-flow rule
does not admit them. A temporary rule accepts only `wwan1` UDP responses to
the owned IPv6 address and actual SIP socket port from source port 5060
(5080 for the new KT baseline comparison).
The program tracks its rule handles and removes them on close, along with
its addresses, source rule and owned routes. Rule/address removal was verified
after the successful 45-second SMS test, and an Internet ping through `wwan0`
still passed. The installed procd service was actually stopped: its process
and status file disappeared, `wwan1` had zero addresses, and its source rule,
routing table and firewall rules were absent. An Internet ping through
`wwan0` passed. Starting the service again restored IMS registration.
SIGHUP follows the same bounded cleanup path as SIGINT/SIGTERM; a physical
USB/modem disconnect remains untested with this build.

In the earlier strict-security tests, **SIP 401 authentication challenges were received** for both
TTA 4.0 / SHA-256-128 proposals (null and AES-CBC encryption). The sanitized
challenge summary reports `AKAv1-MD5`, `ipsec=yes` in the operator control
header, and **no Security-Server header**. The client rejects the incomplete
security agreement and does not send an authenticated REGISTER or report
registration success. A 401 is an intermediate challenge, not successful
AKA or IMS registration. The TTA 3.0 / SHA-1-96 / AES-CBC test transmitted
REGISTER requests, but USB disconnected before a final result. The owner
confirmed another terminal was testing the bootloader; that test and its
cleanup result were **unverified**. The owner subsequently authorized resuming
and reconnected the USB device. Intermittent USB loss and a later low uptime
were observed; this session did not request those later reboots.

The source-routing build completed its 45-second window while remaining
registered. Five original private inbox messages and one later per-SIM message
were received; their bodies and sender identifiers were not printed. Incoming
SIP MESSAGE carried `application/vnd.3gpp.sms` RP-DATA; SIP 200 and RP
acknowledgement received SIP 202. Those **six messages are now in SQLite**.
Each imported body was read back and compared before its old JSON source was
removed. A separate private comparison confirmed all six original message
identities/bodies match the database. New incoming segments and assembled SMS
are committed before positive RP acknowledgement. Durable multipart recovery
across a process restart is covered by a SQLite-backed test. Port-addressed
WAP data is retained as original segments; the subsequent MMS receiver below
now reassembles port 2948 notifications and retrieves their content.

The five original senders were compared privately after the owner asked;
they normalize to one number. On the owner's explicit instruction,
`send-test android-carrier` sent exactly one "하이" to that number, receiving
SIP 202 and `accepted_by_smsc`. The later delivery-report state was unknown,
but the owner explicitly confirmed receipt ("옴"). This verifies actual LG U+
user SMS in both directions. No further user message was sent and none was
deleted. The private send request uses standard input rather than process
arguments or logs.

`probe-slots` observed one card through the old CardStatus interface; SlotStatus
is unsupported on this firmware. When mapping is available, the implementation
follows logical slot 1 to its physical slot instead of assuming physical slot 1.
It validates the selected SIM before AKA and checks again every five seconds;
a changed card or mapping ends that session so the supervisor can reopen it.
The full SIM fingerprint separates profile ownership and private storage.
Physical slot switching, a second SIM and simultaneous dual IMS have not been
tested. No automatic slot-switch command is issued. APN custom settings still
need a complete per-SIM switching flow.

The latest stripped Linux/arm64 binary is at
`/home/builder/a8/ims-build/modemconfig-ko` (13,369,506 bytes, SHA-256
`a0a6787debf49e8060e5ae56bc5815c76dec4b44f41b8a4eb27c64586db55a7f`).
The staged file matched that hash and was atomically installed. After USB
reconnected, the running process's executable matched this hash, its private
status was fresh and registered, and an ordinary LTE-bound ping succeeded.
The latest source's
50 tool/database tests, injected registration-lifecycle cases, complete upstream registration
and SMS suites, and selected root configuration/PDN tests passed. The locked
Linux/arm64 build exited zero. All nine APN/IMS UI/model tests also passed.
The pinned modules and latest binary are retained; temporary module views are
removed by the build script.

The installed `/etc/init.d/modemconfig-ko` runs `run android-carrier` in the
foreground under procd with a single-instance lock, a 15-second stop timeout,
and automatic respawn. Its binary, init script, Lua status reader and LuCI view
all matched their local source/artifact hashes during the preceding build's live
verification. Boot startup is enabled. Actual
stop/start, status removal, owned network/firewall cleanup and subsequent
registration were checked without disrupting the ordinary LTE connection.
Sending SIGTERM to the managed process also verified automatic respawn:
procd launched a new process and it registered after 44 seconds, including
the first P-CSCF timeout/failover. The single-instance lock rejected a second
holder, and an Internet ping passed after automatic recovery.
The live overlay was subsequently observed after a new USB connection with a
low uptime; this session did not request that reboot. During that boot the IMS
PDN returned QMI call-failed error 14 and procd was retrying. USB disconnected
again before a stable-boot registration/data check could complete. Earlier
controlled service restarts and LTE-bound pings had passed; the latest current
network state is **unconfirmed**, and USB failure must not be reported as an
LTE network failure. An OpenWrt package/image integration build has not been
executed, and this standalone Go tool is not yet part of the image definition.
The completed 81voltd build's temporary Alpine root filesystem and archive
were removed; its source archive and latest binary are preserved.

During network diagnosis, `tcpdump-mini` 4.99.6-r1 and `libpcap1` 1.10.6-r1
were installed. Its unsupported link format unexpectedly printed raw
subscriber identifiers and authentication-challenge metadata despite quiet
output options. This was an operator error and was disclosed to the owner.
The capture was stopped, no capture file was written, and raw packet output
is no longer used. No AKA-derived keys were produced in that capture. All
subsequent diagnostics emit only fixed capability flags and numeric status;
do not reproduce the exposed values in this record.

An actual OpenWrt `/sbin/reboot` command completed. ADB access subsequently
recovered after starting its server with USB access outside the restricted
execution environment. Readback confirmed **Gen_3GPP still active**,
`81voltd` running, LG U+ LTE data connected, IMS still `not-registered`, and
**zero messages** in ModemManager. The earlier claim that the device had not
returned was incorrect. After the owner authorized continuing, OpenWrt was
available and the APN/registration tests above ran. The later disconnect
during the TTA 3.0 test was separately confirmed as bootloader testing by
another terminal; this session is leaving that work undisturbed. The earlier
DMS reset and unsupported `adb reboot` request are separate from the actual
full-device reboot. No image was flashed. Those earlier checks preceded the
single explicitly authorized outgoing SMS documented above.

#### SQLite 메시지·연락처 저장 구조 (TTA R9)

설계와 실제 DDL은 [`tools/modemconfig-ko/schema.sql`](../../tools/modemconfig-ko/schema.sql)에
있다. 사용자가 지정한 `TTAK.KO-06.0410R9.pdf`는 212쪽이며 SHA-256은
`3c14a8a74a415542fde11a8984e8a1cf23aaafa0d4c7ed4bc177a9645651bac6`이다.
R9 §1은 통합 메시지(RCS/SMS/MMS)의 RCS 요구사항을 정의하며, SMS/MMS의 망 연동은
각 사업자 규격을 따르도록 한다. 아래 SQL 구조는 이 요구사항을 반영한 구현 설계이며
TTA가 정한 SQL 스키마는 아니다.

| 테이블 | 저장 대상과 설계 근거 |
| --- | --- |
| `subscriptions` | SIM마다 고정된 내부 식별자와 현재 슬롯·통신사. 슬롯을 SIM의 식별자로 사용하지 않는다. |
| `contacts`, `contact_addresses` | 연락처 하나의 여러 전화번호·구분. 연락처를 삭제해도 문자 원본과 상대 주소는 유지한다. |
| `conversations`, `participants` | SIM별 대화와 참여자 이력. R9 §7.3의 Conversation-ID/Contribution-ID, 생성자·초대자, 공용 제목·아이콘과 내 화면의 제목·아이콘을 구분한다. |
| `messages`, `message_recipients` | 공통 본문·송수신 시각·보낸 사람과 받는 사람(to/cc/bcc). 로컬에서 읽은 시각은 망의 읽음 확인과 구분한다. |
| `transport_attempts` | 메시지 한 건의 RCS→SMS/MMS 재전송 이력과 이전 시도 연결. R9 §7.2.4의 CFS 재전송으로 원본 메시지 ID나 실패 이력을 덮어쓰지 않는다. |
| `sms_metadata`, `sms_segments`, `sms_submission_segments` | SMSC/TP 참조값/PID/DCS/긴 문자 조각·포트·원본 TPDU와 각 발신 조각의 결과. 한 조각의 성공을 문자 전체의 전달 성공으로 표시하지 않는다. |
| `mms_metadata`, `mms_pdus` | MMS transaction/message ID, 알림 URL·만료·크기·보고 요청·응답 상태와 원본 알림/수신/보고 PDU. 큰 원본은 전용 파일 경로·크기·해시로 확인하도록 설계했다. |
| `message_parts` | MIME·문자셋·content-id·SMIL 본문·첨부 순서·파일 경로·해시. R9 §7.4의 파일 URL/만료/썸네일/진행률은 MMS 메타데이터와 구분한다. |
| `delivery_events` | 받는 사람별 delivered/displayed/canceled 등 확인 이력. 문자센터 접수를 상대방 전달로 바꾸지 않는다. R9 §7.2.4와 §8.4를 반영했다. |
| `message_changes`, `capabilities`, `drafts` | R9 §8의 답장·공감·취소·수정 참조, 상대 지원 기능의 유효기간, 임시 작성 내용. 현재 화면에 미구현 기능을 노출하지 않는다. |
| `migrations` | 기존 전화번호부를 한 번만 가져오기 위한 기록. |

기기의 기준 저장소는 `/etc/modemconfig-ko/messages.sqlite3`(0600), 상위 폴더는
0700이다. WAL, `synchronous=FULL`, 연결마다 외래 키 검사를 사용한다. SQL과 문자
내용은 sqlite3 명령의 표준 입력으로만 전달하며 로그와 명령 인수에 넣지 않는다.
SQLite 명령줄 도구 3.53.1-r1과 라이브러리를 기기에 설치했다.

실기기에서 기존 수신 문자 **6건**, 대화 **1개**, 기존 연락처 **1개**가 조회된다.
기존 데이터와 충돌하지 않는 임시 연락처로 두 번호 추가, 다시 읽기, 이름·번호 수정,
삭제를 실제 rpcd/ubus 경로에서 확인했고 원래 연락처가 그대로 남는 것을 대조했다.
인증된 HTTPS RPC 조회는 성공했고 읽기 전용 세션의 쓰기 호출은 거부됐다.
서비스를 중지한 상태에서도 SQLite 대화·연락처 조회가 가능했고, 다시 시작한 뒤에도
같은 데이터가 조회되고 IMS가 재등록되는 것을 확인했다.

메시지 화면은 대화 목록·말풍선·검색·여러 번호의 연락처 편집·이전 메시지 조회를
제공한다. 화면 표시, 화면 상태·동작, RPC/SQLite 처리를 분리했다. 입력 중 갱신에
본문을 유지하고 같은 요청의 재시도에는 같은 ID를 사용한다. 발신 대기열은 현재
SIM/IMS 세션에만 연결하며, 재시작 당시 전송 결과가 불확실한 문자는 자동 재발송하지
않는다. 이전에 사용자가 수신을 확인한 시험 문자 이후 추가 망 발신은 하지 않았다.
새 화면의 실제 망 발신, 물리적 SIM 전환과 새 화면의 브라우저 배치는 미확인이다.
USB가 다시 연결된 뒤 최신 실행 파일의 해시, IMS 등록과 LTE 통신을 확인했다.
이 단계에서는 MMS/RCS의 저장 구조만 준비했다. 이후 MMS 수신·첨부파일 읽기와
대화 삭제 처리는 아래와 같이 구현하고 기기에서 확인했다. MMS 발신과 RCS는 미구현이다.

LCD의 SMS 메뉴는 이전 ModemManager 문자함을 조회해 0건으로 표시하던 것을 고쳤다.
SQLite를 읽기 전용으로 조회하며 전체 메시지 수와 수신 메시지의 `local_read_at`으로
안 읽은 개수를 구한다. 실제 메뉴 데이터는 `Stored: 6`, `New: 0`으로 확인했다.
DB 조회 실패는 문자 0건과 구분하며, DB가 없는 구버전에서만 모뎀 문자함을 조회한다.
서로 다른 SIM, 발신·수신, 읽음 변경, 빈 DB와 손상된 DB를 포함한 화면·메뉴 검사
16개가 통과했다.

현재 기기에 적용된 LuCI 테마는 `/luci-static/bootstrap`이다. 초기 대화 화면은
이 테마의 색상 변수를 사용하지 않아 별도 색상과 흰 배경이 나타났고, 정보 창의
글자 대비도 맞지 않았다. Bootstrap의 배경·본문·테두리·강조색과 FeelGood의 해당
변수를 연결했다. 검색 입력란은 Bootstrap의 `content-box` 규칙보다 우선하도록
너비 계산을 고쳤다. 모바일은 목록과 대화를 전환하며, 화면 키보드의 사용 가능한
높이에 맞춰 입력창을 유지한다. 기존 대화의 중복 번호 입력란을 숨기고, 연락처
수정·대화 삭제는 펼침 메뉴로 옮겼다.
입력 유지, 다른 SIM의 발신 차단, 같은 SIM의 연결 갱신, 숨겨진 대화의 읽음 방지와
메뉴 동작 검사가 통과했다. 설치된 JS/CSS 3개를 인증이 필요 없는 HTTPS 정적 경로로
다시 받아 로컬 파일과 해시를 대조했다. 실제 Bootstrap CSS에 필요한 색상 변수
7개가 정의되어 있는 것도 확인했다. Firefox 조작은 화면 자동화 도구에서 승인되지
않아 수정 후 데스크톱·모바일 배치와 실제 화면 키보드 동작은 아직 미확인이다.

#### MMS 수신 복구와 문자 길이 표시

수신이 누락된 원인은 MMS 테이블만 만든 뒤 실제 수신 처리를 연결하지 않은 것이었다.
기기에 남아 있던 포트 2948의 두 SMS 조각을 합치자 LG U+의 MMS 수신 알림이 확인됐다.
[`mms_codec.go`](../../tools/modemconfig-ko/mms_codec.go),
[`mms_store.go`](../../tools/modemconfig-ko/mms_store.go),
[`mms_transport.go`](../../tools/modemconfig-ko/mms_transport.go)에 WAP 알림 해석,
다운로드·본문과 첨부 저장·수신 확인을 연결했다.

수신 형식은 [OMA MMS Encapsulation 1.3 §§6.2–6.3, 7](https://www.openmobilealliance.org/release/MMS/V1_3-20110913-A/OMA-TS-MMS_ENC-V1_3-20110913-A.pdf)을
참조했다. 통신사별 MMS 서버와 SKT 프록시는
[AOSP APN 설정](https://android.googlesource.com/device/sample/+/refs/heads/main/etc/apns-full-conf.xml)의
한국 LTE 항목을 따르며, 다운로드는 `wwan0`에 묶어 IMS 연결인 `wwan1`과 구분한다.
현재 SIM과 맞는 통신사의 서버만 허용하고 다운로드 전후·수신 확인 전에 SIM을 확인한다.
SKT·KT 설정은 코드에 있으며 실제 수신은 LG U+만 확인했다.

실기기에서 기존 알림으로 **MMS 1건**을 복구했다. 수신 원본은 **657,959바이트**,
그 안에는 **PNG 사진 657,468바이트**와 SMIL 302바이트가 있었다.
원본 PDU를 SQLite에 보관하고 첨부파일을 0700 폴더의 0600 파일에 쓴 뒤
`M-NotifyResp.ind`를 보냈다. 망 수신 확인이 성공해 상태는 `mms_received`다.
기존 SMS 6건과 연락처 1개는 보존됐고, LCD 기준 전체 메시지는 7건으로 확인됐다.
새 실행 파일의 실제 실행 해시, 갱신된 IMS 등록 상태와 LTE 통신도 확인했다.

사진 보기·파일 저장은 읽기 권한이 필요한 `read_part` RPC를 통해 제공한다.
한 번에 24KiB씩 읽으며, 기기에서 실제 사진을 27회 호출로 끝까지 읽어
저장본의 SHA-256과 일치하는 것을 확인했다. 첨부 원본은 공개 정적 경로에 두지 않는다.
대화를 지울 때 첨부파일도 지우며 수신 처리 중인 대화는 삭제하지 않는다.
다운로드 실패·만료는 메시지 화면에 표시하고, 수신 확인 실패 시 저장본을 보존해
확인 응답만 다시 보낸다. 일시적인 MMS 서버 오류 응답은 원본으로 보관하면서
다음 시도에 실제 내용을 다시 요청한다. 읽음 확인은 보내지 않는다.

한국어 EUC-KR 본문, multipart 내용과 SMIL·첨부 분리,
조각 저장 뒤 재시작, 다른 SIM의 수신 처리 차단, 저장 전 확인 응답 금지,
확인 재시도 시 재다운로드 방지, 일시적 서버 오류 뒤 복구와 대화 삭제를 검사했다.
도구/DB 검사 44개와 기존 IMS 등록·SMS 검사 및 ARM64 빌드가 통과했다.
화면 검사는 첨부 조각의 순서·크기 확인, 사진 열기, 입력 유지와 SMS 분할 수를 포함한다.
설치된 JS/CSS 5개를 HTTPS로 다시 받아 로컬 파일과 해시가 같은 것도 확인했다.
Firefox 조작 승인이 거부된 상태여서 실제 데스크톱·모바일 화면의 최종 배치는 미확인이다.

이전 `670자`는 국내 통신사 규격이 아니라 코드에 고정한 UTF-16 입력 한도였다.
실제 전송 코드는 GSM-7이면 한 건 160단위·분할 시 153단위, 한글을 포함한
Unicode SMS면 한 건 70단위·분할 시 67단위로 나눈다. 기존 길이 계산 함수를 재사용해
이 수신 복구 단계에서는 `71자 · SMS 2건`으로 표시했으며, 긴 문자를 여러 SMS로
나눠 보냈다. 이후 아래의 통신사별 발신 구현에서 이 동작을 LMS 자동 전환으로 바꿨다.

#### SIM 통신사별 SMS·LMS·MMS 발신과 사진 표시

`message_policy.go`에서 현재 SIM의 통신사 코드로 SKT·KT·LG U+ 설정을 고른다.
Joan이 사용하는 `common/hardware/lineage/generic-ims/carriersettings/configs/`의
`skt_kr.pb`, `kt_kr.pb`, `lguplus_kr.pb`와
[AOSP의 SKT](https://android.googlesource.com/platform/packages/apps/CarrierConfig/+/refs/heads/main/assets/carrier_config_carrierid_1891_SK-Telecom.xml),
[KT](https://android.googlesource.com/platform/packages/apps/CarrierConfig/+/refs/heads/main/assets/carrier_config_carrierid_1890_KT.xml),
[LG U+](https://android.googlesource.com/platform/packages/apps/CarrierConfig/+/refs/heads/main/assets/carrier_config_carrierid_1892_LG-U.xml)
설정을 대조했다. 3사 모두 MMS 1,048,576바이트, 사진 가로 2,592·세로 1,944 제한을
사용하며, LG U+의 `smsToMmsTextLengthThreshold=80`도 적용한다. MMSC와 SKT 프록시도
Joan의 APN 자료와 일치한다. 기업용 문자 API의 SMS 90바이트 기준을 LTE 단말의
문자 기준으로 대신 사용하지 않는다.

SMS는 GSM-7 확장 문자까지 계산한 실제 140옥텟 전송 한도와 통신사의 전환 기준을
검사한다. 한 건에 들어가지 않으면 텍스트형 MMS인 LMS로, 첨부가 있으면 MMS로
전환한다. 장문 본문은 2,000바이트 이내로 검사하며, 표현할 수 있는 문자는 EUC-KR,
이모지 등은 UTF-8로 보존한다. 실제 선택한 문자 인코딩으로 길이를 계산하므로
문자를 잘라 보내거나 이모지를 바꾸지 않는다. 화면은 모뎀의 `measure` 응답으로
`SMS/LMS/MMS`와 `사용량 / 한도 B`를 표시하고, 초과하면 발신을 막는다.

`mms_send.go`는 OMA의 `M-Send.req`를 만들고 SMIL·본문·첨부와 전송 원본을 저장한
다음 현재 SIM을 재확인해 MMSC로 보낸다. `M-Send.conf`의 거래 번호·응답 상태를
검사한 뒤에만 `accepted_by_mmsc`로 기록한다. 이 상태는 문자센터 접수이며 상대방의
실제 수신과 구분한다. 응답이 없거나 전송 중 재시작한 경우 `submit_unknown`으로
보존하고 자동으로 재발송하지 않는다. SMS 작업자는 MMS를 가져가지 않는다.

첨부는 최대 3개이며 JPG·PNG·GIF, AMR·MP3, MP4·3GP를 받는다. 사진은 필요할 때
통신사의 크기·전송량 제한에 맞춰 줄인다. `begin_upload`·`write_upload`·`delete_upload`
쓰기 RPC는 SIM과 연결 세션에 묶이며, 8KiB씩 저장하고 같은 조각의 재요청을 검사한다.
원본은 0700 폴더의 0600 파일에 저장하며, 보내기 전에 내용의 해시와 파일 형식을
다시 확인한다. 미사용 첨부는 1시간이 지난 뒤 다음 첨부 작업에서 정리한다.
SQLite는 버전 2로 갱신하며 기존 메시지·연락처는 보존한다.

사진 메시지는 말풍선 안에 사진을 바로 표시하고 `[제목없음]` 표시는 숨긴다.
사진을 누르면 확대하고 파일로 저장할 수 있다. 화면 밖의 사진은 필요할 때 읽고,
같은 대화의 갱신·확대에서는 읽어 둔 사진을 재사용한다. 대화를 벗어나면 사진
데이터를 해제한다. 실제 Bootstrap의 밝은·어두운 테마 CSS를 사용한 독립 WebKit
검사에서 사진 표시, 제목 숨김, 가로 넘침 방지, 입력창·확대 화면의 범위와 사진
재사용이 통과했다. 폭은 데스크톱 1440px, 모바일 390px이다. 실제 Firefox 조작과
휴대폰 화면 키보드 실물 검사는 미확인이다. 독립 WebKit에서 3,200×2,400 PNG를
전송 한도 이내의 2,592×1,944 이하 JPEG로 줄이는 실제 브라우저 처리도 확인했다.

도구/DB 검사 50개, 기존 IMS 등록·SMS 검사와 Linux/arm64 빌드가 통과했다.
기존 문자 화면 검사와 대화·연락처·SIM 분리·중복 발신 방지, 첨부 쓰기·취소와
길이 초과 표시 검사도 통과했다. A8에서는 DB 버전 2와 `quick_check=ok`,
기존 SMS 6건·MMS 1건·연락처 1개, LG U+ IMS 등록을 확인했다. 실제 ubus로
한글 70자→SMS, 71자→LMS, 1,000자→LMS 허용, 1,001자→초과 차단을 확인했다.
시험 PNG 49,363바이트를 7조각으로 쓰고 첫 조각 재요청·파일 전체 해시 일치·삭제도
확인했다. DB 갱신 전 백업과 기존 메시지·연락처·첨부 기록을 비교하고, 저장된
첨부파일의 전체 해시가 일치하는 것도 확인했다.

사용자 승인으로 기존 수신 확인 번호에 SMS·한글 LMS·사진 MMS를 각 1건씩
실제 발신했다. SMS는 SMSC 접수, LMS·MMS는 `M-Send.conf`의 성공 상태
`0x80`과 MMSC 접수를 확인했고, 사용자가 3건 모두 도착했다고 확인했다.
발신 3건은 `thread` RPC에서 본문·유형·상태가 맞으며, 사진 첨부도 `read_part`로
다시 읽어 저장본 해시가 같은 것을 확인했다. SMS의 망 전달 보고가 없으면
`delivery_unknown`을 유지하며 사용자 수신 확인을 망의 전달 보고로 바꾸지 않는다.
이어 사용자가 보낸 한글 장문 310자를 텍스트형 MMS로 받아 `mms_received` 상태와
대화 저장을 확인했다. 앞서 확인한 짧은 SMS·사진 MMS 수신과 합쳐, 현재 LG U+
SIM에서 SMS·LMS·사진 MMS의 양방향 실기기 동작을 확인했다.
SKT·KT 실물 SIM 송수신, 물리적 듀얼 SIM 전환, RCS와 새 전체 이미지 빌드는 미실행이다.

#### LTE 문자 경로 선택

[SKT의 2026-08-20 공지](https://news.sktelecom.com/229465)와
[KT의 3G 종료 안내](https://my.kt.com/etc/ThirdGenEndInfo.do)를 확인했다.
TTA R6 §9는 NAS와 IP 문자 처리를 모두 요구한다.
[TS 23.272 Annex C](https://www.etsi.org/deliver/etsi_ts/123200_123299/123272/15.00.00_60/ts_123272v150000p.pdf)는
MSC와 SGs 연결 없이 LTE NAS로 문자를 처리하는 SMS in MME도 정의한다.
따라서 NAS를 WCDMA 전용 방식으로 간주하지 않는다. 실제 통신사 내부의 SGd·SGs
사용 여부는 이번 A8 시험으로 확인하지 못했다.

`ModemConfig-KO run android-carrier`의 기본 문자 경로는 SKT(`45005`) NAS,
KT(`45008`)·LG U+(`45006`) IMS다. Joan의 CarrierSettings와
[조사 당사자의 2025-09-18 인터뷰](https://news.sbs.co.kr/news/endPage.do?news_id=N1008267176)를
대조한 기본값이다. 전체 단말·요금제에 고정된 통신사 계약으로 취급하지 않는다.
[TS 24.167 §5.28](https://www.etsi.org/deliver/etsi_ts/124100_124199/124167/19.01.00_60/ts_124167v190100p.pdf)의
통신사별 선택을 반영할 수 있도록 SIM별
`/etc/modemconfig-ko/sims/<ICCID SHA-256>/sms-transport`에서 `ims` 또는 `nas`로
덮어쓸 수 있다. 파일 권한은 0600이며 잘못된 값·심볼릭 링크는 거부한다.
SIM을 바꿔도 다른 SIM의 설정을 가져오지 않는다. 통신사 OMADM 설정을 자동으로
받아 이 파일에 반영하는 기능은 아직 없다.

NAS는 IMS 등록과 독립적으로 SMS·LMS/MMS 작업자를 실행한다. LTE만 허용된 상태와
LTE PS 연결을 확인하고 QMI WMS를 논리 기본 SIM에 연결한다. 문자 도메인은
PS 전용(`3`)으로 설정하고 다시 읽어 확인한다. PS 우선(`1`)으로 대신하지 않는다.
WMS 준비 상태·수신 경로·SIM을 주기적으로 검사하고 발신 직전에도 확인한다.
SIM 데이터 다운로드용 class 2 경로는 보존하고 일반 문자는 자동 수신 확인 없이
호스트로 넘긴다. 시작 시 DB 쓰기 준비를 마친 뒤 모뎀에 저장 공간이 있음을 알린다.
종료 시 같은 SIM에서 아직 이 작업자가 소유한 도메인·수신 경로만 복구한다.

NAS의 3GPP TPDU는 기존 IMS 문자 인코딩·해석 코드를 재사용한다. 수신 원본을
SQLite에 저장한 뒤에만 수신 확인을 보내며, 바이너리 WAP Push는 기존 MMS
재조립·다운로드 경로로 전달한다. SIM이 바뀔 때 공용 NV에 남은 문자를 현재 SIM의
문자로 가져오지 않는다. 발신은 `SMSOnIMS=false`로 한 번 요청하고 응답이 불명확하면
`submit_unknown`으로 보존한다. 모뎀의 전송 응답만으로 상대방 전달 완료를 표시하지
않는다. SQLite 버전 3의 `nas_status_reports`에 전달 보고 원본을 저장하고 SIM·번호·
TP-MR·문자센터 접수 시간을 함께 대조한다. TP-MR이 겹쳐 대상을 하나로 정할 수
없으면 기존 문자의 전달 상태를 바꾸지 않는다.

상태 화면과 메시지 API는 IMS 등록, SMS 준비, MMS 준비, 문자 전달 방식을 각각
표시한다. IMS 미등록이어도 NAS가 준비되면 SMS를 사용할 수 있으며, SMS와 MMS 중
한 경로만 준비된 경우 그 경로로 보낼 수 있는 메시지만 허용한다.

도구/DB 검사 56개와 기존 IMS 등록·SMS 검사, 정적 Linux/arm64 빌드가 통과했다.
새 검사에는 LTE·PS 전용 제한, 설정 소유권과 복구, SIM별 선택, 저장 후 수신 확인,
한글·WAP Push 해석, 중복 수신·중복 발신 방지, 재시작 뒤 전달 보고 처리와 TP-MR
충돌 거부가 포함된다. 문자 화면 JS 검사, SIM 자동 설정 검사 6개, APN·상태 검사
9개도 통과했다. 빌드 스크립트는 OpenWrt에서 실행할 수 있도록 CGO를 끈다.

A8에서 `probe-nas`의 PS 전용 쓰기·재조회·원래 값 복구가 통과했다. 현재 LG U+
SIM의 선택을 잠시 NAS로 바꿔 실제 WMS 수신 경로 설정과 준비 상태를 확인하고,
8초간 실행한 뒤 원래 선택으로 복구했다. 이때 메시지 RPC는 `ims_registered=false`,
`sms_ready=true`, `mms_ready=true`였고 상태 조회도 IMS 미등록과 NAS 준비를 구분했다.
종료 후 문자 도메인은 원래 CS 전용 값(`2`)으로 돌아갔다. 이어 LG U+ IMS의
401 인증 요청 뒤 200 등록과 실행 중 서비스의 등록 상태, LTE `wwan0`의 외부
ping 2건 성공을 확인했다. 추가 사용자 문자는 발송하지 않았다.

SQLite는 버전 3으로 갱신됐고 `quick_check=ok`, 외래 키 오류 0건이다. 갱신 전
백업과 비교해 기존 메시지 11건·연락처 1개·첨부 8개가 모두 보존됐으며 해당 원본
행의 누락·변경은 0건이다. 새 화면 JS 4개도 A8의 HTTPS 응답과 로컬 해시가 일치한다.
원래 바이너리 한 벌은 `/usr/sbin/modemconfig-ko.before-nas`, DB 백업 한 벌은
`/etc/modemconfig-ko/messages.before-nas.sqlite3`에 보존했다.
현재 바이너리는 VM `/home/builder/a8/ims-build/modemconfig-ko`와 A8
`/usr/sbin/modemconfig-ko`에 있으며 SHA-256은
`0a6dc6b554ae71a24e7e7ed9882da596deabb9fb56fa594dfa6544954b3f78aa`다.
소스 수정·로컬 검사·A8 반영과 실행 확인을 마쳤다. SKT·KT 실물 SIM 송수신,
새 NAS 경로의 실제 문자 발신, 물리적 SIM 전환·재부팅 확인, 전체 이미지 빌드와
커밋·푸시는 미실행이다. VoLTE 통화와 RCS는 아직 구현되지 않았다.

#### 2026-11-07 통화 연결·종료 및 음성 처리 수정

이 항목은 위 NAS 도입 당시 기록 이후의 상태다. LG U+ SIM의 실제 발신에서
1,683바이트 INVITE가 UDP로 전송되고 `100 → 180 → 200 → ACK`로 연결됐다.
사용자가 웹에서 종료한 통화는 BYE 전송 후 160ms 만에 200 응답을 받았으며,
점유가 해제되고 SQLite `calls`에 완료·40초로 저장됐다. 음성 데이터는 양방향으로
오갔지만 사용자가 상대방에게 전달되는 음질 불량을 보고했다.

Joan의 LG U+ CarrierSettings에 맞춰 SIP를 UDP로 유지한다. 기존 라이브러리는
1,300바이트를 넘는 통화 요청을 TCP로 바꿔, 변경 전 시도는 전송 기록 없이
45초 동안 기다리다 실패했다. 취소 의사를 SIP 계층에 먼저 전달하고 마이크·화면
점유를 정리하며, 늦게 도착한 이전 통화 응답이 다음 통화 상태를 덮지 않게 했다.
브라우저 마이크 승인 대기 중 취소·늦은 승인·점유 요청과 취소가 겹치는 경우도 처리한다.
통화 기록 조회 실패는 현재 통화 조회와 종료 동작을 막지 않는다.

A8에서 합성음 100프레임을 측정한 AMR-WB 변환은 mode 2에서 평균 27.5ms로
20ms 음성 처리 기한을 넘었다. 별도 모드 비교에서는 mode 0/1/2가 각각
8.2/13.6/21.8ms였다. 송신은 상대와 협의한 WB 모드 중 가장 낮은 모드를 사용하고,
수신에는 기존 모드를 모두 유지한다. RTP에 넣는 20ms 음성 프레임 수와 전송 간격·
시간값도 협의된 `ptime`에 맞췄다. 음질 개선의 실제 통화 확인은 아직 남아 있다.

이후 실제 착신에서 INVITE 수신과 100·183·180 응답은 확인됐으나, 사용자가
상대 휴대폰의 통화 실패를 보고했다. 첫 수신 통화가 남아 후속 시도에는 486을
반환했다. SIP 응답 생성기가 INVITE의 `Record-Route`를 누락하는 결함을 찾아
[RFC 3261 §12.1.1](https://www.rfc-editor.org/rfc/rfc3261.html#section-12.1.1)에
맞춰 180·183·200 응답에 원래 순서와 내용을 유지하게 수정했다. 수정 후 실제 착신은
200·ACK로 연결되고, 27초 뒤 상대의 BYE에 2ms 만에 200을 반환했다. 종료 후
점유도 해제됐다. 그러나 사용자가 브라우저에서 말한 소리가 상대에게 전달되지
않는다고 보고했으므로 음성 통화는 아직 정상 동작으로 확인되지 않았다.

브라우저 음성 전송은 요청 응답 시간과 다음 대기 시간이 누적되지 않게 수정했다.
음성과 통화 점유 요청에는 LuCI의 화면 갱신 대기 묶음을 적용하지 않고,
RPC 오류도 숨기지 않게 했다. A8의 `rpc.js`와 `luci.js`에서도 이 대기 경로와
`nobatch` 지원을 확인했다.
합성 입력을 브라우저의 실제 음성 처리 코드에 넣어 48kHz에서 16kHz로 변환된 값과
전송 크기를 확인했다. AMR-WB 합성음은 별도 FFmpeg 디코더로도 100프레임씩
복원됐다. mode 0/1/2의 원음과 최대 상관값은 각각 0.8667/0.9535/0.9953이었다.
이는 합성음 변환 확인이며 실제 마이크 전달이나 음질 확인을 대신하지 않는다.

실기기의 마이크 입력 프레임·음량·입력 누락 횟수 계측을 검사·빌드해 반영했다.
IMS 재등록과 계측 항목 조회를 확인했으며, 사용자 통화에서의 값은 아직 미확인이다.
현재 실행 파일과 VM 산출물의 SHA-256은
`325e9800b131a246d4eed2a055bcc96cf9adece2df64f12c9da04ee7bbb159a7`로
일치한다. 브라우저 JS 2개도 기기 파일과 해시가 일치한다. 사용자 음성은 파일에
저장하지 않는다.

SQLite 버전은 5이며 기존 연락처와 메시지를 유지한다. 모뎀 프로그램과 음성 처리
검사, 브라우저의 통화·연락처·늦은 마이크 승인 취소 검사가 통과했다. 수정 실행 파일을
A8에 반영했으며, 전체 OpenWrt 이미지 빌드·커밋·푸시는 미실행이다.
SKT·KT 실물 SIM의 통화와 RCS 사용자 메시지 송수신도 미확인이다.

#### 2026-11-08 무음 입력과 음성 대기열 처리

사용자가 "음성 전송이 지연되고 있습니다"를 보고한 통화는 19초였다. A8이 받은
773개 입력 프레임은 최대 음량이 0이었고, 대기열이 차서 147개 프레임이 거절됐다.
송신은 805개, 수신은 120개였으며 종료 후 점유는 해제됐다. 이 기록은 마이크가
무음을 전달한 사실과 입력 거절을 보여 주며, 상대에게 실제 음성이 들렸다는 뜻은 아니다.

대기열이 차면 오래된 입력을 제거하고 최근 입력을 받도록 변경했다. 이때 상대 음성의
조회도 계속 처리한다. 과부하 상태에서 입력 순서·음량·누락 수가 유지되는 검사를
추가했다. 블루투스 입력은 이미 같은 방식으로 처리되고 있었다.

음성 RPC는 큰 Go 프로그램을 매번 실행하는 대신, 작은 Lua 전달기가 기존의 전용
소켓에 요청한다. 기기 안에서 같은 상태 조회를 각각 8회 실행한 평균은 기존 경로
41.20ms, 새 경로 25.18ms였다. SIM·통화 점유 검사는 기존 모뎀 서비스가 계속
담당하며 rpcd 인증·권한도 유지한다. 실제 rpcd 실행 경로를 통해 점유·유지·해제를
확인했고, 통화가 없을 때 음성 요청이 거부되는 것도 확인했다. 실제 통화 중 음성
전달은 아직 미확인이다.

추가로 기기에서 합성 입력을 100프레임씩 변환했다. WB mode 0/1/2의 무음 평균은
각각 7.3/10.3/10.8ms, 합성음 평균은 5.7/9.6/19.5ms였다. mode 2 합성음의
최댓값은 70.4ms여서 WB mode 0을 유지했다. 이 시험의 임시 실행 파일은 확인 후
기기와 VM에서 제거했다.

전화 화면에는 실제 브라우저 마이크 선택, 입력 음량, 마이크 확인을 추가했다.
마이크 확인은 3초 동안 브라우저에서 입력만 측정하고 마이크를 닫으며, 통화를 걸거나
음성을 모뎀·상대에게 전송하지 않는다. 입력 장치를 바꿀 때도 현재 통화의 음성
전송 순번을 유지한다. 합성 입력·장치 선택·로컬 마이크 확인·늦은 취소·점유 잠금과
화면 상태 검사는 통과했다. 실제 브라우저 화면과 사용자의 마이크 신호는 미확인이다.

수정 프로그램의 검사·Linux arm64 빌드와 A8 반영·IMS 재등록을 확인했다.
실행 파일 SHA-256은
`08b88d8086b19277f139c709dd2a19749b9277f048fbff95ee0baa7c74ea401b`이며
VM 산출물과 일치한다. 브라우저 파일·RPC 전달기도 기기에서 다시 읽은 해시가
소스와 일치한다. 전체 이미지 빌드·커밋·푸시와 실제 양방향 음성 확인은 미실행이다.

#### 2026-11-08 실패한 통화 정리와 USB·모뎀 부하 확인

A8의 `modemmanager.sh`가 실행 권한 없이 설치돼 있었다. netifd 로그에는 약 11초
동안 `modem` 내려감·시작이 각각 900번 반복됐고, USB 기록도 로그 버퍼에서 밀려났다.
기기와 소스의 실행 권한을 복구한 뒤 `modem`이 `wwan0`으로 연결됐으며, CPU 유휴
비율은 약 10%에서 84–86%로 늘었다. ADB 조회 8회의 중앙값은 17.9ms, 최대는
21.0ms였다. 패키지 준비 검사에도 이 스크립트의 실행 권한 확인을 추가했다.

USB 관리자는 machine-id가 없는 A8에서 실행 시각과 PID로 식별자를 만들고 있었다.
식별자는 이제 `/etc/usb-gadget.serial`에 한 번 저장하고, USB 네트워크 주소도 같은
값에서 만든다. 기기에는 현재 식별자를 유지한 채 적용했다. 실제 USB 구성을 두 번
다시 만들어 각각 8.5초·9.4초 안에 ADB가 돌아왔고, 두 번째에도 식별자와 네트워크
주소가 유지됐다. 데이터와 IMS 준비 상태도 복구됐다. USB 관련 검사 16개가 통과했다.
이는 USB 소프트웨어 재구성 확인이며 물리적 케이블 분리·재연결 검사는 미실행이다.

통화가 200 응답 뒤 음성 조건을 처리하지 못하면 ACK·BYE를 보내고 실패로 정리한다.
착신 응답 후 ACK를 기다리는 중의 종료 요청도 먼저 반영하고, 늦은 ACK나 ACK 시간
초과가 통화를 다시 살리지 않게 했다. 종료된 착신의 원래 INVITE가 반복 도착하면
기존 응답만 다시 보내며 새 수신 알림을 만들지 않는다. 통화 실패 시 화면 점유·음성·
대기 중 발신을 정리하고, 착신 응답 실패는 통화 기록에도 실패로 저장한다. A8에서
가상 SIP 상대를 사용하는 실패·종료 검사 7개가 통과했다. 실제 rpcd 경로의 점유,
다른 창 거부, 유지와 해제 쓰기도 확인했다. 이 검사는 통신사 번호로 전화를 걸지 않는다.

이후 사용자 발신 시험에서는 INVITE만 나가고 통신사 응답이 없었다. USB 관리자가
방화벽을 다시 읽을 때 실행 중 IMS 수신 규칙이 사라지는 경로를 확인했다. 현재는
OpenWrt fw4의 `chain-pre/input` 자동 포함 경로에서 실행 중인 포트 규칙을 다시 읽는다.
규칙 파일은 `/var/run/modemconfig-ko-firewall.nft`에 권한 0600으로 저장하고 종료 시
제거한다. 방화벽 갱신 후에는 현재의 정확한 설명 표식으로 규칙을 찾아 지우므로
바뀐 규칙 번호로 다른 규칙을 지우지 않는다. 저장 실패 시 새로 연 포트만 되돌린다.

수정 프로그램의 전체 앱 검사, 등록·문자와 관련 통화 검사, Linux arm64 빌드가
통과했다. A8 실행 파일과 VM 산출물의 SHA-256은
`bd0e5ba78acd6672cf806a7845cf48b97f6bac7aab897eefc053d373b6bb4b9d`로
일치한다. `fw4 check`가 통과했고, 실제 방화벽 갱신 전후의 IMS 규칙 2개가 같은
조건으로 유지되며 서비스 프로세스·데이터 연결·IMS 준비 상태가 유지됐다.

11:08 사용자 시험에서 발신 12초·착신 5초 통화가 연결됐다. 사용자는 양방향 음성이
정상이라고 확인했다. A8의 브라우저 입력은 발신 550프레임 중 음성 220프레임,
착신 260프레임 중 음성 128프레임이었으며 입력 대기열에서 버린 프레임은 모두
0개였다. 두 통화 모두 BYE의 200 응답, 점유 해제와 SQLite의 `completed` 기록을
확인했다. 종료 후 관찰 중 새 발신이나 재울림은 없었다. 상대가 먼저 종료하는
추가 착신 시험은 사용자가 다시 걸지 않았다고 확인해 미실행·미확인으로 남긴다.

과거 무음 입력의 단일 원인은 아직 미확인이다. macOS의 Firefox 마이크 권한이
켜져 있고 입력 장치와 웹 선택 모두 MacBook Pro 마이크였으며, 새 수정본에서는
위와 같이 실제 음성이 전달됐다. A8와 빌드 VM의 임시 시험 파일·교체용 백업은
검증 뒤 정리했다. 전체 이미지 빌드·커밋·푸시는 미실행이다.

#### TTA R6 requirements for SKT, KT and LG U+

The user-provided `TTAK.KO-06.0357R6.pdf` exists, has 64 pages, and matches
the Joan record's SHA-256
`f3a12507115e0d6f9d666754663cdeafab3df2ab59aeba6ea0a970f29945116d`.
The relevant clauses were checked in the original, including complete PDF
pages 11–13, 32, 37 and 40 (printed pages 4–6, 25, 30 and 33).

| Requirement | Shared behavior for SKT (`45005`), KT (`45008`) and LG U+ (`45006`) |
| --- | --- |
| Registration, §5.2.1 | Perform SIP registration, renewal and registration-event subscription. Advertise `+g.3gpp.smsip` when providing SMS over IP; include the IMEI URN in `+sip.instance`. |
| Authentication, §5.2.2 | Support ISIM AKA, with USIM fallback when ISIM is absent, security agreement and IPsec integrity protection. R6 defaults to HMAC-SHA-256-128; its HMAC-SHA-1-96 fallback uses the TTA-VoLTE/3.0 user-agent version. |
| User-Agent, §§5.2.1–5.2.2 | R6's normal version is `TTA-VoLTE/4.0`. Model/build describe the A8; the operator token identifies the launch operator or open-market device, not whichever SIM is inserted. Do not copy Joan's V300L identity or TTA-VoLTE/2.0 string. |
| IMS data, §7.4.1 | Establish the separate IMS APN connection for SIP signaling and discover P-CSCF. Keep the working internet APN for ordinary data; do not substitute `ims` for it or use it for initial attach. |
| SMS transport, §§9–9.1 | Support NAS and IP transport. Select the transport from operator provisioning rather than forcing one common choice for all three networks. |
| IMS SMS, §9.3 | Use the operator-provisioned PSI for the SIP Request-URI, and implement transmit, receive, reports and memory-available handling from TS 24.341. An outgoing telephone SMSC field is not that IMS protocol implementation. |

Joan's current shared source
`common/hardware/lineage/generic-ims/carriersettings/src/com/android/carriersettings/VendorOverrides.kt`
also records two concrete interoperability fixes: retain
`algorithm=AKAv1-MD5` in AKA authorization, and form a SIP URI for a bare-host
SMS PSI rather than a `tel:` URI. These are useful protocol references, but
Android CarrierSettings and ImsStack do not run merely by copying their
configuration into OpenWrt. The older RCS provisioning patches address a
different service and are not an ordinary IMS SMS implementation.

The next required results are physical SIM-switch testing and per-SIM APN
settings, post-reboot verification, OpenWrt package/image integration,
a live transmission from the new conversation UI, and carrier-specific VoLTE,
MMS transmission and RCS implementation. LG U+ MMS reception is verified above.
Only an LG U+ SIM was observed; SKT/KT registration
and SMS remain **unverified**. Strict TTA R6 conformance is also unverified.
The owner's prerequisite for starting RCS work, successful ordinary IMS SMS,
has now been met. The existing Joan/shared implementation provides ACS
fetching, provisioning-document parsing, separate RCS SIP registration and
TLS/MSRP handling in Android Java/C++; these require a Linux implementation
and are not supplied by the currently SMS-only Go client. The Korean ACS
endpoint mapping is in CarrierSettings `VendorOverrides.kt`. No RCS
provisioning request or RCS user message has been sent from this A8 yet.
The PDC configuration was written as described above;
modem executable images were not flashed and sensitive NV backups were not read.

During the earlier data bring-up, connection attempts repeatedly returned `emm-invalid-state` after an
initial-EPS profile rewrite failed with `invalid-parameter-length`. The
inherited `init_epsbearer=default` was removed on this unit and the one-time
V18 migration removes that inherited setting on installation. Custom settings
and later manual choices are retained. This stops the failing rewrite; it
does **not** establish a working LTE data session. Subsequent NAS queries show
`registration-denied`, and ModemManager reports `plmn-not-allowed`; LTE signal
is present. This is a registration failure, before a usable WAN device exists.
LuCI's `wan modem: (empty)` therefore has the correct zone assignment but no
active modem device. Tailscale reports DNS/control
server reachability failure and `NeedsLogin` while cellular data was down.
These describe that earlier failure, not the later successful LTE-bound ping
or the subsequent station-uplink state in the current table.

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
| SIM | Matching stock description for GPIO119/112/12 selection and GPIO114 re-announce. Network registration has been observed; hot-swap remains untested. |
| LCD | The compared MTP DTB prefers the 128×128 GC9107 through `qcom,spi-pref-prim-pan`; it also contains an ST7735S description. SPI5, GPIO11/30 reset/DC, L17/L6 supplies and the MPP4 current sink are described below. Readable pixels and runtime menus have been confirmed by the owner. |
| Wi-Fi bands | The initial mainline description selects `qcom,wcn3620`, inherited from V12. [Linux treats WCN3620 as 2.4 GHz only](https://github.com/torvalds/linux/blob/v6.12/drivers/net/wireless/ath/wcn36xx/main.c#L1345). This is a port configuration, not a physical RF-chip identification or a measured A8 channel list. |
| Bluetooth | The mainline DT contains the WCNSS Bluetooth device over SMD, and the image includes `btqcomsmd`, `btqca` and BlueZ. iPhone and iPad pairing and Internet access are owner-confirmed with SC disabled for initial pairing; the iPhone's saved bond also reconnects with SC restored. Optional LCD/web pairing compatibility is installed, off by default. Other clients remain unverified. |
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

The Tux/font follow-up at `c585927` passed bootloader-only
[run 42206750250](https://github.com/wwwhana/msm8916-openwrt/actions/runs/42206750250).
Its aboot is 345,024 bytes, SHA-256
`94708c238fbb0040396dd298d702b4f999d6cd3f1b96323e916de6a26594a342`.
It was installed and its complete partition was read back on 2026-11-06.
The other four boot-chain components were already identical and were not
rewritten. Both GPT copies and all seven radio partitions remained unchanged.
The full root filesystem remains `0c75e22`, with the documented live fixes
in its persistent overlay. Existing backups are reused; do not create duplicates.

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
enabled after drawing, and the splash stays for 750 ms. Recovery entry now
bypasses the bootloader LCD and uses its independently confirmed Linux display
helper. The production Fastboot path starts USB before its LCD splash. The RAM
diagnostic instead waits for an explicit LCD command, so USB can be checked
first. Returned PMIC errors skip display initialization; the observed hardware
failure on the first direct L17 write is still unresolved.

Normal boot then shows OpenWrt, Recovery, Fastboot USB and EDL. Without input it starts
OpenWrt after five seconds; the first button interaction cancels automatic
boot. Explicit recovery/fastboot reboot requests, reset-key recovery and
automatic boot-failure recovery retain priority. The selecting key must be
released before handing control to the selected mode.

The boot display uses conservative, write-only GPIO SPI mode 0 on
MOSI16/CS18/CLK19, the same 140-byte panel sequence as Linux, the stock
reset timing and MPP4 current sink. It checks that shared L6 is already
at 1.8 V and already enabled, without writing its registers.
L17 is also checked at the stock 2.85 V without writing its registers.
MPP4 writes are read back. If a required rail setting or PMIC
access is unavailable, it logs the failure and continues normal boot.
The installed `c585927` logged `A8 LCD unavailable`: its older PLDO range decoder
rejected PM8916's valid range 0 before drawing anything. Fastboot readings gave
L6 range 0/selector 4 and L17 range 0/selector 88, matching 1.8 V and 2.85 V in
[Linux's PM8916 PLDO table](https://github.com/torvalds/linux/blob/v6.12/drivers/regulator/qcom_smd-regulator.c).
The follow-up uses that encoding for both rails and retains both voltages.
A test compiles the actual panel initialization function against PMIC fixtures,
checking those observed values and rejecting disabled rails or different voltages before
any write. The earlier installed correction failed and was replaced by the
known-working baseline. The latest shared-rail repair was physically confirmed
with Tux and text in RAM Fastboot, then installed after the normal RAM path
also passed. The owner confirmed the installed Boot menu before Linux.

Controller tests also reproduced three indefinite waits in the actual menu
function: a power key held throughout startup, a 20 ms input bounce, and a
selecting key that never releases. Navigation now uses the debounced state,
and release waiting ends after two seconds. Normal timeout and short-then-long
Recovery selection remain covered. This correction is included in the
installed primary bootloader.
The hardware-free Tux test compiles the actual pixel helper and checks its
RGB565 output, exact image hash and black margins. The owner subsequently
confirmed Tux on the unit in RAM Fastboot and with the Linux recovery renderer.

After OpenWrt starts, a press opens the LCD menu. It offers status, automatic
APN selection or an existing valid saved APN profile, reboot, Recovery,
Fastboot, EDL and power-off. Profile changes and power actions first show
**Cancel**, followed by **Confirm**. Create/edit profiles through LuCI's
Network → APN profiles page. Choosing a profile saves only `a8_apn`
settings; the existing carrier worker applies it with its normal no-SIM
handling. No extra APN worker is started.

BusyBox's ordinary `reboot bootloader` ignored the requested mode on this
image. Runtime menu actions now use the dedicated `reboot-bootloader`,
`reboot-recovery` and `reboot-edl` helpers after syncing. Fastboot entry through
the installed menu backend was checked on the device (`lk1st-msm8916`), followed
by a successful return to OpenWrt and an owner-confirmed runtime display.
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
Actual SIM/APN switching and fresh SMS reception remain unverified. LTE-bound
ping passed later; the current data-interface state is recorded in the live
overlay table.

## Bluetooth and USB host limits

The running system contains BlueZ 5.83, `bluetoothctl`, the Qualcomm SMD
driver and `bnep.ko`. Controller discovery, power control and an incoming
LCD confirmation request are observed. The user's requested direction is
**router Internet → phone/PC**: the NAP server registers on `br-lan`, using
its existing LAN DHCP, DNS and forwarding. PANU, which uses another device's
Internet connection, is not configured by this change. A registered NAP
service is not evidence that a client has paired or received an IP address.

The adapted [sbwml LuCI package](../../packages/luci-app-bluetooth/README.md)
and `bluez-network` are included in the V18 source profile and installed in
the live overlay. Tcl/Expect and audio packages are not required. OpenWrt's
normal `bluez-tools` package only installs `bt-agent`, so `bluez-network`
builds the upstream `bt-network` utility separately. Its registered
[NetworkServer1](https://github.com/bluez/bluez/blob/master/doc/org.bluez.NetworkServer.rst)
process must remain running. `bt-spp` remains a separate serial service.

The LCD agent replaces the unattended default agent on the LCD variant and
returns to the interrupted menu when a request expires or disconnects.
With SC enabled, the controller accepts an approval reply but reports
authentication failure (`0x05`) about 0.07 seconds later. A BR/EDR-only trial
failed in the same way and BLE was restored. Temporarily switching SC off
then produced HCI Simple Pairing Complete status 0, Link Key Notification
and encryption success. BlueZ reported Paired/Bonded/Connected yes; bnep0
joined br-lan. The owner confirmed iPhone Internet access with its Wi-Fi and
cellular data disabled. After the opt-in compatibility control was installed,
the owner also confirmed iPad pairing and Internet access. BlueZ then listed
two paired and bonded devices. Other clients have not been verified.

The current sharing update adds **Network → Bluetooth → Sharing** to the
LCD and makes CLI/web readiness require a powered adapter as well as the NAP
UUID. `bt-nap on` first powers the adapter on and stops on a power command
failure. Pending web edits remain protected. The real device retained the
NAP UUID while Bluetooth was powered off, reproducing the former false-ready
condition. LCD sharing-on restored power and NAP readiness; LuCI RPC sharing
off/on also passed, and the original enabled state was restored. No device
reboot was involved. These controls do not establish successful client traffic.

SC was subsequently restored to on. The stored iPhone bond authenticated
again, encryption was enabled and the owner confirmed Internet access again.
The owner requested an option. **Pairing Compatibility** is now installed at
**Services → Bluetooth** in LuCI, and **Network → Bluetooth → Compat OFF/ON**
on the LCD. It is V18-only, defaults off and saves
`bt-nap.main.pairing_compat`. Enabling it retains numeric comparison, SSP,
BLE and encryption, while disabling the stronger SC protection. Changes
briefly disconnect Bluetooth. BlueZ uses a generated RAM configuration only
when enabled; the persistent main.conf remains unchanged.

BlueZ 5.83 does not disable SC when it is already enabled at daemon startup,
so the helper explicitly sets the controller's SC mode while powered off,
then restarts BlueZ and restores the previous power state. Failure restores
the previous saved setting and controller mode. Pending edits are rejected.
The actual LCD action enabled compatibility in 10.06 seconds; the web RPC
disabled it in 6.90 seconds. HCI checks confirmed SC off/on, NAP was ready
after both changes, and the original main.conf hash stayed unchanged.
The automated check ended with the saved option off and SC on. The owner
then enabled compatibility for the successful iPad test; it remains on by
that selection. A subsequent normal reboot retained the saved option and
controller SC-off mode. No key file was opened and no firmware image was
written for this Bluetooth update. A complete image build was not run for
this overlay update.

#### 2026-11-08 USB 유선랜의 상위 회선·내부망 선택

상위 인터넷 회선은 LTE·Wi-Fi Station·USB 유선랜, 하위 장비 연결은 Wi-Fi AP·
블루투스 NAP·USB 유선랜으로 구성한다. `네트워크 → 인터넷 연결`에서 USB 유선랜을
WAN/LAN으로 선택하고 LTE·Wi-Fi·유선랜 중 우선 회선을 정한다. WAN은 별도 DHCP
인터페이스 `usbwan`과 기존 WAN 방화벽을 사용한다. 실제 기기의 WAN 영역은 입력
REJECT·전달 DROP·NAT 사용으로 확인했다. LAN은 기존 `br-lan`에 연결하며,
WAN의 종료를 확인한 뒤 연결한다. USB 상위 회선과 내부망을 동시에 연결하지 않는다.

`a8-network`는 USB 호스트의 지원 유선랜 드라이버만 찾는다. PC 연결용 USB 네트워크,
모뎀·Wi-Fi·블루투스는 유선랜으로 선택하지 않는다. 어댑터 연결·분리, 내부망 시작,
설정 적용 시 상태를 갱신하며, 적용 실패는 웹 화면에 표시한다. 방화벽·네트워크
재적용 실패는 다음 갱신에서 재시도한다. LCD에도 유선랜 우선순위를 추가하고,
호스트 모드에서는 PC 연결용 USB 기능 메뉴 대신 Ethernet WAN/LAN을 표시한다.

회선 선택은 기존 `uplink-manager`를 확장했다. USB의 DHCP 기본 경로·DNS 자동
적용은 끄고, 관리자가 시험용 경로와 실제 선택한 회선의 경로·DNS를 적용한다.
LTE만 사용하면 USB 시험용 기본 경로도 제거한다. Wi-Fi의 DHCP 갱신이 유선랜의
DNS를 덮어쓰지 않게 했다. 기존 기기의 Wi-Fi 우선 설정과 현재 연결은 유지됐다.

Realtek RTL8150·RTL8152/8153/8156 계열, ASIX AX88772·AX88179 계열,
AQC111, DM9601, SR9700·SR9800, CH9200, SMSC75xx·95xx, LAN78xx,
MCS7830·Pegasus와 CDC/RNDIS 표준 어댑터용 드라이버를 포함한다. 상류 패키지가 없는
CH9200·SR9800은 이 대상의 커널 패키지 정의를 추가했다. V18의 `a8-network`
필수 의존성으로 선택해 다음 이미지에도 포함되도록 했다. Realtek 드라이버 2개는
기존 커널에 내장돼 있었으며, Realtek 펌웨어 패키지와 파일 7개를 실제 설치·확인했다.

현재 기기의 커널 패키지 식별자와 저장소의 커널 식별자가 달라 저장소의 kmod를
설치할 수 없었다. 원래 배포 커밋 `ce14a4f5b23572835dc43a59665f6c716222cd2d`,
OpenWrt `981c978eff`, 검증한 Linux 6.12.112 원본과 기기에서 읽은 커널 설정으로
USB 모듈만 빌드했다. 원래 컴파일러는 GCC 14.3.0, 이번 빌드는 GCC 13.3.0이며
컴파일러 기능 관련 설정 차이 12개는 VM의 `config-delta.txt`에 남겼다.
CH9200·SR9800은 해당 모듈 빌드 옵션만 추가했다. 커널 식별자는 기기와 동일한
`6.12.112 SMP preempt mod_unload aarch64`이며 강제 적재 옵션은 사용하지 않았다.

추가 드라이버 17개와 공통 모듈 2개, 총 19개 파일을 실제 A8에 적재했다. 내장된
Realtek 드라이버 2개를 합쳐 지원 드라이버는 19종이다. 설치된 파일의 SHA-256은
VM의 최종 산출물과 모두 일치한다. 파일은 `/lib/modules/6.12.112/`, 자동 적재
설정은 `/etc/modules.d/55-a8-usb-ethernet`에 있다. VM의 최신 모듈 1벌은
`/home/builder/a8/usb-net-build/modules/`에 보관하며 파일 합계는 585,128바이트이다.
`SHA256SUMS` 파일의 SHA-256은
`482652c20feccd8ededf4bc16085ab3d47ac2981dc106485bd4e3f247899da17`이다.
드라이버와 `usbnet` 18개를 해제한 뒤 자동 적재 파일로 다시 불러와 19개 전체가
준비된 상태를 확인했다. `phylink`는 원래 커널 소스에 종료 함수가 없어 참조가
0개여도 해제할 수 없으며 그대로 유지했다. 재부팅 시험은 미실행이다.

회선 선택·WAN/LAN 분리·실패 재적용 검사 9개, Wi-Fi 조회 검사 4개, USB 검사
17개와 LCD 메뉴 검사 2개가 통과했다. 실제 기기의 DHCP 비활성 경로·DNS 해석,
설정 쓰기·재적용·RPC 조회·서비스 자동 시작 설정도 확인했다. 반영 후 Wi-Fi 상위
회선, LTE 데이터, IMS 등록과 문자·음성 준비 상태가 유지됐다. 기기의 공개 인증서를
사용한 HTTPS 조회에서도 새 화면 파일과 소스가 일치했고, A8의 Lua 5.1에서도
WAN/LAN 분리 검사가 통과했다. 중간 소스 트리·전체 빌드 로그·기기 임시 전송 파일은
정리하고 최종 모듈과 원본 압축 파일·커널 설정·빌드 정보를 보존했다. 브라우저 제어 도구를
사용할 수 없어 실제 웹 화면 표시는 미확인이다. 전체 이미지 빌드·커밋·푸시·원격
빌드는 미실행이다.

V18은 V12의 `dr_mode = "peripheral"`을 계속 사용한다. 현재 커널의 USB 역할
전환 경로는 읽기·쓰기 가능하며, 설정 관리자는 호스트·PC 연결 모드를 전환하도록
구현했다. 이번 확인에서는 PC·ADB 연결 모드를 유지했다. 실제 어댑터가 연결되지
않아 물리적 OTG 호스트 전환, 5 V 공급·동시 충전, 실제 유선랜 WAN/LAN 통신과
어댑터 분리·재연결은 미확인이다.

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
| Wi-Fi, Bluetooth, LTE traffic | Later overlay checks: AP/STA operation and LTE-bound ping observed. An iPhone paired, joined br-lan through bnep0 and passed the owner's Internet test with Wi-Fi/cellular off, including a saved-bond reconnect with SC restored. The owner also confirmed iPad pairing and Internet access with compatibility enabled. Other clients remain unverified. |
| Charging cycles, temperature, cold boots, traffic soak | Not run for the latest bootloader revision |
| LCD source and initialization checks | 32 checks on macOS: 31 passed and the Linux-only evdev check was skipped. Includes the actual boot controller with held/bouncing inputs, PM8916 voltage encoding, complete panel initialization, Tux pixels, failure handling and rescue flow. The installed bootloader's Tux/Boot menu before Linux is owner-confirmed. |
| Display kernel configuration | Linux 6.12.112 `listnewconfig` detects the missing Acer EC setting before the fix; no new prompts after the fix, and `syncconfig` succeeds with stdin closed. Required framebuffer/panel/backlight options and both unrelated-driver exclusions verified. |
| USB ADB source checks | Fourteen tests passed, including delayed descriptors with integer-only sleep, package preparation and ECM/NCM priority. Linux board harness with mutation checks also passed. |
| Travel APN source checks | Six tests passed on macOS, including the actual Lua model, PIN2/PUK2 handling, supported technology choices, LuCI options and staged-settings guard. Database generation, home-operator selection, manual profiles and existing-daemon integration checked. Earlier isolated-rootfs Lua 5.1/libuci checks passed; modem responses and interface restarts in those checks are simulated. |
| SMS detection source and device checks | Six source tests passed: ModemManager/QMI number detection, manual settings, staged edits and status-only operation. On the A8 the saved number matches ModemManager and the country prefix is +82. Fresh incoming SMS and IMS registration remain unverified. |
| Previous LCD/ADB/APN/menu build | Source `12ea725` [built successfully](https://github.com/wwwhana/msm8916-openwrt/actions/runs/42163596055); tests [42163630238](https://github.com/wwwhana/msm8916-openwrt/actions/runs/42163630238) passed. Its `a8-display` is `1.0-r4`; this image was inspected but not installed. |
| Installed LuCI display/button build | Source `0c75e22`: [build 42187058705](https://github.com/wwwhana/msm8916-openwrt/actions/runs/42187058705) and [tests 42187062701](https://github.com/wwwhana/msm8916-openwrt/actions/runs/42187062701) passed. Archive/ZIP checks, metadata CRC and standalone/sysupgrade comparisons passed. `a8-apn` is `1.0-r2`, `a8-display` is `1.0-r5`. Thirteen checks pass in the actual ARM64 image without source overlays. |
| Updated image sizes and GPT | Boot 6,648,092 bytes / 64 MiB; rootfs 29,369,037 bytes / 128 MiB. Primary/backup GPT CRCs pass and all 17 partition geometries match the installed baseline, including `modemst2` at partition 5, LBA 141314–144385. Generated GUIDs differ; installation retained the existing GPT. |
| Updated LCD DTB and kernel configuration | DTB 56,444 bytes / 469 nodes, byte-identical to the standalone LCD compile. V18 identity, panel pins/timing/supplies, MPP4 sink drive 7, disabled SDC2 and Bluetooth description verified. Framebuffer console, 8x16 font, PMIC power-key/evdev, FunctionFS, built-in GPIO backlight and PMIC GPIO/MPP controller verified in the actual kernel configuration. |
| Previous r4 ARM64 userspace | All 22 checks passed using that image's Lua 5.1/libuci and musl programs, without source overlays, network access or physical devices. Covers real APN configuration writes/migration, home-vs-visited operator selection, no-SIM/locked-SIM handling, staged edits (including edits during modem queries), retry, RPC, menu actions/confirmations, legacy button suppression and input-reader disconnect/ticks. Modem responses and `ifup` are simulated; power commands are not executed. |
| One-button menus | Shared timing, boot selection/timeout, Lua navigation and APN actions pass the source checks above. Runtime menus are visible, confirmed by the owner. The actual runtime Fastboot action was executed, Fastboot product checked, and normal OpenWrt restored. Physical boot-menu selection still needs confirmation. |
| LuCI display/button follow-up source (`a8-display` 1.0-r5) | Fifteen source checks covered across macOS and Linux (the Linux evdev case runs separately from the macOS Lua/Node cases); Linux board harness: 57 passed, 0 failed, 3 skipped. Thirteen isolated OpenWrt Lua 5.1/libuci checks pass with the new source overlaid on the previous rootfs: real staged/committed settings, original power handler, RPC and V18/V12 init routing. Power commands and devices are simulated. This is not an inspection of a newly built image or a physical LuCI/browser test. |
| Menu-enabled bootloader | Repaired 346,184-byte primary aboot installed after successful RAM normal-boot testing and owner approval. Whole 1 MiB read-back and a fresh-kernel hash check passed; recovery is unchanged. Owner confirmed Tux and Boot menu before Linux on a plain reboot; OpenWrt returned. Installed Fastboot USB/LCD initialization, recovery USB console, and return to OpenWrt passed. The explicitly approved rescue-counter correction is also installed: intentional Fastboot/Recovery boots leave the counter at zero and plain Recovery reboot returns directly to OpenWrt. L6/L17 remain read-only. |
| Standalone USB adbd build | AOSP daemon and bundled libraries compiled and linked on ARM64 Alpine/musl; missing FunctionFS exits with an error. Ubuntu 22.04 host Go 1.18 generates the same BoringSSL error table. This is not an OpenWrt firmware build or device connection test. |
| `0c75e22` EDL update | Passed: only aboot/boot/rootfs written. Each full partition read-back verified payload, sector padding and untouched tail. All seven radio hashes, including `modemst2`, and both GPT copies stayed unchanged; reset succeeded. |
| Updated OpenWrt on this A8 | Root USB ACM/ADB observed: Linux 6.12.112, V18 identity, 128×128 RGB565 fb0 and connected power input. The corrected DTB and UART-only/8×16 console image were installed and the full boot partition read back. GPIO11 is high after reboot, text and runtime menus are visible, confirmed by the owner. |
| USB ADB and NCM on this A8 | The `usleep` fix was written to the production overlay through Recovery and its hash verified. After a normal reboot, ADB enumerated and root shell, file push and exact framebuffer read-back passed. Attaching NCM `usb1` to LAN restored the Mac's DHCP lease and a USB-bound management HTTP response. The source also fixes ECM/NCM priority; 14 USB checks pass. Unplug/replug and cellular traffic remain untested. |
| LCD, Wi-Fi and Bluetooth overlay checks (2026-11-07) | Display: 31 checks passed, Linux-only evdev check skipped on macOS. Wi-Fi: ten checks passed. Five Bluetooth CLI checks and LuCI backend/view checks passed. Incoming numeric confirmation, stored iPhone bond, encrypted reconnect and owner Internet tests passed. Optional LCD-on/web-RPC-off checks verified controller SC mode and restored default off. Complete image build not run. |
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
