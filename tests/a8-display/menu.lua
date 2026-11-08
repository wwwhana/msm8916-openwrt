local files=arg[1]
package.loaded['a8_display.settings']=dofile(files..'/settings.lua')
local model=dofile(files .. '/model.lua')
local view=dofile(files .. '/view.lua')
package.loaded['a8_display.dashboard']=dofile(files..'/dashboard.lua')
local state=model.new()
local menus={network={
    {label='Wi-Fi ON',id='wifi',value='0'}, {label='Station ON',id='relay',value='0'},
    {label='VPN',id='vpn'}, {label='USB',id='usb'}, {label='APN profiles',id='apn'},
    {label='LTE first',id='priority'}, {label='Bluetooth',id='bt'}},
    vpn={{label='Tailscale ON',id='tailscale',value='0'}, {label='TS status',id='tailscale-status'}},
    usb={{label='ADB ON',id='usb-function',profile='adb',value='0'}, {label='Reconnect USB',id='usb-restart'}},
    bt={{label='Allow pairing',id='pairable',value='1'}},
    sms={{label='New: 2',info=true},{label='Stored: 3',info=true}}}
local function event(e) return model.event(state,e,{{id='travel',label='Travel'}},menus) end
local function frame()
    local f=view.frame(state, {'Status'})
    assert(f:sub(1,3) == '\27[H')
    local n=0
    for row in (f:sub(4) .. '\r\n'):gmatch('(.-)\r\n') do
        n=n+1; assert(#row == 16); assert(not row:find('[^ -~]'))
    end
    assert(n == 8)
    return f
end
local function choose(id)
    for _=1,#state.entries do
        if state.entries[state.selection].id == id then return end
        event(state.mode == 'side' and 'down' or 'short')
    end
    error('missing entry: ' .. id)
end
local function select()
    local action=event(state.mode == 'side' and 'short' or 'long')
    if action and action.id=='sms-list' then model.inbox(state,menus.sms); return end
    return action
end
event('connected'); assert(state.connected); frame()
event('short'); assert(state.page == 'menu'); frame()
assert(#state.entries == 5)
for i,id in ipairs({'status','network','sms','power','display'}) do assert(state.entries[i].id == id) end
assert(view.frame(state, {'', 'SMS NEW 2'}):find('SMS NEW 2',1,true))
choose('network'); select(); choose('apn'); select(); assert(state.page == 'profiles'); frame()
assert(state.entries[1].id == 'auto' and state.entries[2].profile == 'travel')
select(); assert(state.page == 'confirm'); frame()
assert(select() == nil and state.page == 'profiles')
choose('profile'); select(); assert(state.pending.profile == 'travel')
assert(view.frame(state, {'', 'SMS NEW 2'}):find('Travel',1,true))
assert(not view.frame(state, {'', 'SMS NEW 2'}):find('SMS NEW 2',1,true))
choose('yes'); local action=select()
assert(action.id == 'profile' and action.profile == 'travel' and state.page == 'profiles')
choose('back'); select(); assert(state.page == 'network' and state.entries[state.selection].id == 'apn')
choose('back'); select(); assert(state.page == 'menu' and state.entries[state.selection].id == 'network')
choose('power'); select(); assert(state.page == 'power')
for i,id in ipairs({'poweroff','reboot','recovery','bootloader','edl','back'}) do assert(state.entries[i].id==id) end
for _,id in ipairs({'reboot','recovery','bootloader','edl','poweroff'}) do
    choose(id); assert(select() == nil and state.page == 'confirm'); frame()
    choose('yes'); assert(select().id == id and state.page == 'power')
end
choose('back'); select(); choose('status'); select(); event('short')
for _=1,#state.entries do event('short') end
assert(state.selection == 1)
model.result(state,false,string.rep('\27\n\255',30)); frame()
event('disconnected'); assert(not state.connected and state.page == 'status')
event('short'); assert(state.page == 'status')
event('connected'); event('short'); choose('power'); select(); choose('recovery'); select(); choose('yes')
event('disconnected'); assert(state.page == 'status' and state.pending == nil and #state.parents == 0)
event('connected'); assert(select() == nil and state.page == 'menu')

-- Both button layouts preserve each menu's parent and selection, including interrupts.
for _,mode in ipairs({'mode-side','mode-power'}) do
    event(mode); event('short'); choose('network'); select(); choose('apn'); select(); choose('profile'); select()
    assert(state.page == 'confirm'); assert(select() == nil and state.page == 'profiles')
    choose('back'); select(); assert(state.page == 'network')
    choose('vpn'); select(); assert(state.page == 'vpn'); frame()
    local toggle=select(); assert(toggle.id == 'tailscale' and toggle.value == '0')
    model.result(state,true,'Please wait'); model.result(state,true,'TS OFF')
    menus.vpn[1]={label='Tailscale OFF',id='tailscale',value='1'}
    event('short'); assert(state.page == 'vpn' and state.entries[1].value == '1')
    choose('tailscale-status'); assert(select().id == 'tailscale-status')
    model.result(state,true,'TS LOGIN'); event('short'); assert(state.page == 'vpn')
    choose('back'); select(); assert(state.page == 'network' and state.entries[state.selection].id == 'vpn')
    choose('usb'); select(); assert(state.page == 'usb'); frame()
    local usb=select(); assert(usb.id == 'usb-function' and usb.profile == 'adb')
    model.result(state,true,'USB restarted'); event('short'); assert(state.page == 'usb')
    choose('back'); select(); assert(state.page == 'network' and state.entries[state.selection].id == 'usb')
    choose('priority'); select(); assert(state.page == 'priority'); frame()
    local request={id='request-' .. state.mode,device='Phone',address='00:11:22:33:44:55',code='000042'}
    model.pairing(state,request); assert(state.page == 'pairing' and state.selection == 1)
    assert(frame():find('Code: 000042',1,true))
    model.pairing(state,request); assert(#state.parents == 3)
    event(state.mode == 'side' and 'down' or 'short')
    local approved=select(); assert(approved.id == 'pairing' and approved.value == '1')
    assert(state.page == 'priority' and not state.pairing)
    model.result(state,true,'Approval sent',approved); model.pairing(state,request)
    assert(state.page == 'message' and #state.parents == 2)
    event('short'); assert(state.page == 'priority')
    local next_request={id=request.id .. '-new',device=request.device,address=request.address,code='123456'}
    model.pairing(state,next_request); assert(state.page == 'pairing' and state.selection == 1)
    model.pairing(state,nil); assert(state.page == 'priority' and #state.parents == 2)
    local policy=select(); assert(policy.id == 'policy' and policy.value == 'sta')
    model.result(state,true,'Saved'); event('short'); assert(state.page == 'priority')
    choose('back'); select(); assert(state.page == 'network' and state.entries[state.selection].id == 'priority')
    choose('back'); select(); assert(state.page == 'menu' and state.entries[state.selection].id == 'network')
    choose('sms'); select(); assert(state.page == 'sms' and frame():find('New: 2',1,true))
    assert(select() == nil and state.page == 'sms')
    choose('back'); select(); assert(state.page == 'menu' and state.entries[state.selection].id == 'sms')
    choose('status'); select(); assert(state.page == 'status')
    menus.vpn[1]={label='Tailscale ON',id='tailscale',value='0'}
end
do
    local failed=model.new(); model.event(failed,'connected')
    local r={id='failed-reply',device='Phone',code='123456'}
    model.pairing(failed,r)
    local action=model.event(failed,'long')
    model.result(failed,false,'Response failed',action); model.pairing(failed,r)
    assert(failed.page == 'pairing' and not failed.answered_pairing)
end
assert(state.mode == 'power')
event('short'); assert(view.frame(state):find('Short: next',1,true))
event('mode-side'); event('short'); assert(view.frame(state):find('Side: up/down',1,true))

-- Idle screens preserve menu selection; the entire first hold only wakes.
do
    local s=model.new()
    model.event(s,'connected'); model.event(s,'short'); model.event(s,'short')
    local page, selection=s.page,s.selection
    for _=1,29 do model.tick(s); assert(not s.saver) end
    model.tick(s); assert(s.saver and s.page == page and s.selection == selection)
    for _=31,299 do model.tick(s); assert(not s.sleeping) end
    for _=300,310 do model.tick(s); assert(s.sleeping and s.saver) end
    local status={[4]='LTE  LG U+ ||||-',battery={capacity=75,charging=true}}
    local first=view.frame(s,status,90)
    assert(first:find('BATT 075%  ||||-',1,true))
    assert(view.frame(s,status,91):find('BATT 075%  |||||',1,true))
    for _,now in ipairs({90,91,120,150}) do
        local n=0
        for row in (view.frame(s,status,now):sub(4)..'\r\n'):gmatch('(.-)\r\n') do
            n=n+1; assert(#row == 16 and not row:find('[^ -~]'))
            if n == 8 then assert(row:find('BATT 075%',1,true))
            elseif n == 1 then assert(row == 'LG U+      ||||-')
            else assert(row == string.rep(' ',16)) end
        end
        assert(n == 8)
        local image=view.clock(status,now)
        assert(image:sub(1,15)=='P6\n128 128\n255\n' and #image==49167)
        local xmin,xmax,ymin,ymax=128,0,128,0
        for y=43,70 do for x=0,127 do
            local offset=16+(y*128+x)*3
            if image:sub(offset,offset+2)==string.char(28,34,44) then
                xmin=math.min(xmin,x);xmax=math.max(xmax,x)
                ymin=math.min(ymin,y);ymax=math.max(ymax,y)
            end
        end end
        assert(xmin>=6 and xmax<122 and xmax-xmin+1>=103 and ymin==43 and ymax==70)
    end
    assert(view.clock(status,90)~=view.clock(status,91))
    assert(view.frame(s,status,90,true):find(os.date('%H:%M:%S',90),1,true))
    assert(view.frame(s,{[4]='SIM NONE'},90):find('SIM NONE',1,true))
    for _,e in ipairs({'pressed','long','short','released:5000'}) do
        assert(model.activity(s,e)); model.tick(s); assert(not s.saver and not s.sleeping)
        assert(s.page == page and s.selection == selection)
    end
    assert(not s.waking)
    assert(not model.activity(s,'pressed'))
    assert(not model.activity(s,'long'))
    model.event(s,'long',nil,menus); assert(s.page == 'network')
    for _=1,30 do model.tick(s) end
    assert(s.saver and model.activity(s,'down') and not s.saver)
    assert(s.page == 'network')
    for _=1,30 do model.tick(s) end
    assert(s.saver)
    model.pairing(s,{id='wake-pairing',device='Phone',code='123456'})
    assert(not s.saver and not s.sleeping and s.page == 'pairing' and s.selection == 1)
    for _=1,60 do model.tick(s); assert(not s.saver) end
    model.pairing(s,nil)
    s.page='confirm'
    for _=1,60 do model.tick(s); assert(not s.saver) end
    model.event(s,'resync')
    for _=1,30 do model.tick(s) end
    assert(s.saver and model.activity(s,'pressed'))
    model.activity(s,'disconnected'); model.event(s,'disconnected')
    assert(not s.saver and not s.waking and s.idle == 0)
end

-- Run the actual action backend with an in-memory UCI fixture. No commands run.
local values={settings={['.type']='settings', allow_roaming='0'},
    travel={['.type']='profile', enabled='1', name='Travel',apn='internet',auth='none',iptype='ipv4v6'}}
local commits=0
local pending=false
package.preload.uci=function() return {cursor=function(_, savedir)
    assert(savedir == '/var/run/a8-menu-uci')
    return {
        close=function() end,
        changes=function(_, config)
            assert(config == 'a8_apn')
            return pending and {a8_apn={settings={mode='auto'}}} or {}
        end,
        foreach=function(_,config,kind,fn)
            assert(config == 'a8_apn')
            for id,p in pairs(values) do if p['.type'] == kind then
                local copy={}; for k,v in pairs(p) do copy[k]=v end
                copy['.name']=id; fn(copy)
            end end
        end,
        get=function(_,config,section) assert(config=='a8_apn');return values[section] and values[section]['.type'] end,
        set=function(_,config,section,key,value) assert(config=='a8_apn');values[section][key]=value end,
        commit=function(_,config) assert(config=='a8_apn');commits=commits+1;return true end
    }
end} end
package.preload['a8_apn.model']=function() return dofile(arg[2]) end
local service=dofile(files .. '/service.lua')
package.preload['a8_display.connections']=function() return {run=function(action)
    assert(action.id == 'bt-nap' and action.value == '1');return true,'Sharing ON'
end} end
assert(service.run({id='bt-nap',value='1'}))
-- Cached battery state reaches the real view without modem/sysfs queries.
do
    local open=io.open
    for _,case in ipairs({
        {'BATT 075% CHG','BATT 075%  ||||-','BATT 075%  |||||'},
        {'BATT 075%','BATT 075%  ||||-','BATT 075%  ||||-'},
        {'BATT 000% CHG','BATT 000%  -----','BATT 000%  |----'},
        {'BATT 020%','BATT 020%  |----','BATT 020%  |----'},
        {'BATT 021%','BATT 021%  ||---','BATT 021%  ||---'},
        {'BATT 080%','BATT 080%  ||||-','BATT 080%  ||||-'},
        {'BATT 081%','BATT 081%  |||||','BATT 081%  |||||'},
        {'BATT 100% CHG','BATT 100%  ||||-','BATT 100%  |||||'},
        {'BATT 100%','BATT 100%  |||||','BATT 100%  |||||'},
        {'BATT --','BATT --','BATT --'}
    }) do
        io.open=function(path)
            if path == '/var/run/a8-connections.json' then return nil end
            assert(path == '/var/run/a8-display.status')
            return {read=function() return '\27[HOpenWrt\r\n\r\n'..case[1] end,close=function() end}
        end
        local status=service.status()
        for phase=0,1 do
            local rendered=view.frame({page='status',connected=true},status,100+phase)
            assert(rendered:find(case[phase+2],1,true))
            for row in (rendered:sub(4)..'\r\n'):gmatch('(.-)\r\n') do assert(#row==16) end
        end
    end
    io.open=open
end
assert(#service.profiles() == 1)
assert(service.run({id='auto'}))
assert(values.settings.mode == 'auto' and values.settings.database_profile == 'auto')
assert(values.settings.allow_roaming == '0')
assert(service.run({id='profile',profile='travel'}))
assert(values.settings.mode == 'profile' and values.settings.profile == 'travel')
assert(not service.run({id='profile',profile='deleted'}))
assert(not service.run({id='arbitrary shell text'}))
assert(commits == 2)
pending=true
assert(#service.profiles() == 0)
assert(not service.run({id='auto'}))
assert(not service.run({id='profile',profile='travel'}))
assert(commits == 2 and values.settings.mode == 'profile')
-- Exercise the actual action dispatcher; capture commands without rebooting.
local execute=os.execute
local commands={}
os.execute=function(command) commands[#commands+1]=command; return 0 end
for id, command in pairs({reboot='/sbin/reboot', recovery='/sbin/reboot-recovery -f',
    bootloader='/sbin/reboot-bootloader -f', edl='/sbin/reboot-edl -f', poweroff='/sbin/poweroff'}) do
    commands={}
    assert(service.run({id=id}))
    assert(#commands == 2 and commands[1] == '/bin/sync')
    assert(commands[2] == command .. ' >/dev/null 2>&1')
end
os.execute=function() return 1 end
assert(not service.run({id='bootloader'}))
os.execute=execute
print('one-button menu model, view, APN and reboot actions passed')
