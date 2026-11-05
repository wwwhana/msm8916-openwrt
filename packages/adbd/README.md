# USB adbd for A8

This builds the AOSP 34.0.5 daemon from nmeum/android-tools' release source
(SHA-256 in `Makefile`). It keeps BoringSSL and fmt from that archive and
bundles Brotli 1.1.0 with a separate checked download; LZ4/Zstd use OpenWrt
libraries. It does not install the host `adb` client.

The Linux CMake source list, small compatibility headers, omitted
Android-service stubs and Linux/FunctionFS patch are adapted from
[`dikeckaan/mu300-linux`](https://github.com/dikeckaan/mu300-linux/tree/833616a6c03a7c0245300c5946d98ec98b09bb94/tools/adbd)
at commit `833616a6c03a7c0245300c5946d98ec98b09bb94`, under the MIT license
included as `LICENSE.mu300`. AOSP retains its Apache-2.0 notices; bundled
library notices are installed under `/usr/share/licenses/adbd`.

`uci-usb-gadget` owns the single composite gadget. It mounts FunctionFS,
starts this procd service, waits for both data endpoints, adds `ffs.adb`,
and then binds the UDC. NCM and ACM remain in the same configuration.
The reconnect watcher uses the gadget manager's lock and restores a gadget
unbound by FunctionFS. If descriptors are missing, it binds the remaining
functions and adds ADB when the daemon is ready again.

Only the A8 V18 image selects this package and enables it. Its one-time
defaults script also handles an upgrade with an older preserved USB config,
and leaves an existing ADB choice alone. A8 defaults to `auth=open`:
**a connected USB computer gets a root shell without a confirmation dialog**.
The daemon has no TCP/vsock listener and ignores Android's TCP properties.
`adb tcpip` cannot enable a network listener.

For public-key access, put allowed host **public** keys in
`/etc/adb/adb_keys`, then set `usbgadget.adb.auth=keys` and restart
`usb-gadget`. A missing/unknown auth value also selects keys; there is no
Android framework to approve a new host. Keep private keys on the host.
Run `uci set usbgadget.adb.enabled=0; uci commit usbgadget` and restart
`usb-gadget` to disable ADB. Apply these changes over Wi-Fi/SSH or the
serial console, since a gadget restart temporarily disconnects USB.
