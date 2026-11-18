# JZ08C V50 (sold as MF800-E)

This profile targets the PCB marked `JZ08C_V50_20250930`, not every product
sold under the MF800-E name. The MSM8916 identity and 7,634,944-sector eMMC
were checked through EDL. RAM size still needs a running-system check.

## Stock hardware mapping

The stock boot image contains MTP and 512 MB MTP DTBs. Their panel, key,
SIM and charging mappings are identical. The port uses its own DTS rather
than inheriting another board's status LED or battery settings.

| Part | Stock configuration used by this port |
| --- | --- |
| SoC | Qualcomm MSM8916 |
| eMMC | 3,909,091,328 bytes, 512-byte sectors |
| LCD | Preferred ST7735S WX144 profile, 128×128 RGB565 |
| LCD bus | QUP4 at `0x78b8000`, SPI GPIO12/13/15, CS GPIO14, 16 MHz |
| LCD control | Reset GPIO118, D/C GPIO116 |
| LCD rails | L17 2.85 V, L6 1.8 V |
| LCD address window | Columns 2–129, rows 3–130 |
| Backlight | PM8916 MPP4 current sink, stock 40 mA |
| Exposed side button | PM8916 PON `KEY_POWER`, observed through ADB |
| Other stock key lines | GPIO37 / F2 and GPIO107 / F3; GPIO key device disabled by the port |
| SIM controls | SIM1 GPIO22, SIM2 GPIO23, SIM3 GPIO1; hot-detect pulse GPIO20 |
| Default SIM | SIM3, matching the stock GPIO default |
| Charger | PM8916 linear charger, stock 4.2 V / 1.44 A limits |
| Battery gauge | Stock temperature-dependent OCV curves; fitted pack capacity unknown |
| SDHC2 | Disabled in stock; enabled by the port for an optional microSD socket |

The stock panel's `bl_ctrl_wled` setting sends the `bkl-trigger` LED event.
The MPP4 `lcd-bl` node consumes that trigger; this does not imply a separate
WLED controller. See the [Qualcomm SPI panel implementation](https://android.googlesource.com/kernel/msm/+/06e5ace9eb713a478215eaeea4bfb0c79ed15c71%5E2..06e5ace9eb713a478215eaeea4bfb0c79ed15c71/).

The unused NV3023A panel profile is also present in stock. The port follows
the preferred ST7735S phandle. It reuses the 128×128 menu frontend with a
separate stock initialization blob and a board-specific compatible gate.
Only the side button is exposed on this enclosure. The WPS button is not
populated and the front power button is hidden. Stock ADB input capture
confirmed short presses and a 2.3-second hold as `KEY_POWER`. The menu uses
that single input: a short press advances, and a one-second hold selects.
No Up/Down or WPS button is required. GPIO key navigation is disabled, and
clicks while the LCD is unavailable do not invoke legacy double-click reboot.
In that state, a four-second hold keeps the existing shutdown behavior
(reboot when USB power is present).
The LCD menu and single-power-key navigation have been verified under OpenWrt,
including selecting the USB reconnect action from the exposed side button.

The optional SDHC2 socket uses the SoC's dedicated four-bit CLK/CMD/DAT0–3
signals and the existing L11/L12 supplies. Card insertion is polled without
assuming a card-detect or write-protect GPIO. The initial limit is 25 MHz
with 3 V signalling; 1.8 V switching is disabled. The image includes the exFAT
driver. A 64 GB SDXC card and its first partition have been detected as
`mmcblk1` and `mmcblk1p1` under OpenWrt. A 16 MiB sequential read and a read-only
exFAT mount with directory access have passed using four-bit, 25 MHz signalling.
Long-duration I/O and card writes have not been tested.

## Build and validation

```sh
./build.sh build jz08c-v50
```

The image uses the common recovery layout and preserves device-specific
modem/calibration partitions through the migration installer's backup and
restore steps. The bootloader recovery-key GPIO remains unset until a
physical recovery key has been verified. USB NCM, ACM and ADB provide
bring-up access.

Build the matching recovery image after the production build with
`./build-recovery.sh jz08c-v50`. Its LCD uses the same board-specific panel
firmware. Set `RECOVERY_BLOBS_DIR` to a private directory containing this
device's Wi-Fi firmware and the matching OpenWrt `hostapd`, `iw` and libraries;
otherwise the builder uses `msm89xx/recovery/blobs/jz08c-v50/` as usual.
Device-specific calibration and private recovery images must stay out of Git.

The source configuration and panel conversion checks run before installation.
Production boot with Linux 6.12.112, USB ADB, the 128×128 LCD menu and side-button
navigation, battery gauge readings, and an ext4 overlay write/read check have
passed on hardware.
The Wi-Fi and modem firmware have booted. Wireless connectivity, cellular
data, Bluetooth, charge limiting and recovery boot
still need verification. Stock DT nodes describe the intended wiring; they
do not establish that each feature has passed a hardware test.
