-- SPDX-License-Identifier: GPL-2.0-only
local M = {}
local function line(s)
    return (tostring(s or ''):gsub('[^ -~]', '?') .. string.rep(' ', 16)):sub(1, 16)
end
function M.frame(s, status)
    local rows = {}
    if s.page == 'status' then
        for i=1,8 do rows[i]=(status or {})[i] or '' end
        rows[1] = s.connected and 'A8  PRESS:MENU' or 'A8 NO BUTTON'
    elseif s.page == 'message' then
        rows={'A8', '', s.message, '', '', '', 'Press for menu', ''}
    else
        rows[1] = s.page == 'confirm' and 'Confirm action?' or s.page == 'profiles' and 'APN profiles' or 'A8 menu'
        rows[2] = s.page == 'confirm' and s.pending.label or ''
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
