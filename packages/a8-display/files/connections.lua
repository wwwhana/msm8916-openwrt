-- SPDX-License-Identifier: GPL-2.0-only
local M = {}
local json = require('luci.jsonc')
local CACHE='/var/run/a8-connections.json'
local function cursor()
    return require('uci').cursor(nil, '/var/run/a8-menu-uci')
end
local function query(command)
    local p=io.popen('timeout 4 ' .. command .. ' 2>/dev/null', 'r')
    if not p then return '' end
    local text=p:read('*a'); p:close(); return text or ''
end
local function object(command)
    local ok, value=pcall(json.parse, query(command))
    return ok and type(value) == 'table' and value or {}
end
local function execute(command, seconds)
    local rc=os.execute('timeout ' .. (seconds or 12) .. ' ' .. command .. ' >/dev/null 2>&1')
    return rc == 0 or rc == true
end
local function installed(path)
    local rc=os.execute('test -x ' .. path)
    return rc == 0 or rc == true
end
local function editable(configs)
    -- Check the web UI's savedir before writing through our isolated cursor.
    local c=require('uci').cursor()
    for _, config in ipairs(configs) do
        if next(c:changes(config) or {}) then c:close(); return false end
    end
    c:close(); return true
end
local function valid_name(name)
    return type(name) == 'string' and #name <= 64 and name:match('^[%w_%-]+$') ~= nil
end
local function enabled(c, config, section)
    return c:get(config, section, 'enabled') == '1'
end
local function cached(path)
    local f=io.open(path or CACHE,'r')
    local ok,status=pcall(json.parse,f and f:read('*a') or '')
    if f then f:close() end
    status=ok and type(status) == 'table' and status or {}
    return status
end
local function save_cache(status)
    local f=io.open(CACHE .. '.new','w')
    if not f then return false end
    f:write(json.stringify(status)); f:close()
    return os.rename(CACHE .. '.new',CACHE)
end
local function bluetooth_status(status)
    local controller=query('bluetoothctl --timeout 3 show')
    status.bluetooth=controller:match('Powered:%s*(%a+)')
    status.nap=status.bluetooth == 'yes' and controller:find('00001116-0000-1000-8000-00805f9b34fb',1,true) ~= nil
    local compatibility=query('/usr/sbin/bt-compat status'):match('^([01])%s*$')
    status.pairing_compat=compatibility and compatibility == '1'
    return status.bluetooth
end
local function access_points(c)
    local aps={}
    c:foreach('wireless','wifi-iface',function(s)
        if s.mode == 'ap' and valid_name(s['.name']) and valid_name(s.device) and
           c:get('wireless',s.device) == 'wifi-device' then aps[#aps+1]=s end
    end)
    return aps
end
local tailscale_labels={Running='ON',Stopped='STOP',NeedsLogin='LOGIN',
    NeedsMachineAuth='APPROVAL',Starting='START',NoState='WAIT'}
function M.entries()
    local status=cached()
    local c=cursor()
    local relay=enabled(c, 'wifista', 'main')
    local policy=c:get('uplink', 'general', 'policy')
    local aps=access_points(c)
    local wifi=false
    for _,s in ipairs(aps) do
        if s.disabled ~= '1' and c:get('wireless',s.device,'disabled') ~= '1' then wifi=true end
    end
    local result={network={
        {label=#aps == 0 and 'Wi-Fi --' or 'Wi-Fi ' .. (wifi and 'ON' or 'OFF'),
            id='wifi',value=wifi and '0' or '1',info=#aps == 0},
        {label='Station ' .. (relay and 'ON' or 'OFF'),id='relay',value=relay and '0' or '1'},
        {label='VPN',id='vpn'},{label='USB',id='usb'},
        {label='APN profiles',id='apn'},
        {label=policy == 'ethernet' and 'Ethernet first' or policy == 'sta' and 'Wi-Fi first' or policy == 'modem' and 'LTE only' or 'LTE first',id='priority'}
    },vpn={},usb={},bt={},sms={}}
    if installed('/usr/sbin/tailscale') then
        local on=enabled(c, 'tailscale', 'settings')
        result.vpn[#result.vpn+1]={label='Tailscale ' .. (on and 'ON' or 'OFF'),id='tailscale',value=on and '0' or '1'}
        result.vpn[#result.vpn+1]={label='TS status',id='tailscale-status'}
    end
    if installed('/usr/bin/bluetoothctl') then
        local power=status.bluetooth
        result.network[#result.network+1]={label='Bluetooth',id='bt'}
        result.bt[#result.bt+1]={label='Allow pairing',id='pairable',value='1'}
        result.bt[#result.bt+1]={label='BT ' .. (power == 'yes' and 'ON' or power == 'no' and 'OFF' or '--'),
                          id='bluetooth', value=power == 'yes' and '0' or '1'}
        if installed('/usr/sbin/bt-nap') and c:get('bt-nap','main') == 'sharing' then
            local sharing=enabled(c,'bt-nap','main')
            result.bt[#result.bt+1]={label='Sharing ' .. (not sharing and 'OFF' or status.nap and 'ON' or 'WAIT'),
                                    id='bt-nap',value=sharing and '0' or '1'}
        end
        if installed('/usr/sbin/bt-compat') and status.pairing_compat ~= nil then
            result.bt[#result.bt+1]={label='Compat ' .. (status.pairing_compat and 'ON' or 'OFF'),
                                    id='bt-compat',value=status.pairing_compat and '0' or '1'}
        end
    end
    c:foreach('network', 'interface', function(s)
        local name=s['.name']
        if s.proto == 'wireguard' and valid_name(name) then
            local up=(status.wireguard or {})[name] == true
            result.vpn[#result.vpn+1]={label='WG ' .. name:sub(1,6) .. (up and ' UP' or ' DOWN'),
                              id='wireguard', profile=name, value=up and '0' or '1'}
        end
    end)
    if #result.vpn == 0 then result.vpn[1]={label='No VPN set up',info=true} end
    local usb_role=c:get('uplink','general','usb_ethernet')
    if usb_role=='wan' or usb_role=='lan' then
        result.usb[1]={label='Ethernet ' .. usb_role:upper(),info=true}
    elseif installed('/etc/init.d/usb-gadget') and c:get('usbgadget','usb') == 'device' then
        local master=enabled(c,'usbgadget','usb')
        for _,function_name in ipairs({'adb','network','acm'}) do
            local present,on=false,false
            local sections=function_name == 'network' and {'ncm','ecm','rndis'} or {function_name}
            for _,section in ipairs(sections) do
                if c:get('usbgadget',section) == 'function' then
                    present=true; on=on or enabled(c,'usbgadget',section)
                end
            end
            if present then
                on=master and on
                result.usb[#result.usb+1]={label=({adb='ADB',network='USB net',acm='Serial'})[function_name] ..
                    (on and ' ON' or ' OFF'),id='usb-function',profile=function_name,value=on and '0' or '1'}
            end
        end
        result.usb[#result.usb+1]={label='Reconnect USB',id='usb-restart'}
    else result.usb[1]={label='USB unavailable',info=true} end
    local sms=cached('/var/run/a8-sms.json')
    if sms.available == true then
        result.sms={{label='New: ' .. tostring(sms.new or 0),info=true},
            {label='Stored: ' .. tostring(sms.total or 0),info=true}}
    else result.sms={{label='SMS unavailable',info=true}} end
    c:close(); return result
end
function M.update_cache()
    local status={wireguard={},wifi=false}
    local clients,clients_known={},true
    for _,radio in pairs(object('ubus call network.wireless status')) do
        if type(radio) == 'table' and radio.up == true then
            for _,iface in ipairs(radio.interfaces or {}) do
                if (iface.config or {}).mode == 'ap' then
                    status.wifi=true
                    local peers=valid_name(iface.ifname) and object('ubus call hostapd.' .. iface.ifname .. ' get_clients').clients
                    if type(peers)=='table' then
                        for address,peer in pairs(peers) do
                            if type(peer)=='table' and peer.authorized==true then clients[address]=true end
                        end
                    else clients_known=false end
                end
            end
        end
    end
    if clients_known then
        status.wifi_clients=0
        for _ in pairs(clients) do status.wifi_clients=status.wifi_clients+1 end
    end
    local link=cached('/tmp/uplink.json')
    local dev=type(link.modem)=='table' and link.modem.device
    if type(dev)=='string' and dev:match('^wwan%d+$') then
        local traffic={}
        for _,direction in ipairs({'rx','tx'}) do
            local f=io.open('/sys/class/net/' .. dev .. '/statistics/' .. direction .. '_bytes','r')
            local text=f and f:read('*a') or ''
            if f then f:close() end
            local count=text:match('^%s*(%d+)%s*$') and tonumber(text)
            if count and count<=9007199254740991 then traffic[direction]=count end
        end
        -- Kernel interface totals reset when the data interface is recreated.
        if traffic.rx and traffic.tx then status.traffic=traffic end
    end
    if installed('/usr/sbin/tailscale') then status.tailscale=object('tailscale status --json').BackendState end
    if installed('/usr/bin/bluetoothctl') then
        bluetooth_status(status)
    end
    local c=cursor()
    c:foreach('network','interface',function(s)
        if s.proto == 'wireguard' and valid_name(s['.name']) then
            status.wireguard[s['.name']]=object('ubus call network.interface.' .. s['.name'] .. ' status').up == true
        end
    end)
    c:close()
    return save_cache(status)
end
function M.run(action)
    if action.id == 'tailscale-status' then
        return true,'TS ' .. (tailscale_labels[cached().tailscale or ''] or '--')
    end
    if action.id == 'wifi' then
        if action.value ~= '0' and action.value ~= '1' then return false,'Invalid choice' end
        if not editable({'wireless'}) then return false,'Apply web edits' end
        local c=cursor()
        local aps=access_points(c)
        if #aps == 0 then c:close(); return false,'Not configured' end
        for _,s in ipairs(aps) do
            if not c:set('wireless',s['.name'],'disabled',action.value == '1' and '0' or '1') or
               action.value == '1' and not c:set('wireless',s.device,'disabled','0') then
                c:close(); return false,'Save failed'
            end
        end
        local saved=c:commit('wireless'); c:close()
        if not saved then return false,'Save failed' end
        local ok=execute('/sbin/wifi reload')
        return ok,ok and ('Wi-Fi ' .. (action.value == '1' and 'ON' or 'OFF')) or 'Saved; retry'
    end
    if action.id == 'usb-function' or action.id == 'usb-restart' then
        if action.id == 'usb-function' and
           ((action.profile ~= 'adb' and action.profile ~= 'network' and action.profile ~= 'acm') or
            (action.value ~= '0' and action.value ~= '1')) then return false,'Invalid choice' end
        if not editable({'usbgadget','network','firewall','dhcp'}) then return false,'Apply web edits' end
        if not installed('/etc/init.d/usb-gadget') then return false,'USB unavailable' end
        local c=cursor()
        if c:get('usbgadget','usb') ~= 'device' then c:close(); return false,'Not configured' end
        if action.id == 'usb-function' then
            local sections=action.profile == 'network' and {'ncm','ecm','rndis'} or {action.profile}
            local available,on={},false
            for _,section in ipairs(sections) do
                if c:get('usbgadget',section) == 'function' then
                    available[#available+1]=section; on=on or enabled(c,'usbgadget',section)
                end
            end
            if #available == 0 then c:close(); return false,'Not configured' end
            for i,section in ipairs(available) do
                local value=action.value == '1' and ((on and enabled(c,'usbgadget',section)) or (not on and i == 1)) and '1' or '0'
                if not c:set('usbgadget',section,'enabled',value) then c:close(); return false,'Save failed' end
            end
            local any=false
            for _,section in ipairs({'adb','acm','ncm','ecm','rndis','ums'}) do any=any or enabled(c,'usbgadget',section) end
            if action.value == '1' or not any then
                if not c:set('usbgadget','usb','enabled',action.value) then c:close(); return false,'Save failed' end
            end
            if not c:commit('usbgadget') then c:close(); return false,'Save failed' end
        end
        c:close()
        local ok=execute('/etc/init.d/usb-gadget restart',30)
        return ok,ok and 'USB restarted' or 'Saved; retry'
    end
    if action.id == 'pairable' then
        local ok=execute('bluetoothctl --timeout 3 discoverable-timeout 180') and
            execute('bluetoothctl --timeout 3 pairable on') and execute('bluetoothctl --timeout 3 discoverable on')
        return ok,ok and 'Pairing: 3 min' or 'BT failed'
    end
    if action.id == 'bt-nap' or action.id == 'bt-compat' then
        if action.value ~= '0' and action.value ~= '1' then return false,'Invalid choice' end
        if not editable({'bt-nap'}) then return false,'Apply web edits' end
        local ok=execute('/usr/sbin/' .. action.id .. ' ' .. (action.value == '1' and 'on' or 'off'),60)
        local status=cached(); bluetooth_status(status); save_cache(status)
        return ok,ok and ((action.id == 'bt-nap' and 'Sharing ' or 'Compat ') ..
            (action.value == '1' and 'ON' or 'OFF')) or 'BT change failed'
    end
    if action.id == 'bluetooth' then
        if action.value ~= '0' and action.value ~= '1' then return false, 'Invalid choice' end
        local want=action.value == '1' and 'yes' or 'no'
        local ok=execute('bluetoothctl --timeout 6 power ' .. (want == 'yes' and 'on' or 'off'))
        local status=cached(); local power=bluetooth_status(status); save_cache(status)
        return ok and power == want, power == want and ('BT ' .. (want == 'yes' and 'ON' or 'OFF')) or 'BT failed'
    end
    local config, section, option, command, message
    if action.id == 'relay' then
        config='wifista'; section='main'; option='enabled'
        command='/etc/init.d/wifi-sta-late reload'; message='Relay '
    elseif action.id == 'policy' then
        if action.value ~= 'sta' and action.value ~= 'auto' and action.value ~= 'modem' and action.value ~= 'ethernet' then return false, 'Invalid choice' end
        config='uplink'; section='general'; option='policy'
        command='/etc/init.d/uplink-manager reload'
        message=({sta='Wi-Fi first', auto='LTE first', modem='LTE only',ethernet='Ethernet first'})[action.value]
    elseif action.id == 'tailscale' then
        config='tailscale'; section='settings'; option='enabled'
        command='/etc/init.d/tailscale ' .. (action.value == '1' and 'start' or 'stop'); message='TS '
    elseif action.id == 'wireguard' and valid_name(action.profile) then
        config='network'; section=action.profile; option='auto'
        command=(action.value == '1' and '/sbin/ifup ' or '/sbin/ifdown ') .. section; message='WG '
    else return false, 'Unknown action' end
    if action.id ~= 'policy' and action.value ~= '0' and action.value ~= '1' then return false, 'Invalid choice' end
    local configs=action.id == 'tailscale' and {'tailscale','network','firewall','dhcp'} or {config}
    if not editable(configs) then return false, 'Apply web edits' end
    local c=cursor()
    if not c:get(config, section) or action.id == 'wireguard' and c:get(config, section, 'proto') ~= 'wireguard' then
        c:close(); return false, 'Not configured'
    end
    if not c:set(config, section, option, action.value) or not c:commit(config) then
        c:close(); return false, 'Save failed'
    end
    c:close()
    if not execute(command) then return false, 'Saved; retry' end
    if action.id ~= 'policy' then message=message .. (action.value == '1' and 'ON' or 'OFF') end
    if action.id == 'tailscale' and action.value == '1' then
        local state=object('tailscale status --json').BackendState
        if state == 'NeedsLogin' then return true, 'Login via LuCI' end
        if state == 'NeedsMachineAuth' then return true, 'Admin approval' end
    end
    return true, message
end
return M
