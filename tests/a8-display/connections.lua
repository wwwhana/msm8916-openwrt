local files=arg[1]
local values={wifista={main={['.type']='sta',enabled='1'}},
    wireless={radio0={['.type']='wifi-device',disabled='0'},
        ap={['.type']='wifi-iface',device='radio0',mode='ap',disabled='0'},
        sta={['.type']='wifi-iface',device='radio0',mode='sta',disabled='0'}},
    usbgadget={usb={['.type']='device',enabled='1'},
        adb={['.type']='function',enabled='1'},acm={['.type']='function',enabled='1'},
        ncm={['.type']='function',enabled='1'},ecm={['.type']='function',enabled='0'},
        rndis={['.type']='function',enabled='0'}},
    uplink={general={['.type']='uplink',policy='auto'}},
    tailscale={settings={['.type']='tailscale',enabled='1'}},
    ['bt-nap']={main={['.type']='sharing',enabled='1'}},
    network={wgtrip={['.type']='interface',proto='wireguard',auto='1'},
             lan={['.type']='interface',proto='static'},
             ['bad;command']={['.type']='interface',proto='wireguard'}}}
local pending,commands,commits={}, {}, {}
local bt,ts,wg='yes','NeedsLogin',true
local nap=true
local compatibility=false
local ap_running=false
local ap_ifname
local uplink_dev='wwan0'
local rx_text='12000000\n'
local sms={available=true,total=3,new=1}
local saved_cache
local failed=false
package.preload.uci=function() return {cursor=function(_, savedir)
    if savedir then assert(savedir == '/var/run/a8-menu-uci') end
    return {
        changes=function(_, config) return pending[config] and {staged=true} or {} end,
        get=function(_, config, section, option)
            local value=(values[config] or {})[section]
            return value and value[option or '.type']
        end,
        set=function(_, config, section, option, value) values[config][section][option]=value; return true end,
        commit=function(_, config) commits[#commits+1]=config; return true end,
        foreach=function(_, config, kind, fn)
            for id,value in pairs(values[config] or {}) do
                local copy={['.name']=id}; for k,v in pairs(value) do copy[k]=v end
                if value['.type'] == kind then fn(copy) end
            end
        end,
        close=function() end
    }
end} end
package.preload['luci.jsonc']=function() return {parse=function(text)
    if text == 'CACHEJSON' then return {tailscale=ts,bluetooth=bt,nap=bt == 'yes' and nap,pairing_compat=compatibility,wireguard={wgtrip=wg}} end
    if text == 'TSJSON' then return {BackendState=ts} end
    if text == 'WGJSON' then return {up=wg} end
    if text == 'WIFIJSON' then return {radio0={up=true,interfaces={
        {ifname=ap_ifname,config={mode=ap_running and 'ap' or 'sta'}}}}} end
    if text == 'PEERJSON' then return {clients={one={authorized=true},two={authorized=false},three={authorized=true}}} end
    if text == 'UPLINKJSON' then return {modem={device=uplink_dev}} end
    if text == 'SMSJSON' then return sms end
    error('bad json')
end,stringify=function(status) saved_cache=status; return 'CACHEJSON' end} end
local popen,execute,open,rename=io.popen,os.execute,io.open,os.rename
io.open=function(path,mode)
    if path == '/tmp/uplink.json' then return {read=function() return 'UPLINKJSON' end,close=function() end} end
    if path:match('^/sys/class/net/wwan0/statistics/[rt]x_bytes$') then
        return {read=function() return path:find('/rx_bytes',1,true) and rx_text or '3000000\n' end,close=function() end}
    end
    assert(path:match('^/var/run/a8%-connections%.json') or path == '/var/run/a8-sms.json')
    return {read=function() return path == '/var/run/a8-sms.json' and 'SMSJSON' or 'CACHEJSON' end,
        write=function() return true end,close=function() end}
end
os.rename=function() return true end
local queries=0
io.popen=function(command)
    queries=queries+1
    local value=command:find('tailscale status',1,true) and 'TSJSON' or
        command:find('network.wireless status',1,true) and 'WIFIJSON' or
        command:find('hostapd.phy0-ap0 get_clients',1,true) and 'PEERJSON' or
        command:find('network.interface.wgtrip',1,true) and 'WGJSON' or
        command:find('/usr/sbin/bt-compat status',1,true) and (compatibility and '1\n' or '0\n') or
        'Powered: ' .. bt .. (nap and '\nUUID: 00001116-0000-1000-8000-00805f9b34fb' or '')
    return {read=function() return value end,close=function() return true end}
end
os.execute=function(command)
    commands[#commands+1]=command
    if failed then return 1 end
    if command:find('power off',1,true) then bt='no' end
    if command:find('power on',1,true) then bt='yes' end
    if command:find('/usr/sbin/bt-nap on',1,true) then nap=true;bt='yes';values['bt-nap'].main.enabled='1' end
    if command:find('/usr/sbin/bt-nap off',1,true) then nap=false;values['bt-nap'].main.enabled='0' end
    if command:find('/usr/sbin/bt-compat on',1,true) then compatibility=true end
    if command:find('/usr/sbin/bt-compat off',1,true) then compatibility=false end
    return 0
end
local service=dofile(files .. '/connections.lua')
local entries=service.entries()
assert(#entries.network == 7 and entries.network[1].label == 'Wi-Fi ON')
assert(entries.network[2].label == 'Station ON' and entries.network[2].value == '0')
assert(entries.network[3].id == 'vpn' and entries.network[4].id == 'usb')
assert(entries.vpn[1].label == 'Tailscale ON' and entries.vpn[2].id == 'tailscale-status')
assert(entries.vpn[3].id == 'wireguard' and entries.vpn[3].profile == 'wgtrip')
assert(#entries.vpn[3].label <= 14)
assert(entries.bt[1].id == 'pairable' and entries.bt[2].label == 'BT ON')
assert(entries.bt[3].id == 'bt-nap' and entries.bt[3].label == 'Sharing ON' and entries.bt[3].value == '0')
assert(entries.bt[4].id == 'bt-compat' and entries.bt[4].label == 'Compat OFF' and entries.bt[4].value == '1')
assert(entries.usb[1].label == 'ADB ON' and entries.usb[2].profile == 'network')
assert(entries.sms[1].label == 'New: 1' and entries.sms[2].label == 'Stored: 3')
sms={available=false}; assert(service.entries().sms[1].label == 'SMS unavailable')
assert(queries == 0) -- Opening the menu must not wait for Bluetooth/Tailscale.
assert(service.update_cache() and queries == 5 and saved_cache.wifi == false)
ap_running=true; assert(service.update_cache() and saved_cache.wifi == true)
ap_ifname='phy0-ap0'; assert(service.update_cache() and saved_cache.wifi_clients==2)
assert(saved_cache.traffic.rx==12000000 and saved_cache.traffic.tx==3000000)
ap_ifname='bad;command'; assert(service.update_cache() and saved_cache.wifi_clients==nil)
uplink_dev='wwan0/../../private'; assert(service.update_cache() and saved_cache.traffic==nil)
uplink_dev='wwan0'; rx_text='-1'; assert(service.update_cache() and saved_cache.traffic==nil)
rx_text='12000000\n'; ap_ifname=nil
assert(service.run({id='wifi',value='0'}))
assert(values.wireless.ap.disabled == '1' and values.wireless.sta.disabled == '0')
assert(values.wireless.radio0.disabled == '0' and commands[#commands]:find('/sbin/wifi reload',1,true))
assert(service.entries().network[1].label == 'Wi-Fi OFF')
values.wireless.radio0.disabled='1'
assert(service.run({id='wifi',value='1'}))
assert(values.wireless.ap.disabled == '0' and values.wireless.radio0.disabled == '0')
assert(values.wireless.sta.disabled == '0' and values.wifista.main.enabled == '1')
pending.wireless=true; assert(not service.run({id='wifi',value='0'})); pending.wireless=nil
assert(service.run({id='relay',value='0'}))
assert(values.wifista.main.enabled == '0' and commands[#commands]:find('wifi%-sta%-late reload'))
assert(service.run({id='policy',value='sta'}))
assert(values.uplink.general.policy == 'sta' and service.entries().network[6].label == 'Wi-Fi first')
assert(service.run({id='policy',value='ethernet'}))
assert(values.uplink.general.policy=='ethernet' and service.entries().network[6].label=='Ethernet first')
values.uplink.general.usb_ethernet='wan'
assert(#service.entries().usb==1 and service.entries().usb[1].label=='Ethernet WAN' and service.entries().usb[1].info)
values.uplink.general.usb_ethernet='lan'
assert(service.entries().usb[1].label=='Ethernet LAN')
values.uplink.general.usb_ethernet='off'
assert(service.run({id='bluetooth',value='0'})); assert(bt == 'no')
assert(saved_cache.nap == false and service.entries().bt[3].label == 'Sharing WAIT')
failed=true; assert(not service.run({id='bluetooth',value='1'})); assert(bt == 'no'); failed=false
assert(service.run({id='bluetooth',value='1'})); assert(bt == 'yes')
assert(service.run({id='bt-nap',value='0'})); assert(not nap and service.entries().bt[3].label == 'Sharing OFF')
pending['bt-nap']=true; assert(not service.run({id='bt-nap',value='1'})); assert(not nap);pending['bt-nap']=nil
failed=true; assert(not service.run({id='bt-nap',value='1'}));assert(not nap);failed=false
assert(service.run({id='bt-nap',value='1'})); assert(nap and saved_cache.nap == true)
assert(service.run({id='bt-compat',value='1'})); assert(compatibility and saved_cache.pairing_compat)
assert(service.entries().bt[4].label == 'Compat ON')
pending['bt-nap']=true; assert(not service.run({id='bt-compat',value='0'})); pending['bt-nap']=nil
failed=true; assert(not service.run({id='bt-compat',value='0'})); assert(compatibility); failed=false
assert(service.run({id='bt-compat',value='0'})); assert(not compatibility and saved_cache.pairing_compat == false)
assert(service.run({id='tailscale',value='0'})); assert(values.tailscale.settings.enabled == '0')
local ok,message=service.run({id='tailscale',value='1'})
assert(ok and message == 'Login via LuCI')
local status_ok,status=service.run({id='tailscale-status'}); assert(status_ok and status == 'TS LOGIN')
assert(service.run({id='wireguard',profile='wgtrip',value='0'})); assert(values.network.wgtrip.auto == '0')
for _,config in ipairs({'network','firewall','dhcp','tailscale'}) do
    pending[config]=true
    assert(not service.run({id='tailscale',value='0'}))
    pending[config]=nil
end
pending.wifista=true; assert(not service.run({id='relay',value='1'})); pending.wifista=nil
assert(service.run({id='usb-function',profile='network',value='0'}))
assert(values.usbgadget.ncm.enabled == '0' and values.usbgadget.ecm.enabled == '0' and values.usbgadget.rndis.enabled == '0')
assert(values.usbgadget.adb.enabled == '1' and values.usbgadget.acm.enabled == '1')
assert(service.run({id='usb-function',profile='adb',value='0'}))
assert(values.usbgadget.usb.enabled == '1' and values.usbgadget.acm.enabled == '1')
assert(service.run({id='usb-function',profile='acm',value='0'}))
assert(values.usbgadget.usb.enabled == '0')
assert(service.run({id='usb-function',profile='adb',value='1'}))
assert(values.usbgadget.usb.enabled == '1' and values.usbgadget.adb.enabled == '1')
assert(values.usbgadget.acm.enabled == '0')
assert(service.run({id='usb-function',profile='network',value='1'}))
assert(values.usbgadget.ncm.enabled == '1' and values.usbgadget.ecm.enabled == '0')
values.usbgadget.usb.enabled='0'; values.usbgadget.ncm.enabled='0'; values.usbgadget.ecm.enabled='1'
assert(service.run({id='usb-function',profile='network',value='1'}))
assert(values.usbgadget.usb.enabled == '1' and values.usbgadget.ecm.enabled == '1' and values.usbgadget.ncm.enabled == '0')
local restart_commits=#commits
assert(service.run({id='usb-restart'})); assert(#commits == restart_commits)
assert(commands[#commands]:find('timeout 30 /etc/init.d/usb-gadget restart',1,true))
for _,config in ipairs({'usbgadget','network','firewall','dhcp'}) do
    pending[config]=true
    assert(not service.run({id='usb-function',profile='adb',value='0'}))
    assert(not service.run({id='usb-restart'}))
    pending[config]=nil
end
local before=#commits
for _,action in ipairs({{id='policy',value='shell'}, {id='relay',value='2'},
    {id='tailscale',value=';reboot'}, {id='bluetooth',value=';reboot'}, {id='bt-nap',value=';reboot'}, {id='bt-compat',value=';reboot'},
    {id='wifi',value=';reboot'}, {id='usb-function',profile='adb',value=';reboot'},
    {id='usb-function',profile='adb;reboot',value='1'},
    {id='wireguard',profile='lan',value='0'}, {id='wireguard',profile='bad;command',value='0'}}) do
    assert(not service.run(action))
end
assert(#commits == before and values.network.lan.proto == 'static')
io.popen,os.execute,io.open,os.rename=popen,execute,open,rename
print('Network/VPN/USB/SMS menus, separate AP/Station switches and staged-edit protection passed')
