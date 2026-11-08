-- SPDX-License-Identifier: GPL-2.0-only
local M = {}

local function availability(out, heading)
    if out:find('InvalidServiceType', 1, true) or out:find('NotSupported', 1, true) then
        return 'unsupported'
    end
    if out:find('Unknown option', 1, true) then return 'tool-unsupported' end
    return out:find(heading, 1, true) and 'available' or 'error'
end

local function quoted(out, key)
    return out:match(key .. ":%s*'([%w%-]+)'")
end

function M.registration(out)
    return {state=availability(out, 'IMS registration:'),
        status=quoted(out, 'Status'), technology=quoted(out, 'Technology')}
end

function M.services(out)
    local result = {state=availability(out, 'IMS services:')}
    for key, label in pairs({sms='SMS service', voice='Voice service'}) do
        local block = out:match(label .. '\n(.-)\n\t[^\t]') or out:match(label .. '\n(.*)') or ''
        result[key] = {status=quoted(block, 'Status'), technology=quoted(block, 'Technology')}
    end
    return result
end

function M.settings(out)
    local result = {state=availability(out, 'IMS services:')}
    for key, label in pairs({ims='IMS registration enabled', voice='Voice service enabled', sms='SMS service enabled'}) do
        result[key] = out:match(label .. ':%s*(%a+)')
    end
    return result
end

local function query(device, option)
    -- All arguments are internal constants; TERM lets qmicli release its client.
    local p = io.popen('LC_ALL=C timeout -s TERM 6 flock -n /var/lock/sim-hotplug.lock ' ..
        '/usr/bin/qmicli --device-open-proxy --device=' .. device .. ' ' .. option .. ' 2>&1', 'r')
    if not p then return '' end
    local out = p:read('*a'); p:close(); return out or ''
end

function M.client(data, now)
    if type(data) ~= 'table' or data.version ~= 1
        or type(data.pid) ~= 'number' or data.pid < 1 or data.pid % 1 ~= 0
        or type(data.slot) ~= 'number' or data.slot < 1 or data.slot > 5 or data.slot % 1 ~= 0
        or type(data.updated_at) ~= 'number' or data.updated_at < now - 60 or data.updated_at > now + 5
        or (data.registration ~= 'registered' and data.registration ~= 'registering' and data.registration ~= 'not-registered')
        or (data.sms_transport ~= nil and data.sms_transport ~= 'ims' and data.sms_transport ~= 'nas')
        or (data.sms ~= 'available' and data.sms ~= 'ready' and data.sms ~= 'unavailable') then return nil end
    local nas = data.sms_transport == 'nas'
    local ready = nas and data.sms_ready == true or (not nas and data.registration == 'registered')
	local voice = data.voice_ready == true
	local rcs = type(data.rcs) == 'table' and data.rcs or {}
	local rcs_states = {configured=true, configuring=true, ['verifying-sms']=true, registering=true, registered=true,
		['disabled-by-carrier']=true, ['unsupported-carrier']=true, ['number-required']=true,
		['rcs-sip-rejected']=true, ['rcs-sip-network']=true, ['rcs-sip-dns']=true, ['rcs-sip-timeout']=true,
		['rcs-sip-connect']=true, ['rcs-sip-tls-rejected']=true, ['rcs-sip-certificate-invalid']=true,
		['rcs-sip-certificate-host']=true, ['rcs-profile-invalid']=true, ['rcs-auth-failed']=true,
		['rcs-auth-required']=true, ['rcs-otp-timeout']=true, ['rcs-http-rejected']=true}
    local rcs_status = rcs_states[rcs.state] and rcs.state or 'unavailable'
    return {state='available', backend='modemconfig-ko', sim_slot=data.slot, checked_at=now,
        sms_transport=data.sms_transport or 'ims',
        registration={state='available', status=data.registration, technology='wwan'},
        settings={state='available', ims=data.registration == 'not-registered' and 'no' or 'yes', sms='yes', voice=voice and 'yes' or 'no'},
        services={state='available', sms={status=ready and data.sms or 'unavailable'},
            voice={status=voice and 'ready' or 'unavailable'},
            rcs={status=rcs_status, sip_status=type(rcs.sip_status)=='number' and rcs.sip_status or nil}}}
end

local function client_status()
    local file = io.open('/var/run/modemconfig-ko.json', 'r')
    if not file then return nil end
    local raw = file:read(4097); file:close()
    if not raw or #raw > 4096 then return nil end
    local ok, data = pcall(function() return require('luci.jsonc').parse(raw) end)
    if not ok then return nil end
    local result = M.client(data, os.time())
    if not result then return nil end
    local process = io.open('/proc/' .. string.format('%.0f', data.pid) .. '/comm', 'r')
    if not process then return nil end
    local name = process:read('*l'); process:close()
    return name == 'modemconfig-ko' and result or nil
end

function M.status()
    local client = client_status()
    if client then return client end
    local device
    for _, path in ipairs({'/dev/wwan0qmi0', '/dev/cdc-wdm0'}) do
        local ok = os.execute('test -c ' .. path)
        if ok == 0 or ok == true then device=path; break end
    end
    if not device then return {state='no-device'} end
    local versions = query(device, '--get-service-version-info')
    if not versions:find('Supported versions:', 1, true) then return {state='error'} end
    local imsa = versions:match('\n%s*imsa%s*%(') ~= nil
    local ims = versions:match('\n%s*ims%s*%(') ~= nil
    local result = {state=(ims or imsa) and 'available' or 'unsupported', checked_at=os.time(),
        registration={state='unsupported'}, services={state='unsupported'}, settings={state='unsupported'}}
    if imsa then
        result.registration=M.registration(query(device, '--imsa-get-ims-registration-status'))
        if result.registration.state == 'available' then
            result.services=M.services(query(device, '--imsa-get-ims-services-status'))
        else result.services.state=result.registration.state end
    end
    if ims then
        result.settings=M.settings(query(device, '--ims-get-ims-services-enabled-setting'))
        if result.settings.state ~= 'available' then
            result.settings=require('a8_apn.qmi').legacy_settings(device)
        end
    end
    return result
end

return M
