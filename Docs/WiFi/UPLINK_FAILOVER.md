# Dual-uplink failover (modem + Wi-Fi station)

The HMU05 / HMUF02 boards and the HM-MiFiB-V18 pocket router have two paths to the internet:

- LTE modem -- wwan0, driven by ModemManager (network.modem). netifd owns its
  layer-3 and installs the default route. The manager reads a single netifd
  status response: the default has `target="0.0.0.0"` and `mask=0`, rather than
  a combined `target="0.0.0.0/0"`.
- Wi-Fi station -- phy0-sta0, brought up late by wifi-sta-late / wifi-sta-up
  (Station Mode). netifd is deliberately kept away from this vif: a netifd-managed
  station iface makes netifd restart the radio, which crashes the WCNSS firmware.

uplink-manager picks one of these as the active default route with health probes and a
user policy. It never touches netifd's modem configuration and never restarts the radio
-- it only adds/removes an override route and points dnsmasq at the active link's DNS.

## Route ladder

Lower metric wins. The manager maintains three metrics:

    Metric  Owner              Meaning
    40      uplink-manager     STA override -- installed only when the STA is active
    50      netifd + manager   modem default route (manager normalises it to 50)
    100     wifi-sta-dhcp.sh   STA candidate -- fallback, modem wins while present

Normalising the modem route adds metric 50 first, then removes older metrics
only for that same gateway and device. If adding 50 fails, the old route stays.
This avoids leaving the original metric-10 route ahead of the STA override.

Result:
- Modem healthy, STA down -> modem (50) is the only route.
- Modem healthy, STA up -> modem (50) still wins; STA is a standby at 100.
- Modem unhealthy, STA healthy -> STA override (40) wins.
- Both unhealthy -> active=none and the STA override is removed. A netifd modem
  route or STA candidate may still exist; this state means no healthy link was
  selected, not that the kernel route table is empty.

If uplink-manager dies entirely, the netifd modem route (50) and the STA candidate (100)
still give modem-first service once any stale override is gone. A hard kill may
leave metric 40 behind until the manager restarts; the route table is not a
substitute for the health status.

## Policy

Set in "uci set uplink.general.policy=..." (or Network -> Station Mode -> Uplink in
LuCI):

- auto (default) -- modem wins while it is healthy; the station takes over only when
  the modem has no route or fails its health probe.
- modem -- modem only; the station is never selected even if the modem is down.
- sta -- station preferred; falls back to the modem if the station is unhealthy.

The LCD **Connections** menu exposes the same choices as **LTE first**, **LTE
only**, and **Wi-Fi first**. Its **Relay** switch persists `wifista.main.enabled`;
switching it off removes the station without disabling the local AP. Turning
relay back on reuses the saved networks. LuCI saves the same switch.

## Health probes

Each link is probed with "ping -I <device> <probe_target>" every probe_interval
seconds (default 30s, target 1.1.1.1). A link is marked down after fail_threshold
consecutive probe failures (default 3) and up again after recover_threshold consecutive
successes (default 2). Changing the config and saving the LuCI page (or "kill -USR1" on
the manager) re-evaluates immediately.

## DNS

When the station is the active link, dnsmasq is pointed at uplink.general.sta_dns
(default 8.8.8.8 8.8.4.4, preferred over the AP's DNS). When the modem is active the
explicit server list is cleared so dnsmasq uses the modem's upstream resolver. The
station DHCP script also writes sta_dns on a lease, so the preference survives even
before uplink-manager runs.

## Files

msm89xx-scripts:
- /usr/sbin/uplink-manager -- the manager loop.
- /etc/init.d/uplink-manager -- procd service (START=97, respawn).
- /etc/config/uplink -- policy and probe tuning.
- /usr/sbin/wifi-sta-dhcp.sh -- on lease installs address + route (metric 100) + DNS
  and records /tmp/wifi-sta-lease; on deconfig tears them down and signals the manager.
- /usr/sbin/wifi-sta-up -- apply_firewall() binds phy0-sta0 to the wan zone (firewall
  layer only; netifd never sees the vif).
- /etc/uci-defaults/95-wifi-sta-late -- enables the manager service on first boot.

luci-app-wifi-sta:
- htdocs/luci-static/resources/view/wifi-sta/uplink.js -- Network -> Station Mode ->
  Uplink page: live status (active link, per-link state/device/gateway/DNS) plus the
  policy form. Saving restarts uplink-manager.
- menu.d / acl.d -- the Uplink menu entry and the rpcd ACL granting uplink uci access,
  /tmp/uplink.json read, and /etc/init.d/uplink-manager exec.

## Live status

/tmp/uplink.json is rewritten every probe cycle:

    {
      "active": "sta",
      "policy": "auto",
      "modem": { "present": 0, "healthy": 0, "device": "", "gateway": "" },
      "sta":   { "present": 1, "healthy": 1, "device": "phy0-sta0", "gateway": "10.0.0.1", "dns": "8.8.8.8 8.8.4.4" }
    }

## What happens when the station goes down

wifi-sta-late stop removes only the station, its lease and station DHCP process,
then wakes the manager immediately. A DHCP deconfig / lost association also
removes the station address.
wifi-sta-dhcp.sh deletes the route + DNS + lease and signals the manager. The manager's
read_sta only trusts the lease while phy0-sta0 still carries an address, so a lingering
lease after an unclean udhcpc exit can never be mistaken for a live link. With the
station gone and the modem healthy, the modem route (50) becomes the sole path and
traffic flows automatically. If the modem is also down, active=none and there is no
internet. No radio restart, no firmware crash.

## HM-MiFiB-V18 checks, 2026-11-07

The updated scripts were installed in the running overlay. Actual relay
off/on and LTE-first/Wi-Fi-first/LTE-first changes were read back from UCI and
the route table. The AP stayed up and LTE-bound ping passed during those
checks. Six host tests cover netifd route parsing, policy selection, stale-route
removal and a failed replacement preserving the old route. Later observations
showed a working station uplink while the modem was registered without an
active data interface; these are separate observations, not proof of stable
cellular operation across every reconnection.
