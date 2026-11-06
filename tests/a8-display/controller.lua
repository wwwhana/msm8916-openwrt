-- Exercise the deployed controller using finite input streams and fake devices.
local files=arg[1]
local actual_io, actual_os=io, os
local function run(config, events, expected)
    local writes, actions, releases, command={}, {}, {}, nil
    local fake_io={}
    function fake_io.open(path)
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
    package.loaded['a8_display.model']=dofile(files .. '/model.lua')
    package.loaded['a8_display.view']=dofile(files .. '/view.lua')
    package.loaded['a8_display.service']={status=function() return {} end,
        profiles=function() return {{id='travel',label='Travel'}} end,
        run=function(action) actions[#actions+1]=action; return true,'Saved' end}
    package.loaded['a8_display.device']={supported=function() return true end,
        settings=function() return config end, pid=function() return 123 end,
        legacy_release=function(event) releases[#releases+1]=event; return true end}
    package.loaded['luci.jsonc']={stringify=function() return '{}' end}
    io=fake_io
    os={rename=function() return true end, remove=function() return true end,
        exit=function(code) assert(code == 1); error('TEST_END') end}
    local ok, err=pcall(dofile, files .. '/a8-menu')
    io, os=actual_io, actual_os
    assert(not ok and tostring(err):find('TEST_END',1,true), tostring(err))
    assert(#actions == (expected.actions or 0))
    assert(#releases == (expected.releases or 0))
    if expected.action then assert(actions[1].id == expected.action) end
    if expected.no_tty then
        for _,w in ipairs(writes) do assert(w.path ~= '/dev/tty1') end
    end
    if config.display == 'led' then
        assert(not command:find('--side',1,true))
        for _,w in ipairs(writes) do
            if w.path == '/dev/tty1' then assert(not w.data:find('A8 menu',1,true)) end
        end
    end
end

local click={'pressed','short','released:100'}
local function stream(prefix, clicks)
    local events={}
    for _,v in ipairs(prefix) do events[#events+1]=v end
    for _=1,clicks do for _,v in ipairs(click) do events[#events+1]=v end end
    return events
end
run({display='led', buttons='side'}, stream({'lcd-ready','connected','mode-power'},10), {releases=10})
run({display='lcd', buttons='power'}, stream({'lcd-missing','connected','mode-power'},10), {releases=10, no_tty=true})
run({display='lcd', buttons='power'}, stream({'lcd-ready','connected','mode-power'},10), {releases=10, tty_fails=true})

-- Select Reboot and then Confirm with the single power key.
run({display='lcd', buttons='power'}, {
    'lcd-ready','connected','mode-power',
    'pressed','short','released:100',
    'pressed','short','released:100',
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
    'lcd-ready','connected','mode-side','pressed','short','released:100','down','down',
    'pressed','short','released:100','down','resync','mode-power',
    'pressed','long','released:1200'
}, {})
print('LCD/LED controller routing and input-loss checks passed')
