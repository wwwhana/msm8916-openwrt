local files=arg[1]
local actual_io=io
local value={state='active',pid=123,updated_at=100,peer='+821000000001',duration=65,audio='browser'}
package.loaded['luci.jsonc']={parse=function() return value end}
local command='/usr/sbin/modemconfig-ko\0run\0android-carrier'
local queried=0
io={open=function(path)
    local text=path=='/var/run/a8-call.json' and '{}' or path=='/proc/123/cmdline' and command
    if not text then return end
    return {read=function() return text end,close=function() end}
end,popen=function(sql)
    queried=queried+1
    assert(sql:find("WHERE a.address='+821000000001'",1,true))
    return {read=function() return '가족\n' end,close=function() end}
end}
local calls=dofile(files..'/calls.lua')
local call=calls.read(101)
assert(call and call.name=='가족' and call.duration==65 and queried==1)
calls.read(102); assert(queried==1)
assert(not calls.read(104))
command='/usr/sbin/other\0'; assert(not calls.read(101)); command='/usr/sbin/modemconfig-ko\0run\0'
value.state='ended'; assert(not calls.read(101))
value.state='ringing'; value.pid='123'; assert(not calls.read(101))
io=actual_io
print('Private live-call cache, stale/dead process rejection and shared contact lookup passed')
