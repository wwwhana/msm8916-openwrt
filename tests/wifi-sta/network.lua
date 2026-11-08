-- Execute the real USB LAN/WAN backend against isolated UCI, sysfs and ubus.
local module=arg[1]
local values,files,links,events,fail,wan,devices,pending
local role_path='/sys/class/usb_role/ci_hdrc.0-role-switch/role'
local function copy(v)
    if type(v)~='table' then return v end
    local r={}; for k,x in pairs(v) do r[k]=copy(x) end; return r
end
local function record(event) events[#events+1]=event end
local function reset(role)
    values={uplink={general={['.type']='uplink',usb_ethernet=role or 'off'}},
        usbgadget={usb={gadget_name='A8',enabled='1'}},network={},
        firewall={internet={['.type']='zone',name='wan',network={'modem','stawan'}}}}
    files={[role_path]='device\n',['/sys/kernel/config/usb_gadget/A8/UDC']='ci_hdrc.0\n'}
    links={}; events={}; fail={}; wan={up=false,pending=false}; devices={}; pending={}
end
local function device(name,driver,master,subsystem)
    devices[#devices+1]=name
    local path='/sys/class/net/' .. name
    links[path .. '/device/driver']='/drivers/' .. driver
    links[path .. '/device/subsystem']='/bus/' .. (subsystem or 'usb')
    files[path .. '/type']='1\n';files[path .. '/carrier']='1\n'
    if master then links[path .. '/master']='/sys/class/net/' .. master end
end
package.preload['nixio.fs']=function() return {
    dir=function(path)
        local names=path=='/sys/class/net' and devices or {'ci_hdrc.0-role-switch'}
        local i=0;return function() i=i+1;return names[i] end
    end,
    readfile=function(path) return files[path] end,
    readlink=function(path) return links[path] end,
    writefile=function(path,value)
        record('write:' .. path)
        if path==role_path and fail.role and value=='host\n' then return nil end
        files[path]=value;return true
    end,
    mkdirr=function() return not fail.mkdir end,
    unlink=function(path) files[path]=nil;return true end
} end
package.preload.nixio=function() return {nanosleep=function() record('wait') end} end
package.preload['luci.jsonc']=function() return {parse=function() return {} end} end
package.preload.uci=function() return {cursor=function(_,savedir)
    assert(not savedir or savedir=='/var/run/a8-network-uci')
    local staged=copy(values)
    return {
        get=function(_,config,section,option)
            local s=(staged[config] or {})[section];return s and s[option or '.type']
        end,
        set=function(_,config,section,option,value)
            staged[config]=staged[config] or {}
            if value==nil then staged[config][section]={['.type']=option}
            else staged[config][section][option]=copy(value) end
            return true
        end,
        changes=function(_,config) return pending[config] and {saved=true} or {} end,
        foreach=function(_,config,kind,fn)
            for id,s in pairs(staged[config] or {}) do
                if s['.type']==kind then local row=copy(s);row['.name']=id;fn(row) end
            end
        end,
        commit=function(_,config)
            record('commit:' .. config)
            if fail['commit:' .. config] then return false end
            values[config]=copy(staged[config]);return true
        end,
        revert=function() end,close=function() end
    }
end} end
package.preload.ubus=function() return {connect=function() return {
    close=function() end,
    call=function(_,path,method,args)
        local event=path .. ':' .. method;record(event)
        if fail[event] then return nil end
        if event=='network.interface.usbwan:status' then return copy(wan) end
        if event=='network.interface.usbwan:down' and not fail.stuck then wan={up=false,pending=false} end
        if path=='network.interface.lan' then
            links['/sys/class/net/' .. args.name .. '/master']=method=='add_device' and '/sys/class/net/br-lan' or nil
        end
        return {}
    end
} end} end
local execute=os.execute
os.execute=function(command) record(command);return fail[command] and 1 or 0 end
local function command(s) return 'timeout 20 ' .. s .. ' >/dev/null 2>&1' end
local function index(event)
    for i,s in ipairs(events) do if s==event then return i end end
end
local function expect_error(service,reason)
    local ok,error=service.refresh();assert(not ok and error==reason,tostring(error))
end
reset()
local service=dofile(module)
assert(service.refresh())
assert(not index('commit:network') and not index(command('/etc/init.d/usb-gadget start')))
device('usb0','g_ether');device('wwan0','qmi_wwan');device('wlan0','r8152',nil,'platform')
device('eth1','asix');device('eth0','r8152');device('eth2','sr9700')
device('eth3','ch9200');device('eth4','sr9800')
local found=service.adapters();assert(#found==5 and found[1].device=='eth0' and found[5].driver=='sr9800')

reset('wan');device('eth0','r8152','br-lan');device('eth1','asix','br-lan')
files[role_path]='host\n'
assert(service.refresh())
local cfg=values.network.usbwan
assert(cfg.proto=='dhcp' and cfg.auto=='1' and cfg.device=='eth0')
assert(cfg.defaultroute=='0' and cfg.peerdns=='0' and cfg.metric=='150')
assert(values.firewall.internet.network[1]=='modem' and values.firewall.internet.network[3]=='usbwan')
assert(not links['/sys/class/net/eth0/master'] and not links['/sys/class/net/eth1/master'])
assert(index('network.interface.lan:remove_device')<index('commit:network'))
assert(index(command('/etc/init.d/firewall reload'))<index('commit:network'))
events={};assert(service.refresh())
assert(not index('commit:network') and not index('commit:firewall') and not index('network:reload'))
values.uplink.general.usb_ethernet='lan';wan={up=true,pending=true};events={}
assert(service.refresh())
assert(values.network.usbwan.auto=='0' and values.network.usbwan.device=='a8-usbwan0')
assert(index('network.interface.usbwan:down')<index('network.interface.lan:add_device'))
assert(links['/sys/class/net/eth0/master'] and links['/sys/class/net/eth1/master'])
values.uplink.general.usb_device='eth1';assert(service.refresh())
assert(not links['/sys/class/net/eth0/master'] and links['/sys/class/net/eth1/master'])
values.uplink.general.usb_ethernet='wan';assert(service.refresh())
assert(values.network.usbwan.device=='eth1' and not links['/sys/class/net/eth1/master'])
values.uplink.general.usb_ethernet='lan';wan={up=true};fail.stuck=true;events={}
expect_error(service,'usbwan-still-active');assert(not index('network.interface.lan:add_device'))
fail.stuck=nil;fail['network.interface.usbwan:down']=true
expect_error(service,'usbwan-stop-failed');assert(not index('network.interface.lan:add_device'))

for _,case in ipairs({{'usb_ethernet','bad','invalid-role'}, {'usb_device','eth0;reboot','invalid-device'}}) do
    reset('wan');values.uplink.general[case[1]]=case[2];expect_error(service,case[3]);assert(#events==0)
end
reset('wan');pending.network=true;expect_error(service,'pending-changes');assert(#events==0)
reset('wan');values.network.usbwan={['.type']='interface'}
expect_error(service,'usbwan-already-configured');assert(#events==0)
reset('wan');values.firewall={};expect_error(service,'wan-zone-missing');assert(#events==0)
reset('wan');files[role_path]=nil;expect_error(service,'usb-host-unavailable');assert(#events==0)

reset('wan');fail[command('/etc/init.d/firewall reload')]=true
expect_error(service,'firewall-reload-failed')
assert(not values.network.usbwan and files[role_path]=='device\n')
fail={};events={};assert(service.refresh())
assert(index(command('/etc/init.d/firewall reload'))<index('commit:network'))
assert(values.network.usbwan.device=='a8-usbwan0' and files[role_path]=='host\n')
reset('wan');fail['network:reload']=true
expect_error(service,'network-reload-failed');assert(files[role_path]=='device\n')
fail={};events={};assert(service.refresh());assert(index('network:reload'))
reset('wan');fail.role=true;expect_error(service,'usb-role-failed')
assert(files[role_path]=='device\n' and index(command('/etc/init.d/usb-gadget start')))
reset('wan');fail['commit:network']=true;expect_error(service,'network-save-failed')
assert(not values.network.usbwan and files[role_path]=='device\n')
os.execute=execute
print('USB adapter detection, WAN isolation, LAN transitions and failed-apply retries passed')
