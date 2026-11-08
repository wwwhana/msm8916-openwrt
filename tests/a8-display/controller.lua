-- Exercise the deployed controller using finite input streams and fake devices.
local files=arg[1]
local actual_io, actual_os=io, os
local function run(config, events, expected)
    local writes, actions, releases, clocks, dashboards, screens, command={}, {}, {}, {}, {}, {}, nil
    local thumbnails, powers, powered, call_ticks=0,{},nil,0
    local fake_io={}
    function fake_io.open(path)
        if path=='/usr/share/a8-display/unifont.bin' then return actual_io.open(files..'/unifont.bin','rb') end
        local f={}
        function f:write(data) writes[#writes+1]={path=path, data=data}; return self end
        function f:setvbuf() end
        function f:close() end
        if path == '/dev/tty1' and expected.tty_fails then return nil end
        return f
    end
    function fake_io.popen(cmd)
        command=cmd
        return {
            lines=function()
                local i=0
                return function() i=i+1; return events[i] end
            end,
            close=function() end
        }
    end
    package.loaded['a8_display.settings']=dofile(files .. '/settings.lua')
    package.loaded['a8_display.model']=dofile(files .. '/model.lua')
    package.loaded['a8_display.view']=dofile(files .. '/view.lua')
    local screen=package.loaded['a8_display.view'].screen
    package.loaded['a8_display.view'].screen=function(s,status,now)
        screens[#screens+1]={page=s.page,selection=s.selection,call=status.call and status.call.state}
        return screen(s,status,now)
    end
    package.loaded['a8_display.dashboard']=dofile(files .. '/dashboard.lua')
    package.loaded['a8_display.font']=dofile(files .. '/font.lua')
    package.loaded['a8_display.service']={status=function() return {} end,call=function()
            call_ticks=call_ticks+1
            if expected.calls then return expected.calls(call_ticks) end
            return expected.call
        end,
        pairing=function() return nil end, connections=function() return {network={{label='Station ON',id='relay',value='0'}}} end,
        profiles=function() return {{id='travel',label='Travel'}} end,
        run=function(action)
            actions[#actions+1]=action
            if action.id=='sms-list' then return true,{{id='sms-open',profile='1',label='114',detail='11-07 21:30'}} end
            if action.id=='sms-open' then return true,{sender='114',timestamp='11-07 21:30',body=string.rep('A',100),images=expected.images and {{id='photo'}} or nil} end
            if action.id=='display-timeout' then config[action.profile]=tonumber(action.value) end
            return true,'Saved'
        end,
        thumbnail=function(id)
            assert(id=='photo');thumbnails=thumbnails+1
            if not expected.image_fails then return 'P6\n112 80\n255\n'..string.rep('\255',112*80*3) end
        end}
    package.loaded['a8_display.device']={supported=function() return true end,
        settings=function() return config end, pid=function() return 123 end,
        display_power=function(on)
            powers[#powers+1]=on
            if not on and expected.off_fails_once then expected.off_fails_once=false; return false end
            powered=on; return true
        end,
        draw_clock=function(image) assert(powered); clocks[#clocks+1]=image; return not expected.clock_fails end,
        draw_dashboard=function(image) assert(powered); dashboards[#dashboards+1]=image; return not expected.dashboard_fails end,
        legacy_release=function(event) releases[#releases+1]=event; return true end}
    package.loaded['luci.jsonc']={stringify=function() return '{}' end}
    io=fake_io
    os={rename=function() return true end, remove=function() return true end, time=function() return 100 end,
        date=actual_os.date,
        exit=function(code) assert(code == 1); error('TEST_END') end}
    local ok, err=pcall(dofile, files .. '/a8-menu')
    io, os=actual_io, actual_os
    assert(not ok and tostring(err):find('TEST_END',1,true), tostring(err))
    assert(#actions == (expected.actions or 0))
    assert(#releases == (expected.releases or 0))
    if expected.action then assert(actions[1].id == expected.action) end
    if expected.clocks then assert(#clocks == expected.clocks) end
    if expected.dashboards then assert(#dashboards == expected.dashboards) end
    if expected.powers then
        assert(#powers==#expected.powers)
        for i,on in ipairs(expected.powers) do assert(powers[i]==on) end
    end
    if expected.images then assert(thumbnails==1) end
    if expected.no_tty then
        for _,w in ipairs(writes) do assert(w.path ~= '/dev/tty1') end
    end
    if config.display == 'led' then
        assert(#powers==0)
        assert(not command:find('--side',1,true))
        for _,w in ipairs(writes) do
            if w.path == '/dev/tty1' then assert(not w.data:find('Menu',1,true)) end
        end
    end
    return writes,clocks,screens
end

local click={'pressed','short','released:100'}
local function stream(prefix, clicks)
    local events={}
    for _,v in ipairs(prefix) do events[#events+1]=v end
    for _=1,clicks do for _,v in ipairs(click) do events[#events+1]=v end end
    return events
end
run({display='lcd',buttons='power'}, {'lcd-ready','connected','mode-power','tick','tick'}, {dashboards=1})
-- The real controller dispatches inbox/body reads and returns to the same menu.
local sms_events=stream({'lcd-ready','connected','mode-power'},3)
for _,event in ipairs({'pressed','long','released:1200','pressed','long','released:1200',
    'pressed','short','released:100','pressed','long','released:1200'}) do sms_events[#sms_events+1]=event end
local _,_,sms_screens=run({display='lcd',buttons='power'},sms_events,{actions=2,action='sms-list'})
assert(sms_screens[#sms_screens].page=='sms' and sms_screens[#sms_screens].selection==1)
run({display='lcd',buttons='power'},sms_events,{actions=2,images=true})
run({display='lcd',buttons='power'},sms_events,{actions=2,images=true,image_fails=true})
local failed_dashboard=run({display='lcd',buttons='power'}, {'lcd-ready','connected','mode-power','tick'}, {dashboard_fails=true})
assert(failed_dashboard[#failed_dashboard].data:find('PRESS FOR MENU',1,true))
run({display='led', buttons='side'}, stream({'lcd-ready','connected','mode-power'},10), {releases=10})
run({display='lcd', buttons='power'}, stream({'lcd-missing','connected','mode-power'},10), {releases=10, no_tty=true})
run({display='lcd', buttons='power'}, stream({'lcd-ready','connected','mode-power'},10), {releases=10, tty_fails=true})

-- Select Reboot and then Confirm with the single power key.
run({display='lcd', buttons='power'}, {
    'lcd-ready','connected','mode-power',
    'pressed','short','released:100',
    'pressed','short','released:100',
    'pressed','short','released:100',
    'pressed','short','released:100',
    'pressed','long','released:1200',
    'pressed','short','released:100',
    'pressed','long','released:1200',
    'pressed','short','released:100',
    'pressed','long','released:1200'
}, {actions=1, action='reboot'})

-- Missing screen or lost input midway through a hold cannot power off.
run({display='lcd', buttons='power'}, {
    'lcd-ready','connected','mode-power','pressed','long','lcd-missing','released:5000'
}, {})
run({display='led', buttons='power'}, {
    'lcd-missing','connected','mode-power','pressed','resync','released:5000'
}, {no_tty=true})
run({display='led', buttons='power'}, {
    'lcd-missing','connected','mode-power','pressed','long','released:5000'
}, {releases=1, no_tty=true})

-- Side navigation uses a short power click to select. An unplug cancels Confirm.
run({display='lcd', buttons='side'}, {
    'lcd-ready','connected','mode-side','pressed','short','released:100','down','down','down',
    'pressed','short','released:100','pressed','short','released:100','down','resync','mode-power',
    'pressed','long','released:1200'
}, {})

-- A long first press wakes the saved Power selection without opening it.
local idle=stream({'lcd-ready','connected','mode-power'},4)
for _=1,30 do idle[#idle+1]='tick' end
for _,e in ipairs({'pressed','long','tick','released:1200'}) do idle[#idle+1]=e end
local writes,clocks,screens=run({display='lcd',buttons='power'},idle,{clocks=1})
local cleared, last=false,nil
for _,w in ipairs(writes) do
    if w.path == '/dev/tty1' then
        if w.data == '\27[2J' and #clocks > 0 then cleared=true end
        last=w.data
    end
end
assert(cleared and screens[#screens].page=='menu' and screens[#screens].selection==4)
local sleeping={'lcd-ready','connected','mode-power'}
for _=1,31 do sleeping[#sleeping+1]='tick' end
local fallback=run({display='lcd',buttons='power'},sleeping,{clock_fails=true,clocks=2})
assert(fallback[#fallback].data:find(actual_os.date('%H:%M:%S',100),1,true))
-- Long idle switches the real backlight off once and stops framebuffer writes.
local dark=stream({'lcd-ready','connected','mode-power'},4)
for _=1,345 do dark[#dark+1]='tick' end
run({display='lcd',buttons='power'},dark,{clocks=1,powers={true,false,true}})
run({display='lcd',buttons='power'},dark,{clocks=1,off_fails_once=true,powers={true,false,false,true}})
for _,event in ipairs({'pressed','long','tick','released:1200'}) do dark[#dark+1]=event end
local _,_,awake=run({display='lcd',buttons='power'},dark,{clocks=1,powers={true,false,true}})
assert(awake[#awake].page=='menu' and awake[#awake].selection==4)

-- An incoming call wakes the real display path; keys cannot select Power behind it.
local call_events=stream({'lcd-ready','connected','mode-power'},4)
for _=1,346 do call_events[#call_events+1]='tick' end
local _,_,call_screens=run({display='lcd',buttons='power'},call_events,{powers={true,false,true},calls=function(tick)
    if tick==344 then return {state='ringing',direction='incoming',peer='01000000001',audio='browser'} end
    if tick==345 then return {state='active',direction='incoming',peer='01000000001',duration=65,audio='bluetooth'} end
end})
local incoming,active=false,false
for _,screen in ipairs(call_screens) do incoming=incoming or screen.call=='ringing'; active=active or screen.call=='active' end
assert(incoming and active and call_screens[#call_screens].call==nil)
assert(call_screens[#call_screens].page=='menu' and call_screens[#call_screens].selection==4)

local duration_events=stream({'lcd-ready','connected','mode-power'},5)
for _,event in ipairs({'pressed','long','released:1200','pressed','long','released:1200',
    'pressed','short','released:100','pressed','long','released:1200'}) do duration_events[#duration_events+1]=event end
local duration_config={display='lcd',buttons='power'}
run(duration_config,duration_events,{actions=1,action='display-timeout'})
assert(duration_config.screensaver_timeout==60)

-- The hardware adapter reports failure unless actual brightness follows the write.
local brightness, actual, blocked, write_failed, closed_failed=1,1,false,false,false
io={open=function(path,mode)
    if mode=='w' then
        assert(path=='/sys/class/backlight/backlight/brightness')
        if blocked then return nil end
        return {write=function(_,value)
            if write_failed then return nil end
            brightness=tonumber(value); return true
        end,close=function() return not closed_failed end}
    end
    assert(path=='/sys/class/backlight/backlight/actual_brightness')
    return {read=function() return tostring(actual) end,close=function() end}
end}
local hardware=dofile(files..'/device.lua')
actual=0; assert(hardware.display_power(false) and brightness==0)
actual=1; assert(hardware.display_power(true) and brightness==1)
actual=1; assert(not hardware.display_power(false))
blocked=true; assert(not hardware.display_power(false)); blocked=false
write_failed=true; assert(not hardware.display_power(false)); write_failed=false
closed_failed=true; assert(not hardware.display_power(false))
io=actual_io
print('LCD/LED routing, backlight sleep/wake and input-loss checks passed')
