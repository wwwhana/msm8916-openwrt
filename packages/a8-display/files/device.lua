-- SPDX-License-Identifier: GPL-2.0-only
local M = {}
local function read(path)
    local f=io.open(path, 'r')
    if not f then return end
    local text=f:read('*a'); f:close(); return text
end
function M.supported()
    local compatible='\0' .. (read('/proc/device-tree/compatible') or '')
    return compatible:find('\0mifib-v18,generic\0', 1, true) ~= nil or
        compatible:find('\0jz08c-v50,generic\0', 1, true) ~= nil
end
function M.single_button()
    return ('\0' .. (read('/proc/device-tree/compatible') or '')):find(
        '\0jz08c-v50,generic\0', 1, true) ~= nil
end
function M.pid()
    return tonumber((read('/proc/self/stat') or ''):match('^(%d+)'))
end
function M.display_power(on)
    local value=on and 1 or 0
    local f=io.open('/sys/class/backlight/backlight/brightness','w')
    if not f then return false end
    local written=f:write(tostring(value))
    local closed=f:close()
    return written and closed and
        tonumber(read('/sys/class/backlight/backlight/actual_brightness')) == value or false
end
local function draw_image(image,path,options)
    local f=io.open(path..'.new','wb')
    if not f then return false end
    local written=f:write(image)
    local closed=f:close()
    if not written or not closed or not os.rename(path..'.new',path) then
        os.remove(path..'.new'); return false
    end
    local rc=os.execute('fbsplash -d /dev/fb0 -s ' .. path .. (options or '') .. ' >/dev/null 2>&1')
    return rc == 0 or rc == true
end
function M.draw_clock(image)
    return draw_image(image,'/var/run/a8-clock.ppm')
end
function M.draw_dashboard(image)
    return draw_image(image,'/var/run/a8-dashboard.ppm')
end
function M.settings()
    local config=require('a8_display.settings').read()
    if M.single_button() then config.buttons='power' end
    return config
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
    result.single_button=M.single_button()
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
    -- Keep long-hold shutdown available when the LCD is missing or disabled.
    -- Menu clicks must not trigger the legacy double-click reboot action.
    if ms < 4000 and M.single_button() then return false end
    local seconds=math.floor((ms+500)/1000)
    local rc=os.execute('ACTION=released BUTTON=power SEEN=' .. seconds .. ' /etc/rc.button/power')
    return rc == 0 or rc == true
end
return M
