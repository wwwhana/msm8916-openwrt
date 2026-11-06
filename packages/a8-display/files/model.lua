-- SPDX-License-Identifier: GPL-2.0-only
local M = {}
local root = {
    {label='Status', id='status'}, {label='APN profiles', id='apn'},
    {label='Reboot', id='reboot'}, {label='Recovery', id='recovery'},
    {label='Fastboot', id='bootloader'}, {label='EDL / USB 9008', id='edl'},
    {label='Power off', id='poweroff'}, {label='Back', id='back'}
}
function M.new()
    return {page='status', selection=1, entries=root, connected=false}
end
function M.event(s, event, profiles)
    if event == 'connected' or event == 'disconnected' then
        s.connected = event == 'connected'
        if not s.connected then
            s.page='status'; s.selection=1; s.entries=root; s.pending=nil
        end
        return
    end
    if event ~= 'short' and event ~= 'long' then return end
    s.message = nil
    if s.page == 'status' or s.page == 'message' then
        s.page='menu'; s.selection=1; s.entries=root; return
    end
    if event == 'short' then s.selection=s.selection % #s.entries + 1; return end
    local entry = s.entries[s.selection]
    if s.page == 'confirm' then
        local action = entry.id == 'yes' and s.pending or nil
        s.page='menu'; s.selection=1; s.entries=root; s.pending=nil
        return action
    end
    if entry.id == 'back' or entry.id == 'status' then
        s.page='status'; s.entries=root; s.selection=1
    elseif entry.id == 'apn' then
        s.page='profiles'; s.selection=1; s.entries={{label='Automatic', id='auto'}}
        for _, p in ipairs(profiles or {}) do
            s.entries[#s.entries+1] = {label=p.label, id='profile', profile=p.id}
        end
        s.entries[#s.entries+1] = {label='Back', id='back'}
    else
        s.pending = {id=entry.id, profile=entry.profile, label=entry.label}
        s.page='confirm'; s.selection=1
        s.entries={{label='Cancel', id='no'}, {label='Confirm', id='yes'}}
    end
end
function M.result(s, ok, message)
    s.page='message'; s.message=message or (ok and 'Saved' or 'Failed')
end
return M
