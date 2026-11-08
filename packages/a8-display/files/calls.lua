-- SPDX-License-Identifier: GPL-2.0-only
local M = {}
local active={dialing=true,ringing=true,early_media=true,answering=true,confirmed=true,active=true,ending=true}
local last_peer,last_name
local function contact_name(peer)
    if peer==last_peer then return last_name end
    last_peer,last_name=peer,nil
    if type(peer)~='string' or #peer>22 or not peer:match('^%+?%d+$') then return end
    peer=peer:gsub('^0','+82')
    local p=io.popen('timeout 2 sqlite3 -readonly -batch -noheader -cmd ".timeout 500" ' ..
        '/etc/modemconfig-ko/messages.sqlite3 "SELECT c.name FROM contacts c JOIN contact_addresses a ON a.contact_id=c.id ' ..
        "WHERE a.address='"..peer.."' LIMIT 1;\" 2>/dev/null",'r')
    if p then last_name=p:read(256); p:close() end
    last_name=last_name and last_name:gsub('[\r\n]+$','')
    return last_name
end
function M.read(now)
    local f=io.open('/var/run/a8-call.json','r')
    if not f then return end
    local data=f:read(4097); f:close()
    if not data or #data>4096 then return end
    local ok,call=pcall(require('luci.jsonc').parse,data)
    if not ok or type(call)~='table' or not active[call.state] or type(call.updated_at)~='number' or
       now-call.updated_at<0 or now-call.updated_at>3 or type(call.pid)~='number' or call.pid<1 or call.pid%1~=0 then return end
    f=io.open('/proc/'..string.format('%.0f',call.pid)..'/cmdline','r')
    if not f then return end
    local command=f:read(256); f:close()
    if not command or not command:find('/usr/sbin/modemconfig-ko\0',1,true) then return end
    call.peer=type(call.peer)=='string' and call.peer:sub(1,64) or ''
    call.duration=type(call.duration)=='number' and math.min(359999,math.max(0,math.floor(call.duration))) or 0
    call.name=contact_name(call.peer)
    return call
end
function M.active(call) return type(call)=='table' and active[call.state] or false end
return M
