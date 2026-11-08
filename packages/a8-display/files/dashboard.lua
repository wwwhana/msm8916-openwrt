-- SPDX-License-Identifier: GPL-2.0-only
-- Native 128x128 view. Data comes from the existing background status worker.
local M = {}
local font={
    ['0']={0x3e,0x51,0x49,0x45,0x3e}, ['1']={0,0x42,0x7f,0x40,0},
    ['2']={0x42,0x61,0x51,0x49,0x46}, ['3']={0x21,0x41,0x45,0x4b,0x31},
    ['4']={0x18,0x14,0x12,0x7f,0x10}, ['5']={0x27,0x45,0x45,0x45,0x39},
    ['6']={0x3c,0x4a,0x49,0x49,0x30}, ['7']={1,0x71,9,5,3},
    ['8']={0x36,0x49,0x49,0x49,0x36}, ['9']={6,0x49,0x49,0x29,0x1e},
    A={0x7e,0x11,0x11,0x11,0x7e}, B={0x7f,0x49,0x49,0x49,0x36},
    C={0x3e,0x41,0x41,0x41,0x22}, D={0x7f,0x41,0x41,0x22,0x1c},
    E={0x7f,0x49,0x49,0x49,0x41}, F={0x7f,9,9,9,1},
    G={0x3e,0x41,0x49,0x49,0x7a}, H={0x7f,8,8,8,0x7f},
    I={0,0x41,0x7f,0x41,0}, J={0x20,0x40,0x41,0x3f,1},
    K={0x7f,8,0x14,0x22,0x41}, L={0x7f,0x40,0x40,0x40,0x40},
    M={0x7f,2,0x0c,2,0x7f}, N={0x7f,4,8,0x10,0x7f},
    O={0x3e,0x41,0x41,0x41,0x3e}, P={0x7f,9,9,9,6},
    Q={0x3e,0x41,0x51,0x21,0x5e}, R={0x7f,9,0x19,0x29,0x46},
    S={0x46,0x49,0x49,0x49,0x31}, T={1,1,0x7f,1,1},
    U={0x3f,0x40,0x40,0x40,0x3f}, V={0x1f,0x20,0x40,0x20,0x1f},
    W={0x3f,0x40,0x38,0x40,0x3f}, X={0x63,0x14,8,0x14,0x63},
    Y={7,8,0x70,8,7}, Z={0x61,0x51,0x49,0x45,0x43},
    [' ']={0,0,0,0,0}, ['-']={8,8,8,8,8}, ['.']={0,0x60,0x60,0,0},
    [':']={0,0x36,0x36,0,0}, ['/']={0x20,0x10,8,4,2},
    ['+']={8,8,0x3e,8,8}, ['%']={0x63,0x13,8,0x64,0x63},
    ['?']={2,1,0x51,9,6}
}
local function color(r,g,b) return string.char(r,g,b) end
local bg,white,ink,muted=color(239,242,247),color(255,255,255),color(28,34,44),color(106,115,130)
local track,cyan,green,blue=color(229,233,240),color(0,193,210),color(45,205,119),color(55,144,255)
local atan2=math.atan2 or function(y,x) return math.atan(y,x) end
local function counter(n)
    return type(n)=='number' and n>=0 and n<=9007199254740991 and n or nil
end
function M.draw(status,now,state)
    status=status or {}
    local data=type(status.dashboard)=='table' and status.dashboard or {}
    local pixels={}
    for i=1,128*128 do pixels[i]=bg end
    local function point(x,y,c)
        if x>=0 and x<128 and y>=0 and y<128 then pixels[y*128+x+1]=c end
    end
    local function rect(x,y,w,h,c)
        for yy=y,y+h-1 do for xx=x,x+w-1 do point(xx,yy,c) end end
    end
    local function card(x,y,w,h,c)
        c=c or white
        rect(x+3,y,w-6,h,c); rect(x,y+3,w,h-6,c)
        rect(x+1,y+1,w-2,h-2,c)
    end
    local function text(x,y,value,c,scale)
        scale=scale or 1
        for char in tostring(value):upper():gmatch('.') do
            for col,bits in ipairs(font[char] or font['?']) do
                for row=0,6 do
                    if math.floor(bits/2^row)%2 == 1 then rect(x+(col-1)*scale,y+row*scale,scale,scale,c) end
                end
            end
            x=x+6*scale
        end
    end
    local function center(x,y,value,c,scale)
        scale=scale or 1
        text(math.floor(x-(#value*6-1)*scale/2),y,value,c,scale)
    end
    local function ring(cx,cy,r,arc,paint,thickness)
        for y=cy-r,cy+r do for x=cx-r,cx+r do
            local dx,dy=x-cx,y-cy
            local distance=math.sqrt(dx*dx+dy*dy)
            local angle=(atan2(dy,dx)*180/math.pi+360-arc[1])%360
            if distance>=r-(thickness or 3) and distance<=r and angle<=arc[2] then point(x,y,paint(angle/arc[2])) end
        end end
    end
    local function stroke(x1,y1,x2,y2,c)
        local steps=math.max(math.abs(x2-x1),math.abs(y2-y1))
        for i=0,steps do point(math.floor(x1+(x2-x1)*i/steps+.5),math.floor(y1+(y2-y1)*i/steps+.5),c) end
    end
    local rat,carrier,bars=(status[4] or ''):match('^(%S+)%s+(.-)%s+([|%-]+)%s*$')
    if not bars or #bars~=5 then
        rat=(status[4] or ''):find('SIM NONE',1,true) and 'NO SIM' or 'WAIT'
        carrier='A8'; bars=nil
    end
    local battery=status.battery
    local cap=battery and counter(battery.capacity)
    if cap and cap>100 then cap=nil end
    local filled=cap and math.ceil(cap/20) or 0
    if cap and battery.charging then filled=math.min(filled,4)+(now or 0)%2 end
    if state and state.page=='clock' then
        text(5,8,carrier:sub(1,10),ink)
        for i=1,5 do
            local height=2+i*2
            rect(75+(i-1)*10,17-height,7,height,bars and bars:sub(i,i)=='|' and blue or track)
        end
        card(4,29,120,70)
        center(64,43,os.date('%H:%M',now),ink,4)
        center(64,81,os.date('%S',now),blue,2)
        text(5,113,'BATT '..(cap and cap..'%' or '--%'),ink)
        for i=0,4 do rect(75+i*10,112,8,10,i<filled and green or track) end
        if battery and battery.charging then center(64,102,'CHARGING',green) end
        return 'P6\n128 128\n255\n'..table.concat(pixels)
    end
    text(4,4,os.date('%H:%M',now),ink)
    center(54,4,carrier:sub(1,7),ink)
    text(79,4,cap and (cap..'%') or '--%',muted)
    rect(107,3,18,9,muted); rect(108,4,16,7,bg); rect(125,6,2,3,muted)
    for i=0,filled-1 do rect(109+i*3,5,2,5,cap<=20 and color(230,75,75) or green) end
    local call=status.call
    if call then
        local letters=require('a8_display.font')
        local connected=call.state=='active' or call.state=='confirmed'
        local label=connected and '통화 중' or call.state=='ending' and '통화 종료 중' or
            call.direction=='incoming' and call.state=='ringing' and '전화 수신 중' or '전화 거는 중'
        local lines=letters.wrap(label,112)
        letters.draw(lines[1],8,20,ink,point)
        card(4,43,120,62)
        local name=call.name and call.name~='' and call.name or call.peer
        for i,line in ipairs(letters.wrap(name,112)) do
            if i>2 then break end
            letters.draw(line,8,47+(i-1)*16,ink,point)
        end
        if connected then
            local n=call.duration or 0
            center(64,85,string.format('%02d:%02d',math.floor(n/60),n%60),blue,2)
        else
            center(64,86,call.direction=='incoming' and 'INCOMING' or 'CALLING',blue)
        end
        center(64,115,call.audio=='bluetooth' and 'BLUETOOTH' or 'WEB AUDIO',muted)
        return 'P6\n128 128\n255\n'..table.concat(pixels)
    end
    if state and state.page~='status' then
        local selected=color(218,246,249)
        local function icon(kind,x,y,c)
            local aliases={network='wifi',relay='wifi',bt='bluetooth',pairable='bluetooth',
                ['bt-nap']='bluetooth',['bt-compat']='bluetooth',profiles='apn',profile='apn',auto='apn',
                ['usb-function']='usb',['usb-restart']='usb',tailscale='vpn',['tailscale-status']='vpn',wireguard='vpn',
                ['sms-open']='sms',['sms-older']='sms',reboot='power',recovery='power',bootloader='power',edl='power',poweroff='power',
                screensaver_timeout='display',screen_off_timeout='display',['display-timeout']='display'}
            kind=aliases[kind] or kind
            if kind=='wifi' then
                rect(x,y+2,13,1,c); rect(x+1,y+3,11,1,c); rect(x+3,y+6,7,1,c)
                rect(x+4,y+7,5,1,c); rect(x+6,y+10,1,2,c)
            elseif kind=='bluetooth' then
                stroke(x+6,y,x+6,y+12,c); stroke(x+6,y,x+11,y+4,c)
                stroke(x+11,y+4,x+1,y+10,c); stroke(x+1,y+2,x+11,y+8,c); stroke(x+11,y+8,x+6,y+12,c)
            elseif kind=='sms' then
                rect(x,y+2,13,9,c); rect(x+1,y+3,11,7,white)
                stroke(x,y+2,x+6,y+7,c); stroke(x+6,y+7,x+12,y+2,c)
            elseif kind=='back' then
                stroke(x+8,y+1,x+2,y+6,c); stroke(x+2,y+6,x+8,y+11,c)
            elseif kind=='usb' then
                stroke(x+6,y,x+6,y+11,c)
                stroke(x+4,y+2,x+6,y,c); stroke(x+6,y,x+8,y+2,c)
                stroke(x+1,y+5,x+1,y+7,c); stroke(x+1,y+7,x+6,y+10,c)
                rect(x,y+4,3,1,c); rect(x+1,y+3,1,3,c)
                stroke(x+11,y+4,x+11,y+6,c); stroke(x+11,y+6,x+6,y+8,c)
                rect(x+10,y+2,3,3,c); rect(x+5,y+11,3,1,c); rect(x+6,y+10,1,3,c)
            elseif kind=='vpn' then
                rect(x+4,y,5,1,c); rect(x+3,y+1,1,5,c); rect(x+9,y+1,1,5,c)
                rect(x+1,y+5,11,8,c); rect(x+2,y+6,9,6,white)
                rect(x+5,y+8,3,1,c); rect(x+6,y+7,1,4,c)
            elseif kind=='display' then
                rect(x,y+1,13,9,c); rect(x+1,y+2,11,7,white)
                rect(x+6,y+10,1,2,c); rect(x+3,y+12,7,1,c)
            elseif kind=='power' or kind=='status' then
                ring(x+6,y+6,5,{kind=='power' and 305 or 0,kind=='power' and 290 or 360},function() return c end,1)
                stroke(x+6,y+6,x+6,y,c)
            else
                for dy=2,10,4 do rect(x+1,y+dy,11,1,c); rect(x+3,y+dy-1,2,3,c) end
            end
        end
        local function hint(back)
            if back then
                text(4,121,state.mode=='side' and 'SIDE:PAGE PWR:BACK' or 'SHORT:NEXT HOLD:BACK',muted)
            else
                text(5,111,state.mode=='side' and 'SIDE: UP/DOWN' or 'SHORT: NEXT',muted)
                text(5,121,state.mode=='side' and 'POWER: SELECT' or 'HOLD 1S: SELECT',muted)
            end
        end
        local titles={menu='MENU',network='NETWORK',vpn='VPN',usb='USB',bt='BLUETOOTH',
            sms='SMS INBOX',power='POWER',priority='PRIORITY',profiles='APN PROFILES',confirm='CONFIRM',
            pairing='BLUETOOTH PAIR',message='NOTICE',display='DISPLAY',
            screensaver_timeout='SCREEN SAVER',screen_off_timeout='SCREEN OFF'}
        local function buttons(left,right,y)
            for i,label in ipairs({left,right}) do
                local x=i==1 and 4 or 66
                card(x,y,58,18,state.selection==i and selected or white)
                if state.selection==i then rect(x+2,y+3,2,12,cyan) end
                center(x+30,y+6,label,state.selection==i and ink or muted)
            end
        end
        if state.page=='sms-text' then
            local message=state.sms
            local page=message.pages[state.text_page]
            text(5,17,page.image and ('IMAGE '..page.index..'/'..page.count) or message.sender:sub(1,20),ink)
            card(4,27,120,82)
            if page.image then
                if page.preview then
                    for y=0,79 do for x=0,111 do
                        local offset=15+(y*112+x)*3
                        point(x+8,y+28,page.preview:sub(offset,offset+2))
                    end end
                else
                    center(64,60,page.loaded and 'IMAGE UNAVAILABLE' or 'LOADING IMAGE',muted)
                end
            else
                local letters=require('a8_display.font')
                for i,line in ipairs(page) do letters.draw(line,8,28+(i-1)*16,ink,point) end
            end
            text(5,111,message.timestamp:sub(1,16),muted)
            local page=state.text_page..'/'..#message.pages
            text(124-#page*6,111,page,blue); hint(true)
        else
            text(5,17,titles[state.page] or 'MENU',ink)
            if state.page=='menu' then
                local first=math.floor((state.selection-1)/4)*4+1
                if #state.entries>4 then
                    local pages=math.floor((state.selection-1)/4)+1 ..'/'..math.ceil(#state.entries/4)
                    text(124-#pages*6,17,pages,muted)
                end
                for i=first,math.min(first+3,#state.entries) do
                    local entry=state.entries[i]; local slot=i-first
                    local x=slot%2==0 and 4 or 66
                    local y=slot<2 and 29 or 69
                    card(x,y,58,36,state.selection==i and selected or white)
                    if state.selection==i then rect(x+2,y+5,2,26,cyan) end
                    icon(entry.id,x+23,y+5,({cyan,blue,green,color(136,102,221)})[slot+1])
                    center(x+29,y+25,entry.label,ink)
                end
                hint()
            elseif state.page=='pairing' then
                local request=state.pairing or {}
                card(4,29,120,56)
                center(64,34,tostring(request.device or 'DEVICE'):sub(1,19),ink)
                center(64,45,(request.address or ''):gsub(':',''),muted)
                local code=request.code or ''
                if code:match('^%d%d%d%d%d%d$') then center(64,60,code,ink,2)
                else center(64,63,'CONFIRM DEVICE',ink) end
                buttons('REJECT','APPROVE',89); hint()
            elseif state.page=='confirm' then
                card(4,29,120,52); center(64,35,'CONTINUE?',muted)
                local label=(state.pending or {}).label or ''
                if label:find('[^ -~]') then
                    local letters=require('a8_display.font')
                    for i,line in ipairs(letters.wrap(label,112)) do if i<=2 then letters.draw(line,8,45+(i-1)*16,ink,point) end end
                else
                    center(64,51,label:sub(1,19),ink)
                    if #label>19 then center(64,63,label:sub(20,38),ink) end
                end
                buttons('CANCEL','CONFIRM',89); hint()
            elseif state.page=='message' then
                card(4,29,120,76)
                center(64,36,'A8',blue,2)
                local value=tostring(state.message or ''):gsub('[^ -~]','?')
                for i=1,3 do center(64,60+(i-1)*12,value:sub((i-1)*19+1,i*19),ink) end
                center(64,117,'PRESS TO RETURN',muted)
            else
                local entries=state.entries or {}
                local selection=state.selection or 1
                local count=selection..'/'..#entries
                text(124-#count*6,17,count,muted)
                local detailed=state.page=='sms' or state.page=='display'
                local visible=detailed and 3 or 4
                local height=detailed and 27 or 19
                local first=math.max(1,selection-visible+1)
                for row=0,visible-1 do
                    local index=first+row; local entry=entries[index]
                    if entry then
                        local y=29+row*height; local active=index==selection
                        card(4,y,118,height-2,active and selected or white)
                        if active then rect(6,y+3,2,height-8,cyan) end
                        icon(entry.id,10,y+math.floor((height-15)/2),active and blue or muted)
                        local label=tostring(entry.label or '')
                        if label:find('[^ -~]') then
                            local letters=require('a8_display.font')
                            letters.draw(letters.wrap(label,96)[1],24,y+1,ink,point)
                        elseif entry.detail then
                            text(25,y+3,label:sub(1,16),ink); text(25,y+14,entry.detail:sub(1,16),muted)
                        elseif #label>16 then
                            text(25,y+1,label:sub(1,16),ink); text(25,y+9,label:sub(17,32),ink)
                        else text(25,y+math.floor((height-9)/2),label,ink) end
                        if entry.current then
                            stroke(111,y+8,114,y+11,blue); stroke(114,y+11,120,y+5,blue)
                        end
                    end
                end
                if #entries>visible then
                    rect(124,29,2,76,track)
                    rect(124,29+math.floor((first-1)*76/#entries),2,math.max(3,math.floor(visible*76/#entries)),cyan)
                end
                hint()
            end
        end
        return 'P6\n128 128\n255\n'..table.concat(pixels)
    end
    text(5,17,'A8 MOBILE',ink)
    text(91,17,battery and battery.charging and 'CHG' or '',green)
    card(4,28,58,65); card(66,28,58,65)
    center(33,32,'LTE DATA',muted); center(95,32,'SIGNAL',muted)
    local traffic=type(data.traffic)=='table' and data.traffic or {}
    local rx,tx=counter(traffic.rx),counter(traffic.tx)
    local total=rx and tx and counter(rx+tx)
    ring(33,63,20,{270,360},function(t)
        if not total or total==0 then return track end
        return t<rx/total and cyan or green
    end)
    local value,unit='--',''
    if total then
        local units={'B','KB','MB','GB','TB','PB'}; local n,index=total,1
        while n>=1000 and index<#units do n=n/1000; index=index+1 end
        value=n<10 and n%1~=0 and string.format('%.1f',n) or string.format('%.0f',n)
        unit=units[index]
    end
    local scale=#value<=3 and 2 or 1
    center(33,scale==2 and 53 or 57,value,ink,scale); center(33,71,unit,muted)
    center(33,84,'SESSION',muted)
    local level=bars and select(2,bars:gsub('|',''))
    ring(95,63,20,{135,270},function(t) return level and level>0 and t<=level/5 and blue or track end)
    center(95,#rat<=3 and 55 or 59,rat:sub(1,6),ink,#rat<=3 and 2 or 1)
    center(95,75,level and (level..'/5') or '--',muted)
    card(4,98,58,15); card(66,98,58,15)
    rect(9,102,9,1,blue); rect(10,103,7,1,blue); rect(11,105,5,1,blue); rect(13,108,1,1,blue)
    text(22,102,'WIFI',ink)
    local clients=counter(data.wifi_clients)
    local wifi=data.wifi==false and 'OFF' or clients and tostring(math.min(math.floor(clients),99)) or '--'
    text(wifi=='OFF' and 45 or 49,102,wifi,wifi=='OFF' and muted or blue)
    stroke(75,101,75,110,blue); stroke(75,101,79,104,blue); stroke(79,104,71,108,blue)
    stroke(71,103,79,108,blue); stroke(79,108,75,110,blue)
    text(86,102,'BT',ink)
    text(105,102,data.bluetooth=='yes' and 'ON' or data.bluetooth=='no' and 'OFF' or '--',muted)
    for y=119,125,3 do rect(5,y,8,1,muted) end
    local address=(status[6] or ''):match('(%d+%.%d+%.%d+%.%d+)')
    center(72,119,address or 'MENU',muted)
    return 'P6\n128 128\n255\n'..table.concat(pixels)
end
return M
