-- SPDX-License-Identifier: GPL-2.0-only
local M = {}
local json=require('luci.jsonc')
local timestamp='strftime("%m-%d %H:%M",coalesce(m.received_at,m.sent_at,m.created_at),"localtime") AS timestamp'
local function sequence(value)
    value=tostring(value or '')
    if value:match('^[1-9]%d*$') and #value<=16 and tonumber(value)<=9007199254740991 then return value end
end
local function query(sql)
    -- The LCD only reads the same archive used by the web inbox.
    local p=io.popen("timeout 3 sqlite3 -readonly -batch -json -cmd '.timeout 1000' " ..
        "/etc/modemconfig-ko/messages.sqlite3 '" .. sql .. "' 2>/dev/null; printf '\\nA8_SQL:%s\\n' \"$?\"",'r')
    if not p then return end
    local data=p:read(262145); local ok=p:close()
    if not ok or not data or #data>262144 then return end
    -- Lua 5.1 pclose does not expose the child's exit status.
    local payload,exit=data:match('^(.*)\nA8_SQL:(%d+)\n$')
    if exit~='0' then return end
    if payload=='' then return {} end
    local parsed,rows=pcall(json.parse,payload)
    if parsed and type(rows)=='table' then return rows end
end
function M.list(before)
    local filter=''
    if before then
        before=sequence(before)
        if not before then return false,'Invalid message' end
        filter=' AND m.seq<'..before
    end
    local rows=query('SELECT m.seq,m.sender,' .. timestamp .. ',' ..
        '(SELECT count(*) FROM message_parts p WHERE p.message_id=m.id AND p.mime_type LIKE "image/%") AS photos ' ..
        "FROM messages m WHERE m.direction=\"incoming\" AND m.protocol IN(\"sms\",\"mms\")" ..
        filter .. ' ORDER BY m.seq DESC LIMIT 21;')
    if not rows then return false,'SMS unavailable' end
    local entries={}
    for i=1,math.min(20,#rows) do
        local r=rows[i]; local id=sequence(r.seq)
        if id then entries[#entries+1]={id='sms-open',profile=id,label=tostring(r.sender or 'Unknown'),
            detail=tostring(r.timestamp or '')..((tonumber(r.photos) or 0)>0 and ' IMG' or '')} end
    end
    if #rows>20 and #entries>0 then
        entries[#entries+1]={id='sms-older',profile=entries[#entries].profile,label='Older messages'}
    end
    if #entries==0 then entries[1]={label='No messages',info=true} end
    return true,entries
end
function M.read(id)
    id=sequence(id)
    if not id then return false,'Invalid message' end
    local rows=query('SELECT m.sender,m.body,m.subject,m.protocol,' ..
        timestamp .. ',' ..
        '(SELECT count(*) FROM message_parts p WHERE p.message_id=m.id AND p.mime_type NOT IN("text/plain","application/smil","application/smil+xml")) AS attachments ' ..
        'FROM messages m WHERE m.seq='..id..' AND m.direction="incoming" AND m.protocol IN("sms","mms");')
    if not rows then return false,'SMS unavailable' end
    local r=rows[1]
    if not r then return false,'Message removed' end
    local images={}
    local attachments=tonumber(r.attachments) or 0
    if attachments>0 then
        local parts=query('SELECT p.id,p.mime_type FROM message_parts p JOIN messages m ON m.id=p.message_id ' ..
            'WHERE m.seq='..id..' AND p.mime_type LIKE "image/%" ORDER BY p.position;')
        if not parts then return false,'Images unavailable' end
        for _,part in ipairs(parts) do
            if type(part.id)=='string' and #part.id==64 and part.id:match('^[0-9a-f]+$') then
                images[#images+1]={id=part.id}
            end
        end
    end
    local body=tostring(r.body or '')
    if type(r.subject)=='string' and r.subject~='' and r.subject~='[제목없음]' then body=r.subject..'\n'..body end
    if attachments>#images then body=body..'\n[Other files: '..(attachments-#images)..']' end
    if body=='' and #images==0 then body='(No text)' end
    return true,{sender=tostring(r.sender or 'Unknown'),timestamp=tostring(r.timestamp or ''),body=body,images=images}
end
function M.thumbnail(id)
    if type(id)~='string' or #id~=64 or not id:match('^[0-9a-f]+$') then return end
    local p=io.popen('timeout 6 /usr/libexec/a8-image '..id..' 2>/dev/null','r')
    if not p then return end
    local image=p:read(26895); p:close()
    if image and #image==26894 and image:sub(1,14)=='P6\n112 80\n255\n' then return image end
end
return M
