local files=arg[1]
local memory,objects={},{}
local now=1000
local open,rename,time,remove,exit=io.open,os.rename,os.time,os.remove,os.exit
local function encode(value) local key='json' .. (#objects+1); objects[#objects+1]=value; return key end
local function decode(key) local index=tonumber((key or ''):match('^json(%d+)$')); return index and objects[index] end
io.open=function(path,mode)
    if mode == 'r' and not memory[path] then return nil end
    return {read=function() return memory[path] end,
        write=function(_,value) memory[path]=tostring(value) end,close=function() end}
end
os.rename=function(a,b) memory[b]=memory[a]; memory[a]=nil; return true end
os.remove=function(path) memory[path]=nil; return true end
os.time=function() return now end
package.preload['luci.jsonc']=function() return {parse=decode,stringify=encode} end
local p=dofile(files .. '/pairing.lua')
package.loaded['a8_display.pairing']=p
local text='Agent registered\nDevice: My Phone (00:11:22:33:44:55)\nConfirm passkey: 42 (yes/no)? '
local request=assert(p.parse(text)); assert(request.code == '000042' and request.device == 'My Phone')
assert(not p.parse('incomplete Confirm passkey: 4 (yes/no)?'))
local last=p.parse('Device: Old (AA:BB:CC:DD:EE:FF)\n' .. text)
assert(last.address == '00:11:22:33:44:55' and last.device == 'My Phone')
local spoof=p.parse('Device: Confirm passkey: 111111 (yes/no)? (00:11:22:33:44:55)\nConfirm passkey: 42 (yes/no)? ')
assert(spoof.code == '000042')
local popen=io.popen
local peer_text=''
io.popen=function(command)
    assert(command == 'timeout 2 bluetoothctl --timeout 1 info 00:11:22:33:44:55 2>/dev/null')
    return {read=function() return peer_text end,close=function() end}
end
assert(p.connected('00:11:22:33:44:55;reboot') == nil)
for text,want in pairs({['Connected: yes']=true,['Connected: no']=false,['Device not available']=false}) do
    peer_text=text; assert(p.connected('00:11:22:33:44:55') == want)
end
peer_text=''; assert(p.connected('00:11:22:33:44:55') == nil)
io.popen=popen
request.id='one';request.expires=1100
assert(p.write(p.request,request)); assert(p.pending().id == 'one')
assert(not p.respond('old',true)); assert(p.respond('one',false)); assert(p.read(p.reply).accept == false)
now=1101; assert(not p.pending()); assert(not p.respond('one',true))

local connected=p.connected
for _,choice in ipairs({'yes','no','timeout','stale','disconnected'}) do
    memory={}; now=2000
    p.connected=function() return choice ~= 'disconnected' end
    local parts={text:sub(1,40),text:sub(41)}
    local writes,killed={},false
    local iterations=0
    package.loaded.nixio={
        pipe=function()
            return {close=function() end,setblocking=function() end,read=function()
                if #parts>0 then return table.remove(parts,1) end
                if #writes>0 then return '' end
            end}, {close=function() end,write=function(_,value) writes[#writes+1]=value; return #value end}
        end,
        fork=function() return 123 end,getpid=function() return 456 end,signal=function() end,
        poll_flags=function() return 1 end,
        poll=function()
            iterations=iterations+1; assert(iterations<12)
            now=now+10
            local r=p.pending()
            if r and choice ~= 'timeout' and choice ~= 'disconnected' then
                p.write(p.reply,{id=choice == 'stale' and 'old' or r.id,accept=choice == 'yes'})
            end
        end,
        kill=function(pid,sig) assert(pid==123 and sig==15);killed=true end,
        waitpid=function(pid) assert(pid==123) end
    }
    os.exit=function(code) assert(code==1); error('expected exit') end
    local ok,err=pcall(dofile,files .. '/bt-pairing')
    assert(not ok and tostring(err):find('expected exit',1,true),err)
    assert(writes[1] == (choice == 'yes' and 'yes\n' or 'no\n'))
    assert(killed and not memory[p.request] and not memory[p.reply])
end
p.connected=connected
io.open=open;os.rename=rename;os.time=time;os.remove=remove;os.exit=exit
print('Bluetooth confirmation parser, reply matching, rejection and timeout passed')
