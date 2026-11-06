local model = dofile(arg[1])
local function profile(id, apn, extra)
    local p = {id=id, apn=apn, iptype='ipv4v6', auth='none'}
    for k,v in pairs(extra or {}) do p[k]=v end
    return p
end
local auto = {enabled='1', mode='auto', database_profile='auto', iptype='profile', allow_roaming='0'}
local sim = {present=true, home_plmn='45006', provider='LG U+', visited_plmn='44010'}
local lgu = profile('lgu', 'internet.lguplus.co.kr', {provider='LG U+'})
local other = profile('other', 'other.example')
local saved = profile('travel', 'travel.example', {home_plmn='45006', provider_match='LG U+'})

local p, reason = model.choose(auto, {}, {lgu}, {present=false})
assert(p == nil and reason == 'no-sim')
p, reason = model.choose(auto, {}, {lgu}, {present=true, locked=true})
assert(p == nil and reason == 'sim-locked')
assert(model.choose(auto, {}, {lgu}, sim) == lgu) -- never the visited network
assert(model.choose(auto, {saved}, {lgu}, sim) == saved)
saved.provider_match = 'another brand'
assert(model.choose(auto, {saved}, {lgu}, sim) == lgu)
assert(model.choose({enabled='0'}, {}, {lgu}, sim) == nil)
assert(model.choose({enabled='1', mode='profile', profile='travel'}, {saved}, {}, sim) == saved)
p, reason = model.choose(auto, {}, {}, sim)
assert(p == nil and reason == 'no-match')
p, reason = model.choose({enabled='1', mode='bogus'}, {}, {lgu}, sim)
assert(p == nil and reason == 'invalid-mode')

local tsv = '# comment\n45006\tMy carrier\tcustom.example\tipv4\t4g\tunused.mbn\n'
local custom = assert(model.tsv_profile(tsv, '45006', 'custom'))
assert(custom.apn == 'custom.example' and custom.iptype == 'ipv4')
assert(model.tsv_profile(tsv, '45005', 'custom') == nil)
assert(model.choose(auto, {}, {lgu, custom}, sim) == custom)
local chosen = {enabled='1', mode='auto', database_profile='lgu'}
assert(model.choose(chosen, {}, {lgu, custom}, sim) == lgu)
chosen.database_profile='old-sim-only'
assert(model.choose(chosen, {}, {other}, {present=true, home_plmn='23415'}) == other)

for _,bad in ipairs({'', 'internet,foo=bar', 'a\nb', string.rep('x', 101)}) do
    assert(model.validate(profile('bad', bad)) == nil)
end
assert(model.validate(profile('bad', 'apn', {auth='bad'})) == nil)
assert(model.validate(profile('bad', 'apn', {iptype='bad'})) == nil)
assert(model.validate(profile('bad', 'apn', {password='a,b'})) == nil)
assert(model.validate(profile('bad', 'apn', {password='a\nb'})) == nil)

-- Exercise the same cursor methods the service uses, with a file on commit.
local values = {proto='modemmanager', apn='jionet', username='old', password='old', auth='pap'}
local writes, commits = 0, 0
local cursor = {}
function cursor:get(config, interface, key)
    assert(config == 'network' and interface == 'modem')
    if key == nil then return 'interface' end
    return values[key]
end
function cursor:set(_, _, key, value) values[key]=value; writes=writes+1; return true end
function cursor:delete(_, _, key) values[key]=nil; writes=writes+1; return true end
function cursor:commit(config)
    assert(config == 'network'); commits=commits+1
    local f = assert(io.open(arg[2], 'w'))
    f:write(values.apn, '\n', values.iptype, '\n', values.allow_roaming, '\n', table.concat(values.allowedauth, ','))
    f:close(); return true
end
assert(model.apply(cursor, 'modem', lgu, auto) == true)
assert(values.apn == 'internet.lguplus.co.kr' and values.iptype == 'ipv4v6')
assert(values.username == nil and values.password == nil and values.auth == nil)
assert(values.allowedauth[1] == 'none' and values.allow_roaming == '0')
local before = writes
assert(model.apply(cursor, 'modem', lgu, auto) == false)
assert(writes == before and commits == 1)
local roaming = {iptype='ipv4', allow_roaming='1'}
local authenticated = profile('auth', 'private.example', {auth='both', username='user', password='pass'})
assert(model.apply(cursor, 'modem', authenticated, roaming) == true)
assert(values.iptype == 'ipv4' and values.allow_roaming == '1')
assert(table.concat(values.allowedauth, ',') == 'pap,chap')
assert(values.username == 'user' and values.password == 'pass')
assert(model.public(authenticated).password == nil and model.public(authenticated).username == nil)
local mode_settings={iptype='ipv4', allow_roaming='1'}
for id, expected in pairs({['lte-preferred']={'4g|3g','4g'}, ['lte-only']={'4g','none'},
    ['3g-preferred']={'4g|3g','3g'}, ['3g-only']={'3g','none'}, auto={'4g|3g','none'}}) do
    mode_settings.radio_mode=id
    assert(model.apply(cursor, 'modem', authenticated, mode_settings) == true)
    assert(values.allowedmode == expected[1] and values.preferredmode == expected[2])
    assert(model.apply(cursor, 'modem', authenticated, mode_settings) == false)
end
mode_settings.radio_mode='keep'
before=writes
assert(model.apply(cursor, 'modem', authenticated, mode_settings) == false and writes == before)
mode_settings.radio_mode='4g; arbitrary command'
p, reason=model.apply(cursor, 'modem', authenticated, mode_settings)
assert(p == nil and reason == 'invalid-radio-mode' and writes == before)
values.proto='qmi'
p, reason = model.apply(cursor, 'modem', lgu, auto)
assert(p == nil and reason == 'wrong-interface')
values.proto='modemmanager'
function cursor:commit() return false end
p, reason = model.apply(cursor, 'modem', lgu, auto)
assert(p == nil and reason == 'commit-failed')

-- Staged settings must not reach modem queries, configuration writes or ifup.
local pending_config, lock
local reported_modes={'allowed: 3g; preferred: none', 'allowed: 4g; preferred: none',
    'allowed: 3g, 4g; preferred: 4g', 'allowed: 4g, 3g; preferred: 3g'}
local settings = {mode='auto',interface='modem'}
package.preload.uci=function() return {cursor=function()
    return {
        get_all=function(_, config, section)
            assert(config == 'a8_apn' and section == 'settings')
            return settings
        end,
        changes=function(_, config)
            return pending_config == config and {[config]={modem={apn='unsaved.example'}}} or {}
        end,
        foreach=function(_, config, kind, fn)
            assert(config == 'a8_apn' and kind == 'profile')
            fn({['.name']='travel', apn='travel.example', auth='none', iptype='ipv4v6'})
        end,
        get=function(_, config, interface, key)
            assert(config == 'network' and interface == 'modem' and key == 'device')
            return 'qcom-soc'
        end
    }
end} end
package.preload['luci.jsonc']=function() return {parse=function(value)
    if value == 'modem' then return {modem={generic={
        sim='/org/freedesktop/ModemManager1/SIM/0', state='registered', ['unlock-required']=lock,
        ['supported-modes']=reported_modes, ['current-modes']='allowed: 3g, 4g; preferred: 4g'}}} end
    if value == 'sim' then return {sim={properties={['operator-code']='45006'}}} end
    return {}
end} end
package.preload['a8_apn.model']=function() return model end
local open, popen = io.open, io.popen
io.open=function() return nil end
io.popen=function() error('modem query while settings are staged') end
local service=dofile(arg[1]:gsub('model.lua$', 'service.lua'))
for _, config in ipairs({'network','a8_apn'}) do
    pending_config=config
    assert(service.apply().state == 'pending-changes')
end
-- Feed mmcli responses through the actual service, including its lock parser.
pending_config=nil
settings={enabled='1', mode='profile', interface='modem', profile='travel'}
io.popen=function(command)
    local response
    if command:find('--modem=qcom-soc', 1, true) then response='modem'
    else
        assert(command:find('--sim=/org/freedesktop/ModemManager1/SIM/0', 1, true))
        response='sim'
    end
    return {read=function() return response end, close=function() end}
end
for _, value in ipairs({false, 'none', 'unknown', 'sim-pin2', 'sim-puk2'}) do
    lock=value or nil
    assert(service.inspect().state == 'ready')
end
for _, value in ipairs({'sim-pin', 'sim-puk', 'ph-net-pin'}) do
    lock=value
    assert(service.inspect().state == 'sim-locked')
end
lock='sim-pin2'
local status=service.inspect()
assert(#status.radio_modes == 4 and status.current_radio_mode == 'lte-preferred')
settings.radio_mode='lte-only'
assert(service.inspect().state == 'ready')
settings.radio_mode='auto' -- This modem does not advertise 3G+4G without preference.
assert(service.inspect().state == 'unsupported-radio-mode')
reported_modes={}
settings.radio_mode='lte-only'
assert(service.inspect().state == 'unsupported-radio-mode')
io.open, io.popen = open, popen
print('APN selection, manual mode, roaming, credentials and config writes passed')
