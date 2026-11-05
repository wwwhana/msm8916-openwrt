-- SPDX-License-Identifier: GPL-2.0-only
local json = require('luci.jsonc')
local uci = require('uci')
local model = require('a8_apn.model')
local M = {}
local function read(path)
    local f = io.open(path, 'r'); if not f then return nil end
    local s = f:read('*a'); f:close(); return s
end
local function query(args)
    -- Arguments come from validated device/object identifiers, never shell text.
    local p = io.popen('/usr/bin/mmcli --timeout=3 ' .. args .. ' --output-json 2>/dev/null', 'r')
    if not p then return {} end
    local s = p:read('*a'); p:close(); return json.parse(s) or {}
end
local function get_sim(device)
    if not device:match('^[%w_./%-]+$') then return {present=false} end
    local modem = query('--modem=' .. device).modem or {}
    local g = modem.generic or {}
    local path = g.sim or ''
    if not path:match('^/org/freedesktop/ModemManager1/SIM/%d+$') then
        return {present=false, modem_state=g.state}
    end
    local sim = (query('--sim=' .. path).sim or {}).properties or {}
    return {present=true, home_plmn=sim['operator-code'], provider=sim['operator-name'],
        locked=g['unlock-required'] and g['unlock-required'] ~= 'none' and g['unlock-required'] ~= 'unknown',
        modem_state=g.state}
end

function M.inspect()
    -- Ignore LuCI's uncommitted /tmp/.uci edits. Only committed configuration
    -- is used, and the APN worker has its own delta directory.
    local c = uci.cursor(nil, '/var/run/a8-apn-uci')
    local settings = c:get_all('a8_apn', 'settings') or {}
    local interface = settings.interface or 'modem'
    if not interface:match('^[%w_]+$') then interface = 'modem' end
    local saved = {}
    c:foreach('a8_apn', 'profile', function(p) p.id=p['.name']; saved[#saved+1]=p end)
    local device = c:get('network', interface, 'device') or 'qcom-soc'
    local sim = get_sim(device)
    local db = {}
    if model.valid_plmn(sim.home_plmn) then
        db = json.parse(read('/usr/share/a8-apn/' .. sim.home_plmn .. '.json') or '[]') or {}
        for _, spec in ipairs({
            {'/etc/qcom-carrier-autocfg/custom-apns.tsv', 'custom'},
            {'/usr/share/qcom-carrier-autocfg/apns.tsv', 'carrier'}
        }) do
            local p = model.tsv_profile(read(spec[1]), sim.home_plmn, spec[2])
            if p then db[#db+1] = p end
        end
    end
    local chosen, reason = model.choose(settings, saved, db, sim)
    local candidates = {}
    for _, p in ipairs(db) do candidates[#candidates+1] = model.public(p) end
    local status = {state=chosen and 'ready' or reason, mode=settings.mode or 'auto',
        home_plmn=sim.home_plmn, provider=sim.provider, modem_state=sim.modem_state,
        selected=model.public(chosen), candidates=candidates,
        allow_roaming=settings.allow_roaming == '1'}
    return status, c, interface, chosen, settings
end

function M.apply()
    local status, c, interface, profile, settings = M.inspect()
    local path = '/var/run/a8-apn.json'
    local previous = json.parse(read(path) or '{}') or {}
    if profile then
        local changed, err = model.apply(c, interface, profile, settings)
        if changed == nil then status.state=err
        elseif changed or previous.state == 'restart-failed' then
            -- Restart only the cellular interface; Wi-Fi/LAN/USB stay up.
            local result = os.execute('/sbin/ifup ' .. interface .. ' >/dev/null 2>&1')
            status.state = (result == 0 or result == true) and 'applied' or 'restart-failed'
        else status.state='ready' end
    end
    local f = io.open(path .. '.tmp', 'w')
    if f then f:write(json.stringify(status)); f:close(); os.rename(path .. '.tmp', path) end
    return status
end
return M
