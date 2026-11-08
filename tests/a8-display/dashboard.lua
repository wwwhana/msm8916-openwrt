local files=arg[1]
package.loaded['a8_display.settings']=dofile(files..'/settings.lua')
package.loaded['a8_display.font']=dofile(files..'/font.lua')
local dashboard=dofile(files .. '/dashboard.lua')
local status={[4]='LTE  LG U+ ||||-', [6]='IP   192.168.8.1',
    battery={capacity=75,charging=false},
    dashboard={wifi=true,wifi_clients=3,bluetooth='yes',traffic={rx=12000000,tx=3000000}}}
local function draw(s,now)
    local image=dashboard.draw(s,now or 100)
    assert(image:sub(1,15)=='P6\n128 128\n255\n' and #image==15+128*128*3)
    return image
end
local normal=draw(status)
assert(normal==draw(status))
assert(normal~=draw(status,160))
local function blue_signal(image)
    local count=0
    for y=42,84 do for x=74,116 do
        local pixel=image:sub(16+(y*128+x)*3,18+(y*128+x)*3)
        if pixel==string.char(55,144,255) then count=count+1 end
    end end
    return count
end
assert(blue_signal(normal)>100)
status[4]='LTE  LG U+ -----'; assert(blue_signal(draw(status))==0)
status[4]='SIM NONE'; assert(blue_signal(draw(status))==0)
status[4]='LTE  LG U+ ||||-'
status.dashboard.traffic=nil; local unknown=draw(status)
status.dashboard.traffic={rx=0,tx=0}; assert(draw(status)~=unknown)
for _,bad in ipairs({{rx=-1,tx=0},{rx='12',tx=0},{rx=0/0,tx=0},{rx=math.huge,tx=0}}) do
    status.dashboard.traffic=bad; assert(draw(status)==unknown)
end
for _,cap in ipairs({0,20,21,80,81,100}) do
    status.battery={capacity=cap,charging=true}
    assert(draw(status,100)~=draw(status,101))
    status.battery.charging=false
    assert(draw(status,100)==draw(status,101))
end
draw({}); draw({dashboard='invalid'}); draw({dashboard={traffic='invalid'}})

-- Cached dashboard data reaches the same status model as the text screen.
local open=io.open
package.loaded['luci.jsonc']={parse=function(value)
    assert(value=='SNAPSHOT')
    return {wifi=true,wifi_clients=2,traffic={rx=100,tx=20}}
end}
io.open=function(path)
    local value=path=='/var/run/a8-display.status' and '\27[HOpenWrt\r\n\r\nBATT 075% CHG' or
                path=='/var/run/a8-connections.json' and 'SNAPSHOT'
    assert(value,path)
    return {read=function() return value end,close=function() end}
end
local service=dofile(files .. '/service.lua')
local cached=service.status()
io.open=open
assert(cached.battery.capacity==75 and cached.battery.charging)
assert(cached.dashboard.wifi_clients==2 and cached.dashboard.traffic.rx==100)

local model=dofile(files .. '/model.lua')
local s=model.new(); assert(s.page=='status')
model.event(s,'connected'); model.event(s,'short'); assert(s.page=='menu')
model.event(s,'long'); assert(s.page=='status')
model.event(s,'short'); model.event(s,'long'); assert(s.page=='status')
for _=1,30 do model.tick(s) end
assert(s.saver and model.activity(s,'pressed') and not s.saver and s.page=='status')
model.activity(s,'released:100')
model.pairing(s,{id='test',device='Phone',code='123456'})
assert(s.page=='pairing' and s.selection==1)
model.pairing(s,nil); assert(s.page=='status')
-- Every menu family uses a full card frame and keeps selection visible.
for _,page in ipairs({'network','vpn','usb','bt','sms','power','priority','profiles','display','screensaver_timeout','screen_off_timeout'}) do
    local entries={}
    for i=1,8 do entries[i]={id=i==8 and 'back' or page,label=page..' '..i} end
    local state={page=page,selection=1,entries=entries,mode='power'}
    local first=dashboard.draw(status,100,state)
    assert(#first==49167)
    state.selection=8
    assert(dashboard.draw(status,100,state)~=first)
end
local root={page='menu',selection=1,entries=s.entries,mode='power'}
local first=dashboard.draw(status,100,root)
root.selection=5
local second=dashboard.draw(status,100,root)
assert(#second==49167 and second~=first)
root.selection=1; assert(dashboard.draw(status,100,root)==first)
for _,state in ipairs({
    {page='menu',selection=1,entries=s.entries},
    {page='pairing',selection=1,pairing={device='Phone',code='000042'}},
    {page='confirm',selection=1,pending={label='Power off'}},
    {page='message',message='Saved'}
}) do
    local first=dashboard.draw(status,100,state)
    assert(#first==49167)
    if state.page~='message' then state.selection=2; assert(dashboard.draw(status,100,state)~=first) end
end
print('Dashboard pixels, live-state validation, text fallback, menu, clock and pairing transitions passed')

local call={state='ringing',direction='incoming',peer='01000000001',duration=0,audio='browser'}
status.call=call
local ringing=dashboard.draw(status,100,{page='status'})
call.state='active'; call.duration=65
local talking=dashboard.draw(status,100,{page='status'})
assert(#talking==49167 and ringing~=talking)
call.duration=66; assert(dashboard.draw(status,100,{page='status'})~=talking)
local s=model.new(); model.event(s,'connected'); s.sleeping=true; s.saver=true
model.call(s,call); assert(not s.sleeping and not s.saver)
for _=1,400 do model.tick(s) end
assert(not s.sleeping and model.activity(s,'long'))
model.call(s,nil); model.tick(s); assert(s.idle==1)
print('Incoming-call wake, talk timer and call-end sleep reset passed')
