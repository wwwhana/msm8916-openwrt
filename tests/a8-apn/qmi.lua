local qmi=dofile(arg[1])
local function le16(n) return string.char(n%256,math.floor(n/256)) end
local function tlv(t,s) return string.char(t)..le16(#s)..s end
local function response(svc,cid,tx,msg,values)
    local h=svc==0 and string.char(1,tx) or string.char(2)..le16(tx)
    local q=h..le16(msg)..le16(#values)..values
    return '\1'..le16(#q+5)..string.char(128,svc,cid)..q
end
local success=tlv(2,'\0\0\0\0')
for _,failure in ipairs({false,true}) do
    local input=response(0,0,1,0xff00,success)..
        response(0,0,2,0x22,success..tlv(1,string.char(0x12,7)))
    if failure then
        input=input..response(0x12,7,3,0x26,tlv(2,'\1\0\3\0'))..
            response(0,0,4,0x23,success)
    else
        input=input..response(0x12,7,3,0x26,success..tlv(0x13,'\0'))..
            response(0x12,7,4,0x27,success..tlv(0x12,'\1'))..
            response(0x12,7,5,0x37,success..tlv(0x13,'\1'))..
            response(0,0,6,0x23,success)
    end
    local sent,closed='',false
    package.loaded.nixio={socket=function(domain,kind)
        assert(domain=='unix' and kind=='stream')
        return {setopt=function() return true end,
            connect=function(_,path) assert(path=='\0qmi-proxy'); return true end,
            send=function(_,s) local n=math.min(#s,3); sent=sent..s:sub(1,n); return n end,
            recv=function(_,n)
                assert(#input>0); n=math.min(n,2)
                local s=input:sub(1,n); input=input:sub(n+1); return s
            end,
            close=function() closed=true end}
    end}
    local result=qmi.legacy_settings('/dev/wwan0qmi0')
    assert(closed and #input==0, 'must release CID after success and failure')
    if failure then assert(result.state=='error')
    else assert(result.state=='available' and result.ims=='yes' and result.sms=='yes' and result.voice=='yes') end
    -- The only modem messages allowed here are reads and client management.
    local expected=failure and {0xff00,0x22,0x26,0x23} or {0xff00,0x22,0x26,0x27,0x37,0x23}
    local i=1
    for _,msg in ipairs(expected) do
        local len=sent:byte(i+1)+256*sent:byte(i+2)
        local offset=sent:byte(i+4)==0 and i+8 or i+9
        assert(sent:byte(offset)+256*sent:byte(offset+1)==msg)
        i=i+len+1
    end
    assert(i==#sent+1)
end
local disabled=qmi.decode({[0x13]='\1'},{[0x12]='\0'},{[0x13]='\0'})
assert(disabled.ims=='no' and disabled.sms=='no' and disabled.voice=='no')
assert(qmi.decode({}, {}, {}).ims==nil)
print('Legacy IMS reads, fragmented frames, flags and CID cleanup passed')
