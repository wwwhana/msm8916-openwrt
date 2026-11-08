-- SPDX-License-Identifier: GPL-2.0-only
local M = {}
function M.connections()
    return require('a8_display.connections').entries()
end
local function cursor()
    return require('uci').cursor(nil, '/var/run/a8-menu-uci')
end
function M.profiles()
    local ok, model = pcall(require, 'a8_apn.model')
    if not ok then return {} end
    local result = {}
    local c = cursor()
    if next(c:changes('a8_apn') or {}) then c:close(); return result end
    c:foreach('a8_apn', 'profile', function(p)
        if p.enabled ~= '0' and model.validate(p) then
            result[#result+1] = {id=p['.name'], label=p.name or p['.name']}
        end
    end)
    c:close(); return result
end
function M.status()
    local f = io.open('/var/run/a8-display.status', 'r')
    local rows = {}
    if f then
        local data=f:read('*a'); f:close()
        data=data:gsub('^\27%[H', '')
        for row in (data .. '\r\n'):gmatch('(.-)\r\n') do rows[#rows+1]=row end
    end
    local capacity=tonumber((rows[3] or ''):match('^BATT (%d+)%%'))
    if capacity and capacity>=0 and capacity<=100 then
        rows.battery={capacity=capacity,charging=rows[3]:match('%sCHG%s*$') ~= nil}
    end
    local cache=io.open('/var/run/a8-connections.json','r')
    if cache then
        local data=cache:read('*a'); cache:close()
        local ok,value=pcall(function() return require('luci.jsonc').parse(data) end)
        if ok and type(value)=='table' then rows.dashboard=value end
    end
    return rows
end
function M.pairing()
    return require('a8_display.pairing').pending()
end
function M.call()
    return require('a8_display.calls').read(os.time())
end
function M.thumbnail(id)
    return require('a8_display.messages').thumbnail(id)
end
function M.run(action)
    if action.id == 'display-timeout' then
        return require('a8_display.settings').save(action.profile,action.value)
    end
    if action.id == 'sms-list' then return require('a8_display.messages').list(action.profile) end
    if action.id == 'sms-open' then return require('a8_display.messages').read(action.profile) end
    if action.id == 'pairing' then
        return require('a8_display.pairing').respond(action.profile,action.value == '1')
    end
    if action.id == 'pairable' or action.id == 'wifi' or action.id == 'relay' or action.id == 'policy' or
       action.id == 'tailscale' or action.id == 'tailscale-status' or action.id == 'usb-function' or
       action.id == 'usb-restart' or action.id == 'bluetooth' or action.id == 'bt-nap' or action.id == 'bt-compat' or action.id == 'wireguard' then
        return require('a8_display.connections').run(action)
    end
    if action.id == 'auto' or action.id == 'profile' then
        local c = cursor()
        if next(c:changes('a8_apn') or {}) then return false, 'Apply web edits' end
        if c:get('a8_apn', 'settings') ~= 'settings' then return false, 'APN unavailable' end
        if action.id == 'profile' then
            local found = false
            for _, p in ipairs(M.profiles()) do if p.id == action.profile then found=true end end
            if not found then return false, 'Profile changed' end
            c:set('a8_apn', 'settings', 'profile', action.profile)
        end
        c:set('a8_apn', 'settings', 'enabled', '1')
        c:set('a8_apn', 'settings', 'mode', action.id == 'auto' and 'auto' or 'profile')
        if action.id == 'auto' then c:set('a8_apn', 'settings', 'database_profile', 'auto') end
        if not c:commit('a8_apn') then return false, 'Save failed' end
        -- The existing carrier worker applies this, including no-SIM handling.
        return true, 'APN saved'
    end
    -- BusyBox reboot ignores a mode operand. Use the RESTART2 helper directly
    -- after syncing; its graceful wrapper would stop this service mid-action.
    local commands = {reboot='/sbin/reboot', recovery='/sbin/reboot-recovery -f',
        bootloader='/sbin/reboot-bootloader -f', edl='/sbin/reboot-edl -f', poweroff='/sbin/poweroff'}
    local command=commands[action.id]
    if not command then return false, 'Unknown action' end
    os.execute('/bin/sync')
    local rc=os.execute(command .. ' >/dev/null 2>&1')
    local ok=rc == 0 or rc == true
    return ok, ok and 'Restarting...' or 'Action failed'
end
return M
