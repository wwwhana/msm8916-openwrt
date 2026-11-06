-- SPDX-License-Identifier: GPL-2.0-only
local M = {}
local function cursor()
    return require('uci').cursor(nil, '/var/run/a8-menu-uci')
end
function M.profiles()
    local ok, model = pcall(require, 'a8_apn.model')
    if not ok then return {} end
    local result = {}
    local c = cursor()
    if next(c:changes('a8_apn') or {}) then return result end
    c:foreach('a8_apn', 'profile', function(p)
        if p.enabled ~= '0' and model.validate(p) then
            result[#result+1] = {id=p['.name'], label=p.name or p['.name']}
        end
    end)
    return result
end
function M.status()
    local f = io.open('/var/run/a8-display.status', 'r')
    local rows = {}
    if f then
        local data=f:read('*a'); f:close()
        data=data:gsub('^\27%[H', '')
        for row in (data .. '\r\n'):gmatch('(.-)\r\n') do rows[#rows+1]=row end
    end
    return rows
end
function M.run(action)
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
    local commands = {reboot='/sbin/reboot', recovery='/sbin/reboot recovery',
        bootloader='/sbin/reboot bootloader', edl='/sbin/reboot edl', poweroff='/sbin/poweroff'}
    local command=commands[action.id]
    if not command then return false, 'Unknown action' end
    os.execute('/bin/sync')
    local rc=os.execute(command .. ' >/dev/null 2>&1')
    local ok=rc == 0 or rc == true
    return ok, ok and 'Restarting...' or 'Action failed'
end
return M
