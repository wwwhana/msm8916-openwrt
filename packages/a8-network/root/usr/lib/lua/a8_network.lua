-- SPDX-License-Identifier: GPL-2.0-only
local fs=require('nixio.fs')
local json=require('luci.jsonc')
local uci=require('uci')
local M={}
local NETWORK_PENDING='/var/run/a8-network-reload'
local FIREWALL_PENDING='/var/run/a8-network-firewall-reload'
local drivers={r8152=true,rtl8150=true,asix=true,ax88179_178a=true,aqc111=true,
    cdc_ether=true,cdc_ncm=true,cdc_eem=true,cdc_subset=true,dm9601=true,
    smsc95xx=true,smsc75xx=true,mcs7830=true,pegasus=true,sr9700=true,sr9800=true,ch9200=true,
    lan78xx=true,rndis_host=true}
local function name(s)
    return type(s)=='string' and #s>0 and #s<=15 and s:match('^[%w_.%-]+$') and s~='.' and s~='..'
end
local function base(s) return type(s)=='string' and s:match('([^/]+)$') or '' end
local function run(s)
    local r=os.execute('timeout 20 ' .. s .. ' >/dev/null 2>&1')
    return r==0 or r==true
end
local function bus(path,method,args)
    local b=require('ubus').connect()
    if not b then return nil end
    local result=b:call(path,method,args or {}); b:close(); return result
end
local function port()
    for d in fs.dir('/sys/class/usb_role') or function() end do
        if name(d) or d:match('^[%w_.%-]+$') then
            local p='/sys/class/usb_role/' .. d .. '/role'
            local role=(fs.readfile(p) or ''):match('^([%a]+)')
            if role then return p,role end
        end
    end
end
function M.adapters()
    local result={}
    for dev in fs.dir('/sys/class/net') or function() end do
        if name(dev) then
            local p='/sys/class/net/' .. dev .. '/device'
            local driver=base(fs.readlink(p .. '/driver'))
            if drivers[driver] and base(fs.readlink(p .. '/subsystem'))=='usb' and
               (fs.readfile('/sys/class/net/' .. dev .. '/type') or ''):match('^1%s*$') then
                result[#result+1]={device=dev,driver=driver,
                    carrier=(fs.readfile('/sys/class/net/' .. dev .. '/carrier') or ''):match('^1')~=nil}
            end
        end
    end
    table.sort(result,function(a,b) return a.device<b.device end)
    return result
end
function M.status()
    local c=uci.cursor()
    local role=c:get('uplink','general','usb_ethernet') or 'off'
    local device=c:get('uplink','general','usb_device') or ''
    c:close()
    local _,mode=port()
    local ok,link=pcall(json.parse,fs.readfile('/tmp/uplink.json') or '')
    local runtime_ok,runtime=pcall(json.parse,fs.readfile('/var/run/a8-network.json') or '')
    return {usb={role=role,device=device,port_mode=mode or 'unavailable',adapters=M.adapters(),
        error=runtime_ok and type(runtime)=='table' and runtime.error or nil},
        uplink=ok and type(link)=='table' and link or {}}
end
function M.refresh()
    if not fs.mkdirr('/var/run/a8-network-uci') then return false,'state-directory-failed' end
    local c=uci.cursor(nil,'/var/run/a8-network-uci')
    local role=c:get('uplink','general','usb_ethernet') or 'off'
    local selected=c:get('uplink','general','usb_device') or ''
    local gadget=c:get('usbgadget','usb','gadget_name') or 'g1'
    local gadget_enabled=c:get('usbgadget','usb','enabled')~='0'
    if role~='off' and role~='wan' and role~='lan' then c:close(); return false,'invalid-role' end
    if selected~='' and not name(selected) then c:close(); return false,'invalid-device' end
    local normal=uci.cursor()
    for _,config in ipairs({'network','firewall'}) do
        if next(normal:changes(config) or {}) then normal:close(); c:close(); return false,'pending-changes' end
    end
    normal:close()
    local owned=c:get('network','usbwan','a8_usb_ethernet')
    if c:get('network','usbwan') and owned~='1' then c:close(); return false,'usbwan-already-configured' end
    local zone
    c:foreach('firewall','zone',function(s) if s.name=='wan' then zone=s['.name'] end end)
    if role=='wan' and not zone then c:close(); return false,'wan-zone-missing' end
    local path,mode=port()
    local want=role=='off' and 'device' or 'host'
    if role~='off' and not path then c:close(); return false,'usb-host-unavailable' end
    local adapters=M.adapters()
    local chosen
    for _,a in ipairs(adapters) do
        if selected=='' or selected==a.device then chosen=chosen or a.device end
    end
    -- Stop WAN before moving its device into LAN; never bridge the upstream.
    if role~='wan' and c:get('network','usbwan') then
        local status=bus('network.interface.usbwan','status')
        if not status then c:close(); return false,'usbwan-stop-failed' end
        if status.up or status.pending then
            if not bus('network.interface.usbwan','down') then c:close(); return false,'usbwan-stop-failed' end
            for i=1,20 do
                status=bus('network.interface.usbwan','status')
                if status and not status.up and not status.pending then break end
                require('nixio').nanosleep(0,50000000)
            end
            if not status or status.up or status.pending then c:close(); return false,'usbwan-still-active' end
        end
    end
    for _,a in ipairs(adapters) do
        if role~='lan' or selected~='' and selected~=a.device then
            local master=base(fs.readlink('/sys/class/net/' .. a.device .. '/master'))
            if master=='br-lan' and not bus('network.interface.lan','remove_device',{name=a.device}) then
                c:close(); return false,'lan-detach-failed'
            end
        end
    end
    local changed=false
    local function set(config,section,option,value)
        if c:get(config,section,option)~=value then c:set(config,section,option,value); changed=true end
    end
    if role=='wan' or owned=='1' then
        if not c:get('network','usbwan') then c:set('network','usbwan','interface'); changed=true end
        set('network','usbwan','a8_usb_ethernet','1')
        set('network','usbwan','proto','dhcp')
        set('network','usbwan','device',role=='wan' and (chosen or 'a8-usbwan0') or 'a8-usbwan0')
        set('network','usbwan','auto',role=='wan' and '1' or '0')
        -- Only uplink-manager installs the chosen USB default route and DNS.
        set('network','usbwan','defaultroute','0')
        set('network','usbwan','peerdns','0')
        set('network','usbwan','metric','150')
    end
    local fw_changed=false
    if role=='wan' then
        local nets=c:get('firewall',zone,'network') or {}
        if type(nets)=='string' then
            local text=nets; nets={}; for n in text:gmatch('%S+') do nets[#nets+1]=n end
        end
        local found=false
        for _,n in ipairs(nets) do if n=='usbwan' then found=true end end
        if not found then nets[#nets+1]='usbwan'; c:set('firewall',zone,'network',nets); fw_changed=true end
    end
    if fw_changed then
        if not fs.writefile(FIREWALL_PENDING,'1') then c:close(); return false,'state-directory-failed' end
        if not c:commit('firewall') then c:revert('firewall'); c:close(); return false,'firewall-save-failed' end
    end
    if fs.readfile(FIREWALL_PENDING) then
        if not run('/etc/init.d/firewall reload') then c:close(); return false,'firewall-reload-failed' end
        fs.unlink(FIREWALL_PENDING)
    end
    if changed then
        if not fs.writefile(NETWORK_PENDING,'1') then c:close(); return false,'state-directory-failed' end
        if not c:commit('network') then c:revert('network'); c:close(); return false,'network-save-failed' end
    end
    c:close()
    if fs.readfile(NETWORK_PENDING) then
        if not bus('network','reload') then return false,'network-reload-failed' end
        fs.unlink(NETWORK_PENDING)
    end
    -- Configure the isolated WAN first. Hotplug queues the newly appearing adapter.
    if path and mode~=want then
        if want=='host' and not run('/etc/init.d/usb-gadget stop') then return false,'usb-stop-failed' end
        if not fs.writefile(path,want .. '\n') or (fs.readfile(path) or ''):match('^(%a+)')~=want then
            if want=='host' then fs.writefile(path,'device\n'); run('/etc/init.d/usb-gadget start') end
            return false,'usb-role-failed'
        end
    end
    local udc=gadget:match('^[%w_.%-]+$') and fs.readfile('/sys/kernel/config/usb_gadget/' .. gadget .. '/UDC')
    if want=='device' and path and (mode~=want or gadget_enabled and not (udc or ''):match('%S')) and
       not run('/etc/init.d/usb-gadget start') then return false,'usb-start-failed' end
    if role=='lan' then
        for _,a in ipairs(adapters) do
            if (selected=='' or selected==a.device) and
               not bus('network.interface.lan','add_device',{name=a.device}) then return false,'lan-attach-failed' end
        end
    end
    run('/etc/init.d/uplink-manager reload')
    return true
end
return M
