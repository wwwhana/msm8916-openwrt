-- SPDX-License-Identifier: GPL-2.0-only
local M = {}
local settings=require('a8_display.settings')
local root = {
    {label='Status', id='status'}, {label='Network', id='network'},
    {label='SMS', id='sms'}, {label='Power', id='power'}, {label='Display',id='display'}
}
local power = {
    {label='Power off', id='poweroff'}, {label='Reboot', id='reboot'}, {label='Recovery', id='recovery'},
    {label='Fastboot', id='bootloader'}, {label='EDL / USB 9008', id='edl'},
    {label='Back', id='back'}
}
local submenus={network=true, vpn=true, usb=true, bt=true}
function M.new(config)
    return {page='status', selection=1, entries=root, connected=false, mode='power', parents={}, idle=0,
            settings=settings.normalize(config)}
end
function M.configure(s,config)
    s.settings=settings.normalize(config)
    s.idle=0; s.saver=false; s.sleeping=false
end
function M.tick(s)
    if s.call or not s.connected or s.waking or s.page == 'confirm' or s.page == 'pairing' then
        s.idle=0; s.saver=false; s.sleeping=false; return
    end
    s.idle=s.idle+1
    local config=s.settings
    s.sleeping=config.screen_off_timeout>0 and s.idle>=config.screen_off_timeout
    s.saver=s.sleeping or config.screensaver_timeout>0 and s.idle>=config.screensaver_timeout
end
function M.activity(s,event)
    local input=event == 'pressed' or event == 'short' or event == 'long' or event == 'up' or event == 'down'
    if s.call and input then return true end
    if s.waking then
        if event:match('^released:') then s.waking=nil; return true end
        if event == 'disconnected' or event == 'resync' or event:match('^mode%-') then s.waking=nil
        elseif input then return true end
    end
    if not input then return false end
    s.idle=0
    s.sleeping=false
    if s.saver then
        s.saver=false; s.waking=event == 'pressed'
        return true
    end
    return false
end
function M.call(s,call)
    local was=s.call~=nil
    s.call=call
    if call or was then s.idle=0; s.saver=false; s.sleeping=false; s.waking=nil end
end
local function snapshot(s)
    return {page=s.page, selection=s.selection, entries=s.entries, sms=s.sms, text_page=s.text_page}
end
local function menu_entries(entries)
    local result={}
    for _,entry in ipairs(entries or {}) do result[#result+1]=entry end
    result[#result+1]={label='Back',id='back'}
    return result
end
local function restore(s, target, menus)
    s.page=target.page; s.selection=target.selection; s.entries=target.entries
    s.sms=target.sms; s.text_page=target.text_page
    if submenus[s.page] and menus then s.entries=menu_entries(menus[s.page]) end
    if s.page=='display' then s.entries=settings.entries(s.settings)
    elseif settings.is_timeout(s.page) then s.entries=settings.choices(s.settings,s.page) end
    s.selection=math.min(s.selection,#s.entries)
end
local function enter(s,page,entries)
    s.parents[#s.parents+1]=snapshot(s)
    s.page=page; s.entries=entries; s.selection=1
end
local function reset(s)
    s.page='status'; s.selection=1; s.entries=root; s.pending=nil; s.resume=nil; s.parents={}; s.pairing=nil
    s.sms=nil; s.text_page=nil
    s.idle=0; s.saver=false; s.sleeping=false; s.waking=nil
end
local function back(s,menus)
    local target=table.remove(s.parents)
    if target then restore(s,target,menus) else reset(s) end
end
function M.pairing(s,request)
    if not s.connected then return end
    if request and request.id == s.answered_pairing then return end
    if s.pairing and (not request or request.id ~= s.pairing.id) then
        back(s); s.pairing=nil
    end
    if request and not s.pairing then
        s.saver=false; s.sleeping=false; s.idle=0
        enter(s,'pairing',{{label='Reject',id='pairing',value='0'}, {label='Approve',id='pairing',value='1'}})
        s.pairing=request
    end
end
function M.event(s, event, profiles, menus)
    if event == 'mode-power' or event == 'mode-side' or event == 'resync' then
        if event ~= 'resync' then s.mode=event == 'mode-side' and 'side' or 'power' end
        reset(s)
        return
    end
    if event == 'connected' or event == 'disconnected' then
        s.connected = event == 'connected'
        if not s.connected then
            reset(s)
        end
        return
    end
    if not s.connected then return end
    local next_item=(event == 'down' and s.mode == 'side') or (event == 'short' and s.mode == 'power')
    local previous=event == 'up' and s.mode == 'side'
    local select=event == 'long' or (event == 'short' and s.mode == 'side')
    if not next_item and not previous and not select then return end
    s.message = nil
    if s.page == 'message' then
        local target=s.resume; s.resume=nil
        if target then restore(s,target,menus)
        else s.page='menu'; s.selection=1; s.entries=root end
        return
    end
    if s.page == 'status' then
        s.page='menu'; s.selection=1; s.entries=root; return
    end
    if s.page == 'sms-text' then
        if select then back(s,menus)
        else s.text_page=(s.text_page-1+(previous and -1 or 1)) % #s.sms.pages+1 end
        return
    end
    if next_item then s.selection=s.selection % #s.entries + 1; return end
    if previous then s.selection=(s.selection-2) % #s.entries + 1; return end
    local entry = s.entries[s.selection]
    if s.page == 'pairing' then
        local action={id='pairing',value=entry.value,profile=s.pairing.id}
        back(s,menus); s.pairing=nil
        return action
    end
    if s.page == 'confirm' then
        local action = entry.id == 'yes' and s.pending or nil
        back(s,menus); s.pending=nil
        return action
    end
    if entry.id == 'back' then
        back(s,menus)
    elseif entry.id == 'status' then
        reset(s)
    elseif entry.id == 'sms' or entry.id == 'sms-older' then
        return {id='sms-list',profile=entry.profile}
    elseif entry.id == 'sms-open' then
        return {id='sms-open',profile=entry.profile}
    elseif submenus[entry.id] then
        enter(s,entry.id,menu_entries((menus or {})[entry.id]))
    elseif entry.id == 'power' then
        enter(s,'power',power)
    elseif entry.id == 'display' then
        enter(s,'display',settings.entries(s.settings))
    elseif settings.is_timeout(entry.id) then
        enter(s,entry.id,settings.choices(s.settings,entry.id))
        for i,choice in ipairs(s.entries) do if choice.current then s.selection=i; break end end
    elseif entry.id == 'display-timeout' then
        return {id=entry.id,profile=entry.profile,value=entry.value}
    elseif entry.id == 'priority' then
        enter(s,'priority',{
            {label='Wi-Fi first', id='policy', value='sta'},
            {label='LTE first', id='policy', value='auto'},
            {label='LTE only', id='policy', value='modem'},
            {label='Ethernet first', id='policy', value='ethernet'},
            {label='Back', id='back'}})
    elseif entry.id == 'apn' then
        local entries={{label='Automatic', id='auto'}}
        for _, p in ipairs(profiles or {}) do
            entries[#entries+1] = {label=p.label, id='profile', profile=p.id}
        end
        entries[#entries+1] = {label='Back', id='back'}
        enter(s,'profiles',entries)
    elseif entry.info then
        return
    elseif submenus[s.page] or (s.page == 'priority' and entry.id == 'policy') then
        return {id=entry.id, value=entry.value, profile=entry.profile}
    else
        s.pending = {id=entry.id, profile=entry.profile, label=entry.label}
        enter(s,'confirm',{{label='Cancel', id='no'}, {label='Confirm', id='yes'}})
    end
end
local function finish_loading(s)
    if s.page=='message' and s.resume then restore(s,s.resume); s.resume=nil end
end
function M.inbox(s,entries)
    finish_loading(s)
    enter(s,'sms',menu_entries(entries))
end
function M.read_sms(s,message)
    finish_loading(s)
    enter(s,'sms-text',{})
    message.pages={}
    for i,part in ipairs(message.images or {}) do
        message.pages[#message.pages+1]={image=part.id,index=i,count=#message.images}
    end
    if message.body~='' then
        for _,page in ipairs(require('a8_display.font').pages(message.body)) do message.pages[#message.pages+1]=page end
    end
    if #message.pages==0 then message.pages={{'(No text)'}} end
    s.sms=message; s.text_page=1
end
function M.image(s,preview)
    local page=s.sms.pages[s.text_page]
    page.preview=preview; page.loaded=true
end
function M.result(s, ok, message, action)
    if ok and action and action.id == 'pairing' then s.answered_pairing=action.profile end
    if s.page ~= 'message' then s.resume=snapshot(s) end
    s.page='message'; s.message=message or (ok and 'Saved' or 'Failed')
end
return M
