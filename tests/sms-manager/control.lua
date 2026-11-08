local script = 'packages/luci-app-sms-manager/root/usr/libexec/modemconfig-ko-control'
local original_print, original_stdin, original_arg = print, io.stdin, arg
local input, response, written, emitted, closed, connect_ok, send_ok, receive_ok
package.preload['luci.jsonc'] = function()
    return {
        parse=function(body) if body == '{"ready":true}' then return {ready=true} end end,
        stringify=function(value) return value.ready and 'ready' or value.error end
    }
end
package.preload.nixio = function()
    return {socket=function(domain,kind)
        assert(domain=='unix' and kind=='stream')
        return {
            setopt=function() return true end,
            connect=function(_,path) assert(path=='/var/run/modemconfig-ko.sock'); return connect_ok end,
            send=function(_,data)
                if not send_ok then return 0 end
                local n=math.min(3,#data);written=written..data:sub(1,n);return n
            end,
            recv=function()
                if not receive_ok then return nil end
                local part=response:sub(1,7);response=response:sub(8);return part
            end,
            close=function() closed=closed+1 end
        }
    end}
end
local function run(options)
    options=options or {}; input=options.input or '{}';written='';emitted=nil;closed=0
    response=options.response or 'HTTP/1.0 200 OK\r\nContent-Length: 14\r\n\r\n{"ready":true}'
    connect_ok=not options.no_connect;send_ok=not options.no_send;receive_ok=not options.no_receive
    io.stdin={read=function(_,limit) return input:sub(1,limit) end}
    arg={[1]=options.method or 'voice_audio'};print=function(value) emitted=value end
    dofile(script)
    print,io.stdin,arg=original_print,original_stdin,original_arg
    return emitted
end
assert(run()=='ready' and closed==1)
assert(written=='POST /voice_audio HTTP/1.0\r\nContent-Type: application/json\r\nContent-Length: 2\r\n\r\n{}','partial socket writes changed the request')
assert(run({no_connect=true})=='service-unavailable' and closed==1)
assert(run({no_send=true})=='service-unavailable' and closed==1)
assert(run({no_receive=true})=='service-unavailable' and closed==1)
assert(run({response='HTTP/1.0 200 OK\r\nContent-Length: 99\r\n\r\n{"ready":true}'})=='service-unavailable')
assert(run({response='HTTP/1.0 200 OK\r\n\r\ninvalid'})=='service-unavailable')
assert(run({input=string.rep('x',16385)})=='service-unavailable' and closed==0)
assert(run({method='arbitrary'})=='service-unavailable' and closed==0)
print('Voice control socket: partial reads/writes, limits, method and failure checks passed')
