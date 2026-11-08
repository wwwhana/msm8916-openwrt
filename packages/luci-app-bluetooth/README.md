# luci-app-bluetooth

This tree vendors [sbwml/luci-app-bluetooth](https://github.com/sbwml/luci-app-bluetooth)
at `75c09ba33a6544304cb3119e7d18ad5c239cbad0` under its GPL-3.0 license.
This adaptation uses the installed BlueZ `bluetoothctl` agent for headless
pairing, bounds command duration, validates RPC inputs, preserves quoted
device names and reports command failures. The UI starts from the actual
adapter state and refreshes in place. Tcl/Expect and audio packages are not
required; audio controls remain available when BlueALSA is installed.
Devices requiring typed passkeys can be paired through interactive
`bluetoothctl`.

**Allow Pairing (3 min)** makes the adapter visible to phones and PCs.
On the LCD model, incoming confirmation requests show the device and passkey;
Reject is selected initially, and approval requires a button selection. Requests
expire after 45 seconds or close when the peer disconnects. **Approval sent**
means that the confirmation reply was delivered to the agent, not that bonding
completed. A successfully answered request is not shown again while its
file is being cleared; failed replies can be retried. The original unattended
agent is stopped while the LCD agent runs. **Share Internet** registers the NAP profile on `br-lan`;
paired clients use the existing LAN DHCP, DNS and firewall forwarding. The
setting persists in `/etc/config/bt-nap`. On HM-MiFiB-V18, an iPhone paired,
created `bnep0` on `br-lan` and passed the owner's Internet test with Wi-Fi and
cellular data disabled. The owner also confirmed iPad pairing and Internet
access with compatibility enabled. Other clients remain unverified.

**Pairing Compatibility** is an optional V18 setting, off by default. Enable it
if a device rejects pairing after both sides confirm the code. It retains
numeric comparison and encryption but disables Secure Connections, using the
older SSP protection. Changing it briefly disconnects Bluetooth. The LCD
provides **Network → Bluetooth → Compat OFF/ON**; the saved option is
`bt-nap.main.pairing_compat`. The original `main.conf` stays unchanged: BlueZ
uses a generated RAM configuration while compatibility is enabled. An iPhone
paired with compatibility enabled, then reconnected and passed Internet
access again after Secure Connections was restored. Live LCD-on and web-RPC-off
checks verified the controller setting and restored the default off state.
The command-line tools are `bluetoothctl`, `bt-nap on|off|status`,
`bt-compat on|off|status`, and `bt-network --server nap br-lan`.

A LuCI application for managing Bluetooth devices on OpenWrt.

## Features

This application provides a web interface within LuCI to manage your device's Bluetooth adapter and connected devices.

- **Adapter Control**:
  - Turn the Bluetooth adapter on or off.
  - View and change the adapter's alias.

- **Device Management**:
  - Scan for nearby Bluetooth devices.
  - View a list of available and paired devices, including their MAC address, name, and status (Paired, Bonded, Trusted, Connected).
  - Pair with new devices.
  - Connect to and disconnect from paired devices.
  - Remove (unpair) existing devices.

- **Audio Management (Requires `bluez-alsa`)**:
  - View connected audio devices.
  - Set the audio codec (e.g., SBC, AAC) for a connected device.
  - Adjust the volume for a connected device.

## Dependencies

The package depends on BlueZ, `bluez-network`, rpcd/ucode, jshn and timeout.
`bluez-network` builds upstream `bt-network` from the same pinned bluez-tools
revision used by OpenWrt (`f65321736475429316f07ee94ec0deac8e46ec4a`).
Expect is not required. BlueALSA is optional and only supplies audio controls.
The LCD confirmation agent is part of the display package.

## Technical Overview

-   **Frontend**: The user interface is a single-page application built with JavaScript, located under `htdocs/luci-static/resources/view/bluetooth/`.
-   **Backend**: The backend logic is implemented in `ucode` and is located at `root/usr/share/rpcd/ucode/bluetooth.uc`. It exposes RPC methods that the frontend consumes.
-   **Core Logic**: The backend primarily acts as a wrapper around the standard `bluetoothctl` command-line tool to perform Bluetooth operations. Helper scripts in `root/usr/libexec/bluetooth/` are used for more complex tasks like device enumeration and pairing.
