local files=arg[1]
local model=dofile(files .. '/model.lua')
local view=dofile(files .. '/view.lua')
local state=model.new()
local function event(e) return model.event(state, e, {{id='travel',label='Travel'}}) end
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
event('connected'); assert(state.connected); frame()
event('short'); assert(state.page == 'menu'); frame()
event('short'); assert(state.entries[state.selection].id == 'apn')
event('long'); assert(state.page == 'profiles'); frame()
assert(state.entries[1].id == 'auto' and state.entries[2].profile == 'travel')
event('long'); assert(state.page == 'confirm'); frame()
assert(event('long') == nil) -- Cancel is selected initially.
assert(state.page == 'menu')
event('short'); event('long'); event('short'); event('long')
assert(state.pending.profile == 'travel')
event('short')
local action=event('long')
assert(action.id == 'profile' and action.profile == 'travel')
assert(state.page == 'menu' and state.pending == nil)
for _,id in ipairs({'reboot','recovery','bootloader','edl','poweroff'}) do
    state=model.new(); event('long')
    while state.entries[state.selection].id ~= id do event('short') end
    assert(event('long') == nil and state.page == 'confirm'); frame()
    event('short'); assert(event('long').id == id)
end
state=model.new(); event('long')
for _=1,8 do event('short') end
assert(state.selection == 1)
model.result(state, false, string.rep('\27\n\255',30)); frame()
event('disconnected'); assert(not state.connected)
event('short'); assert(state.page == 'menu')
event('short'); event('long'); event('long'); event('short')
assert(state.page == 'confirm' and state.selection == 2)
event('disconnected'); assert(state.page == 'status' and state.pending == nil)
event('connected'); assert(event('long') == nil and state.page == 'menu')

-- Run the actual action backend with an in-memory UCI fixture. No commands run.
local values={settings={['.type']='settings', allow_roaming='0'},
    travel={['.type']='profile', enabled='1', name='Travel',apn='internet',auth='none',iptype='ipv4v6'}}
local commits=0
local pending=false
package.preload.uci=function() return {cursor=function(_, savedir)
    assert(savedir == '/var/run/a8-menu-uci')
    return {
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
print('one-button menu model, view and APN actions passed')
