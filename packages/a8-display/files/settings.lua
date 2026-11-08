-- SPDX-License-Identifier: GPL-2.0-only
local M = {}
M.seconds={0,15,30,60,120,300,600,1800}
local defaults={screensaver_timeout=30,screen_off_timeout=300}
function M.is_timeout(key) return defaults[key] ~= nil end
local function timeout(value)
    if type(value) ~= 'number' and type(value) ~= 'string' then return end
    local n=tonumber(value)
    for _,seconds in ipairs(M.seconds) do if n==seconds then return n end end
end
function M.label(seconds)
    if seconds==0 then return 'Never' end
    return seconds<60 and (seconds..' sec') or string.format('%d min',seconds/60)
end
function M.normalize(value)
    value=type(value)=='table' and value or {}
    local result={display=value.display=='lcd' and 'lcd' or 'led',
                  buttons=value.buttons=='side' and 'side' or 'power'}
    for key,default in pairs(defaults) do result[key]=timeout(value[key]) or default end
    return result
end
function M.read()
    local c=require('uci').cursor()
    -- Read committed settings: Save alone must not change the live controls.
    local ok=c:load('/etc/config/a8_device')
    local value={}
    for _,key in ipairs({'display','buttons','screensaver_timeout','screen_off_timeout'}) do
        value[key]=ok and c:get('a8_device','settings',key) or nil
    end
    c:close(); return M.normalize(value)
end
function M.entries(config)
    return {{id='screensaver_timeout',label='Screen saver',detail=M.label(config.screensaver_timeout)},
            {id='screen_off_timeout',label='Screen off',detail=M.label(config.screen_off_timeout)},
            {id='back',label='Back'}}
end
function M.choices(config,key)
    local entries={}
    for _,seconds in ipairs(M.seconds) do
        entries[#entries+1]={id='display-timeout',profile=key,value=tostring(seconds),
                            label=M.label(seconds),current=config[key]==seconds}
    end
    entries[#entries+1]={id='back',label='Back'}
    return entries
end
function M.save(key,value)
    local seconds=timeout(value)
    if not M.is_timeout(key) or seconds==nil then return false,'Invalid time' end
    local c=require('uci').cursor()
    local pending=next(c:changes('a8_device') or {})
    c:close()
    if pending then return false,'Apply web edits' end
    c=require('uci').cursor(nil,'/var/run/a8-menu-uci')
    if not c:load('/etc/config/a8_device') or c:get('a8_device','settings')~='settings' then
        c:close(); return false,'Settings missing'
    end
    local ok=c:set('a8_device','settings',key,tostring(seconds)) and c:commit('a8_device')
    c:close(); return ok and true or false,ok and 'Saved' or 'Save failed'
end
return M
