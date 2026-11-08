local files,sql_output=arg[1],arg[2]
package.loaded['a8_display.settings']=dofile(files..'/settings.lua')
local letters=dofile(files..'/font.lua')
package.loaded['a8_display.font']=letters
local chars={}
for char,code in letters.chars('A한글😀') do chars[#chars+1]={char,code} end
assert(#chars==4 and chars[2][2]==0xd55c and chars[4][2]==0x1f600)
for _,bad in ipairs({'\255','\192\175','\237\160\128','\244\144\128\128','\226\130'}) do
    for _,code in letters.chars(bad) do assert(code==63) end
end
local wrapped=letters.wrap('가나다라마바사아A\r\n\nZ',112)
assert(wrapped[1]=='가나다라마바사' and wrapped[2]=='아A' and wrapped[3]=='' and wrapped[4]=='Z')
assert(#letters.pages(string.rep('가',36))==2)
local original_open=io.open
io.open=function(path,mode)
    return original_open(path=='/usr/share/a8-display/unifont.bin' and files..'/unifont.bin' or path,mode)
end
local function glyph(value)
    local pixels={}
    letters.draw(value,0,0,'x',function(x,y) pixels[y*128+x]=true end)
    local result={}
    for i=0,16*128 do if pixels[i] then result[#result+1]=i end end
    return table.concat(result,',')
end
assert(glyph('가')~=glyph('나') and glyph('한')~=glyph('글'))
assert(glyph('A')~=glyph('a') and glyph('😀')~='')
io.open=original_open

local rows,failed,invalid,commands={},false,false,{}
local child_status,child_output=0,'ROWS'
local part_rows={}
local current_rows
package.loaded['luci.jsonc']={parse=function(data)
    assert(data=='ROWS')
    if invalid then error('invalid JSON') end
    return current_rows
end}
local original_popen=io.popen
io.popen=function(command)
    assert(command:find('sqlite3 -readonly -batch -json',1,true))
    assert(not command:find('UPDATE',1,true) and not command:find('DELETE',1,true))
    commands[#commands+1]=command
    current_rows=command:find('SELECT p.id',1,true) and part_rows or rows
    return {read=function(_,count) assert(count==262145); return child_output..'\nA8_SQL:'..child_status..'\n' end,
        close=function() return not failed end}
end
local messages=dofile(files..'/messages.lua')
local ok,entries=messages.list()
assert(ok and entries[1].info and entries[1].label=='No messages')
rows={}
for i=1,21 do rows[i]={seq=22-i,sender='114',timestamp='11-07 21:30',photos=i==1 and 1 or 0} end
ok,entries=messages.list()
assert(ok and #entries==21 and entries[1].profile=='21' and entries[20].profile=='2')
assert(entries[1].detail=='11-07 21:30 IMG' and entries[21].id=='sms-older' and entries[21].profile=='2')
assert(messages.list('2'))
rows={{sender='114',body='한글 본문\n둘째 줄',timestamp='11-07 21:30',attachments=0}}
local message
ok,message=messages.read('21')
assert(ok and message.body=='한글 본문\n둘째 줄')
local n=#commands
for _,bad in ipairs({'0','-1','1;reboot',"1'",'1.5','nan','9007199254740992',{},'../1'}) do
    assert(not messages.read(bad) and not messages.list(bad))
end
assert(#commands==n)
rows={}; assert(not messages.read('21'))
child_output=''; local empty_ok,empty_entries=messages.list(); assert(empty_ok and empty_entries[1].label=='No messages')
child_status=1; assert(not messages.list()); child_status=124; assert(not messages.list())
child_status=0; child_output='ROWS'
failed=true; assert(not messages.list()); failed=false
invalid=true; assert(not messages.read('21')); invalid=false
rows={{body='',attachments=1,subject='알림'}}
ok,message=messages.read('21'); assert(ok and message.body:find('알림',1,true) and message.body:find('Other files: 1',1,true))
part_rows={{id=string.rep('a',64),mime_type='image/png'},{id='../invalid',mime_type='image/png'}}
rows={{body='',attachments=1}}
ok,message=messages.read('21');assert(ok and #message.images==1 and message.body=='')
local photo=message
local preview='P6\n112 80\n255\n'..string.rep(string.char(255,0,0),112*80)
io.popen=function(command)
    assert(command=='timeout 6 /usr/libexec/a8-image '..string.rep('a',64)..' 2>/dev/null')
    return {read=function(_,count) assert(count==26895);return preview end,close=function() return true end}
end
assert(messages.thumbnail(string.rep('a',64))==preview)
assert(not messages.thumbnail('../image'))
local real_preview=preview
preview='invalid';assert(not messages.thumbnail(string.rep('a',64)));preview=real_preview
io.popen=original_popen
if sql_output then
    local f=assert(original_open(sql_output,'w'))
    for _,command in ipairs(commands) do f:write(command,'\n') end
    f:close()
end

local model=dofile(files..'/model.lua')
local s=model.new(); model.event(s,'connected'); model.event(s,'short')
model.event(s,'short'); model.event(s,'short')
local action=model.event(s,'long'); assert(action.id=='sms-list')
model.result(s,true,'Please wait'); model.inbox(s,entries)
assert(s.page=='sms' and #s.parents==1)
action=model.event(s,'long'); assert(action.id=='sms-open' and action.profile=='21')
model.result(s,true,'Please wait')
model.read_sms(s,{sender='114',timestamp='',body=string.rep('한',36)})
assert(s.page=='sms-text' and s.text_page==1 and #s.parents==2)
model.event(s,'short'); assert(s.text_page==2)
local draw=dofile(files..'/dashboard.lua').draw
local page2=draw({},100,s)
model.event(s,'short'); assert(s.text_page==1 and draw({},100,s)~=page2)
model.pairing(s,{id='interrupt',device='Phone',code='000042'})
assert(s.page=='pairing' and s.selection==1)
model.pairing(s,nil); assert(s.page=='sms-text' and s.text_page==1)
model.event(s,'long'); assert(s.page=='sms' and s.selection==1)
for _=1,#s.entries-1 do model.event(s,'short') end
model.event(s,'long'); assert(s.page=='menu' and s.selection==3)
-- Older pages keep a separate Back destination and never lose the inbox.
model.inbox(s,entries)
for _=1,20 do model.event(s,'short') end
action=model.event(s,'long'); assert(action.id=='sms-list' and action.profile=='2')
model.result(s,true,'Please wait'); model.inbox(s,{{label='No messages',info=true}})
model.event(s,'short'); model.event(s,'long')
assert(s.page=='sms' and s.selection==21)
model.read_sms(s,photo)
assert(#s.sms.pages==1 and s.sms.pages[1].image==string.rep('a',64))
model.image(s,preview)
local screen=draw({},100,s)
local offset=16+(28*128+8)*3
assert(screen:sub(offset,offset+2)==string.char(255,0,0))
model.image(s,nil);assert(draw({},100,s)~=screen)
print('Read-only SMS queries, inbox pagination, Korean glyphs, Unicode wrapping and safe navigation passed')
