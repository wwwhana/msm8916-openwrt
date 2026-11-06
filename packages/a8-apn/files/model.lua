-- SPDX-License-Identifier: GPL-2.0-only
local M = {}
local function enabled(v) return v ~= '0' and v ~= false end
local function normalize(s) return (s or ''):lower():gsub('[%s%p]', '') end
local function valid_plmn(s) return type(s) == 'string' and s:match('^%d%d%d%d%d%d?$') end
M.valid_plmn = valid_plmn

local radio_modes = {
    {id='auto', allowed='4g|3g', preferred='none'},
    {id='lte-preferred', allowed='4g|3g', preferred='4g'},
    {id='lte-only', allowed='4g', preferred='none'},
    {id='3g-preferred', allowed='4g|3g', preferred='3g'},
    {id='3g-only', allowed='3g', preferred='none'}
}
local function sorted_modes(allowed)
    local modes={}
    for mode in allowed:gmatch('[^|,%s]+') do modes[#modes+1]=mode end
    table.sort(modes)
    return table.concat(modes, '|')
end
local function radio_key(text)
    if type(text) ~= 'string' then return nil end
    local allowed, preferred = text:match('^allowed:%s*([^;]+);%s*preferred:%s*(%w+)%s*$')
    if allowed then return sorted_modes(allowed) .. ';' .. preferred end
end
function M.radio_mode(id)
    if not id or id == 'keep' then return {} end
    for _, mode in ipairs(radio_modes) do if mode.id == id then return mode end end
    return nil, 'invalid-radio-mode'
end
function M.supported_radio_modes(reported)
    local supported, result = {}, {}
    for _, text in ipairs(type(reported) == 'table' and reported or {}) do
        local key=radio_key(text)
        if key then supported[key]=true end
    end
    for _, mode in ipairs(radio_modes) do
        if supported[sorted_modes(mode.allowed) .. ';' .. mode.preferred] then result[#result+1]=mode.id end
    end
    return result
end
function M.current_radio_mode(reported)
    local key=radio_key(reported)
    for _, mode in ipairs(radio_modes) do
        if key == sorted_modes(mode.allowed) .. ';' .. mode.preferred then return mode.id end
    end
end

function M.validate(p)
    if type(p) ~= 'table' or type(p.apn) ~= 'string' or #p.apn > 100
        or not p.apn:match('^[A-Za-z0-9][A-Za-z0-9%.%-]*$') then return nil, 'invalid-apn' end
    if not ({none=true,pap=true,chap=true,both=true})[p.auth or 'none'] then return nil, 'invalid-auth' end
    if not ({ipv4=true,ipv6=true,ipv4v6=true})[p.iptype or 'ipv4v6'] then return nil, 'invalid-iptype' end
    for _, k in ipairs({'username','password'}) do
        if type(p[k] or '') ~= 'string' or #(p[k] or '') > 512 or (p[k] or ''):find('[,%c]') then return nil, 'invalid-credentials' end
    end
    return p
end

-- Keep the existing per-operator TSV overrides and built-in carrier table.
function M.tsv_profile(text, code, source)
    for line in (text or ''):gmatch('[^\r\n]+') do
        local plmn, name, apn, iptype = line:match('^([^\t]+)\t([^\t]*)\t([^\t]*)\t([^\t]*)\t')
        if plmn == code then
            local p = {id=source .. '-' .. code, provider=name, name=name, apn=apn,
                iptype=iptype, auth='none', source=source}
            if M.validate(p) then return p end
        end
    end
end

function M.choose(settings, saved, database, sim)
    if not enabled(settings.enabled) then return nil, 'disabled' end
    if not sim.present then return nil, 'no-sim' end
    if sim.locked then return nil, 'sim-locked' end
    if settings.mode ~= 'auto' and settings.mode ~= 'profile' then return nil, 'invalid-mode' end
    if settings.mode == 'profile' then
        for _, p in ipairs(saved) do
            if p.id == settings.profile and enabled(p.enabled) then return M.validate(p) end
        end
        return nil, 'profile-not-found'
    end
    if not valid_plmn(sim.home_plmn) then return nil, 'waiting-sim' end
    local spn = normalize(sim.provider)
    for _, p in ipairs(saved) do
        local match = normalize(p.provider_match)
        if enabled(p.enabled) and p.home_plmn == sim.home_plmn and (match == '' or spn:find(match, 1, true)) then
            return M.validate(p)
        end
    end
    local first, matching, custom, carrier
    for _, p in ipairs(database) do
        local good = M.validate(p)
        if good then
            first = first or p
            if settings.database_profile == p.id then return p end
            if p.source == 'custom' then custom = custom or p
            elseif p.source == 'carrier' then carrier = carrier or p
            elseif spn ~= '' and normalize(p.provider) == spn then matching = matching or p end
        end
    end
    if custom or matching or carrier or first then return custom or matching or carrier or first end
    return nil, 'no-match'
end

function M.public(profile)
    if not profile then return nil end
    return {id=profile.id, name=profile.name or profile.label or profile.id,
        provider=profile.provider, country=profile.country, apn=profile.apn, iptype=profile.iptype,
        source=profile.source}
end

-- A cursor is passed in so file changes and connection restarts are testable
-- independently of modem queries and the LuCI view.
function M.apply(cursor, interface, profile, settings)
    local valid, err = M.validate(profile)
    if not valid then return nil, err end
    local radio, radio_err = M.radio_mode(settings.radio_mode)
    if not radio then return nil, radio_err end
    if settings.iptype ~= 'profile' and not ({ipv4=true,ipv6=true,ipv4v6=true})[settings.iptype] then
        return nil, 'invalid-iptype'
    end
    if cursor:get('network', interface) ~= 'interface' or cursor:get('network', interface, 'proto') ~= 'modemmanager' then
        return nil, 'wrong-interface'
    end
    local auth = profile.auth or 'none'
    local values = {apn=profile.apn, username=profile.username or '', password=profile.password or '',
        iptype=settings.iptype ~= 'profile' and settings.iptype or profile.iptype or 'ipv4v6',
        allow_roaming=settings.allow_roaming == '1' and '1' or '0'}
    if radio.allowed then
        values.allowedmode=radio.allowed
        values.preferredmode=radio.preferred
    end
    local changed = false
    for key, value in pairs(values) do
        if (cursor:get('network', interface, key) or '') ~= value then
            if value == '' then cursor:delete('network', interface, key)
            else cursor:set('network', interface, key, value) end
            changed = true
        end
    end
    local wanted = auth == 'both' and {'pap','chap'} or {auth}
    local old = cursor:get('network', interface, 'allowedauth') or {}
    if type(old) == 'string' then old = {old} end
    if table.concat(old, ',') ~= table.concat(wanted, ',') then
        cursor:set('network', interface, 'allowedauth', wanted); changed = true
    end
    -- Older BSP images used 'auth', which this ModemManager proto ignores.
    if cursor:get('network', interface, 'auth') then cursor:delete('network', interface, 'auth'); changed = true end
    if changed then
        local ok = cursor:commit('network')
        if not ok then return nil, 'commit-failed' end
    end
    return changed
end
return M
