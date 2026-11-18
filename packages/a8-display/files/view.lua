-- SPDX-License-Identifier: GPL-2.0-only
local M = {}
local function line(s)
    return (tostring(s or ''):gsub('[^ -~]', '?') .. string.rep(' ', 16)):sub(1, 16)
end
local function battery_line(status,now)
    local battery=(status or {}).battery
    if not battery then return (status or {})[3] or 'BATT ---%  -----' end
    local bars=math.ceil(battery.capacity/20)
    if battery.charging then bars=math.min(bars,4)+math.floor(now or 0)%2 end
    return string.format('BATT %03d%%  %s%s',battery.capacity,
        string.rep('|',bars),string.rep('-',5-bars))
end
local function carrier_line(status)
    local text=(status or {})[4] or 'LTE --     -----'
    local carrier,bars=text:match('^%S+%s+(.-)%s+([|%-]+)%s*$')
    if carrier and #bars == 5 then return string.format('%-10.10s %s',carrier,bars) end
    return text
end
function M.clock(status,now)
    return require('a8_display.dashboard').draw(status,now,{page='clock'})
end
function M.screen(s,status,now)
    return require('a8_display.dashboard').draw(status,now,s)
end
function M.frame(s, status, now, text_clock)
    local rows = {}
    if (status or {}).call then
        local call=status.call
        local connected=call.state=='active' or call.state=='confirmed'
        local n=call.duration or 0
        rows={'PHONE',connected and 'IN CALL' or call.direction=='incoming' and 'INCOMING CALL' or 'CALLING',
            call.peer,'',connected and string.format('%02d:%02d',math.floor(n/60),n%60) or '',
            '',call.audio=='bluetooth' and 'BLUETOOTH' or 'WEB AUDIO',''}
    elseif s.saver then
        rows[1]=carrier_line(status)
        if text_clock then rows[4]='    '..os.date('%H:%M:%S',now) end
        rows[8]=battery_line(status,now)
    elseif s.page == 'status' then
        for i=1,8 do rows[i]=(status or {})[i] or '' end
        if (status or {}).battery then rows[3]=battery_line(status,now) end
        rows[1] = s.connected and 'PRESS FOR MENU' or 'NO BUTTON'
    elseif s.page == 'pairing' then
        local r=s.pairing or {}
        rows={'Bluetooth pair?',r.device,(r.address or ''):gsub(':',''),r.code ~= '' and ('Code: ' .. (r.code or '')) or 'Confirm device',
            (s.selection == 1 and '> ' or '  ') .. 'Reject',
            (s.selection == 2 and '> ' or '  ') .. 'Approve',
            s.mode == 'side' and 'Side: up/down' or 'Short: next',
            s.mode == 'side' and 'Power: select' or 'Hold 1s: select'}
    elseif s.page == 'message' then
        rows={'OpenWrt', (status or {})[2], s.message, '', '', '', 'Press to return', ''}
    elseif s.page == 'sms-text' then
        rows={'SMS',s.sms.sender,s.sms.timestamp}
        for i,text in ipairs(s.sms.pages[s.text_page]) do rows[i+3]=text end
        rows[7]='Short: next page'; rows[8]='Hold 1s: back'
    else
        local titles={confirm='Confirm action?', profiles='APN profiles', network='Network',
            vpn='VPN', usb='USB', bt='Bluetooth', sms='SMS', power='Power', priority='Internet first',
            display='Display',screensaver_timeout='Screen saver',screen_off_timeout='Screen off'}
        rows[1] = titles[s.page] or 'Menu'
        rows[2] = s.page == 'confirm' and s.pending.label or (status or {})[2]
        local first = math.max(1, s.selection - 3)
        for i=0,3 do
            local index = first+i
            rows[i+3] = s.entries[index] and ((index == s.selection and '> ' or '  ') .. s.entries[index].label) or ''
        end
        rows[7]=s.mode == 'side' and 'Side: up/down' or 'Short: next'
        rows[8]=s.mode == 'side' and 'Power: select' or 'Hold 1s: select'
    end
    for i=1,8 do rows[i]=line(rows[i]) end
    return '\27[H' .. table.concat(rows, '\r\n')
end
return M
