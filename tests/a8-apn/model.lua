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
values.proto='qmi'
p, reason = model.apply(cursor, 'modem', lgu, auto)
assert(p == nil and reason == 'wrong-interface')
values.proto='modemmanager'
function cursor:commit() return false end
p, reason = model.apply(cursor, 'modem', lgu, auto)
assert(p == nil and reason == 'commit-failed')
print('APN selection, manual mode, roaming, credentials and config writes passed')
