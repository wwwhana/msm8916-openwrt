-- SPDX-License-Identifier: GPL-2.0-only
local M = {}
local function read(path)
    local f=io.open(path, 'r')
    if not f then return end
    local text=f:read('*a'); f:close(); return text
end
function M.supported()
    return ('\0' .. (read('/proc/device-tree/compatible') or '')):find('\0mifib-v18,generic\0', 1, true) ~= nil
end
function M.pid()
    return tonumber((read('/proc/self/stat') or ''):match('^(%d+)'))
end
function M.settings()
    local c=require('uci').cursor()
    -- An absolute path skips UCI deltas: Save alone must not change controls.
    local ok=c:load('/etc/config/a8_device')
    local display=ok and c:get('a8_device', 'settings', 'display')
    local buttons=ok and c:get('a8_device', 'settings', 'buttons')
    c:close()
    return {display=display == 'lcd' and 'lcd' or 'led',
            buttons=buttons == 'side' and 'side' or 'power'}
end
function M.probe()
    local p=io.popen('/usr/libexec/a8-button --probe', 'r')
    if not p then return {} end
    local data=p:read('*a'); p:close()
    local ok, value=pcall(require('luci.jsonc').parse, data)
    return ok and type(value) == 'table' and value or {}
end
function M.status()
    local config=M.settings()
    local result=M.probe()
    result.supported=M.supported()
    result.display=config.display
    result.buttons=config.buttons
    local ok, active=pcall(require('luci.jsonc').parse, read('/var/run/a8-device.json') or '')
    local pid=ok and type(active) == 'table' and type(active.pid) == 'number' and active.pid
    if pid and (pid < 1 or pid % 1 ~= 0) then pid=nil end
    local command=pid and read('/proc/' .. string.format('%.0f', pid) .. '/cmdline') or ''
    result.active=command and command:find('/usr/libexec/a8-menu\0', 1, true) and active or {}
    return result
end
function M.legacy_release(event)
    local ms=event:match('^released:(%d+)$')
    if not ms then return false end
    ms=tonumber(ms)
    if not ms or ms > 86400000 then return false end
    local seconds=math.floor((ms+500)/1000)
    local rc=os.execute('ACTION=released BUTTON=power SEEN=' .. seconds .. ' /etc/rc.button/power')
    return rc == 0 or rc == true
end
return M
