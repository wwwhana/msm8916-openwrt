-- SPDX-License-Identifier: GPL-2.0-only
-- Read the original IMSS v1 settings on MSM8916. No setting or NV writes.
local M = {}
local function le16(n) return string.char(n % 256, math.floor(n / 256)) end
local function u16(s, p) local a,b=s:byte(p,p+1); assert(b, 'short integer'); return a+b*256 end
local function tlv(t, s) return string.char(t)..le16(#s)..s end

function M.legacy_settings(device)
    local socket, cid, transaction, release
    local function run()
        local nixio = require('nixio')
        socket = assert(nixio.socket('unix', 'stream'))
        assert(socket:setopt('socket', 'rcvtimeo', 2))
        assert(socket:setopt('socket', 'sndtimeo', 2))
        assert(socket:connect('\0qmi-proxy'))
        transaction = 0
        local deadline = os.time()+6
        local function read(n)
            local parts, have = {}, 0
            while have < n do
                assert(os.time() <= deadline, 'QMI deadline')
                local s = assert(socket:recv(n-have))
                assert(#s > 0, 'proxy closed')
                parts[#parts+1], have = s, have+#s
            end
            return table.concat(parts)
        end
        local function call(service, client, message, values)
            assert(os.time() <= deadline, 'QMI deadline')
            transaction = transaction%250+1
            values = values or ''
            local header = service == 0 and string.char(0, transaction) or '\0'..le16(transaction)
            local qmi = header..le16(message)..le16(#values)..values
            local frame = '\1'..le16(#qmi+5)..string.char(0, service, client)..qmi
            local sent = 0
            while sent < #frame do
                local n = assert(socket:send(frame:sub(sent+1)))
                assert(n > 0, 'short send'); sent=sent+n
            end
            for _=1,16 do
                local first = read(3)
                local length = u16(first, 2)
                assert(first:byte(1) == 1 and length >= 11 and length <= 8192, 'bad QMUX frame')
                local data = first..read(length-2)
                local svc, cl, flags = data:byte(5,7)
                local offset = svc == 0 and 9 or 10
                local tx = svc == 0 and data:byte(8) or u16(data,8)
                if svc == service and cl == client and tx == transaction and
                    flags == (svc == 0 and 1 or 2) and u16(data,offset) == message then
                    local p, result = offset+4, {}
                    assert(p+u16(data,offset+2) == #data+1, 'bad QMI length')
                    while p <= #data do
                        local t, len = data:byte(p), u16(data,p+1); p=p+3
                        assert(p+len <= #data+1 and not result[t], 'bad QMI TLV')
                        result[t] = data:sub(p,p+len-1); p=p+len
                    end
                    assert(result[2] and #result[2] == 4 and u16(result[2],1) == 0, 'QMI request failed')
                    return result
                end
            end
            error('QMI response missing')
        end
        -- Keep this closure for CID release on both normal and error exits.
        release = function()
            deadline=os.time()+2
            return call(0,0,0x23,tlv(1,string.char(0x12,cid)))
        end
        call(0,0,0xff00,tlv(1,device))
        local allocation = call(0,0,0x22,tlv(1,string.char(0x12)))[1]
        assert(allocation and #allocation == 2 and allocation:byte(1) == 0x12, 'bad CID')
        cid = allocation:byte(2)
        return M.decode(call(0x12,cid,0x26),call(0x12,cid,0x27),call(0x12,cid,0x37))
    end
    local ok, result = pcall(run)
    if cid and release then pcall(release) end
    if socket then socket:close() end
    return ok and result or {state='error'}
end

function M.decode(reg, sms, voice)
    local function boolean(s, invert)
        if not s or #s ~= 1 or s:byte() > 1 then return nil end
        return (s:byte() == (invert and 0 or 1)) and 'yes' or 'no'
    end
    return {state='available', source='legacy-v1', ims=boolean(reg[0x13],true),
        sms=boolean(sms[0x12]), voice=boolean(voice[0x13])}
end

return M
