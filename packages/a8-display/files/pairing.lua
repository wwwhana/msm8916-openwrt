-- SPDX-License-Identifier: GPL-2.0-only
local M={}
local json=require('luci.jsonc')
M.request='/var/run/bt-pairing.json'
M.reply='/var/run/bt-pairing.reply'
function M.read(path)
    local f=io.open(path,'r')
    if not f then return end
    local ok,value=pcall(json.parse,f:read('*a')); f:close()
    if ok and type(value) == 'table' then return value end
end
function M.write(path,value)
    local f=io.open(path .. '.new','w')
    if not f then return false end
    f:write(json.stringify(value)); f:close()
    return os.rename(path .. '.new',path)
end
function M.pending()
    local r=M.read(M.request)
    if r and type(r.id) == 'string' and type(r.expires) == 'number' and r.expires>os.time() then return r end
end
function M.respond(id,accept)
    local r=M.pending()
    if not r or r.id ~= id then return false,'Request expired' end
    local ok=M.write(M.reply,{id=id,accept=accept == true})
    return ok,ok and (accept and 'Approval sent' or 'Pairing rejected') or 'Response failed'
end
function M.connected(address)
    if type(address) ~= 'string' or not address:match('^%x%x:%x%x:%x%x:%x%x:%x%x:%x%x$') then return end
    local f=io.popen('timeout 2 bluetoothctl --timeout 1 info ' .. address .. ' 2>/dev/null','r')
    if not f then return end
    local text=f:read('*a'); f:close()
    local value=text:match('Connected:%s*(%a+)')
    if value == 'yes' then return true end
    if value == 'no' or text:find('not available',1,true) then return false end
end
function M.parse(text)
    local prompt=text:match('(Confirm passkey: %d+ %(yes/no%)%?%s*)$') or
        text:match('(Confirm pin code: [^%s]+ %(yes/no%)%?%s*)$') or
        text:match('(Authorize this device pairing %(yes/no%)%?%s*)$')
    if not prompt then return end
    local before=text:sub(1,#text-#prompt)
    local address=before:match('%((%x%x:%x%x:%x%x:%x%x:%x%x:%x%x)%)%s*$')
    if not address then return end
    local name
    for value in before:gmatch('Device: ([^\n]*)') do name=value end
    name=(name or address):gsub(' %(%x%x:%x%x:%x%x:%x%x:%x%x:%x%x%)%s*$','')
    local code=prompt:match('Confirm passkey: (%d+)') or prompt:match('Confirm pin code: ([^%s]+)')
    return {device=name,address=address,code=code and (tonumber(code) and string.format('%06d',tonumber(code)) or code) or ''}
end
return M
