-- SPDX-License-Identifier: GPL-2.0-only
local M = {}
local ranges={{0x20,0xff},{0x1100,0x11ff},{0x2000,0x206f},{0x3000,0x303f},
    {0x3130,0x318f},{0xac00,0xd7a3},{0xff00,0xffef},{0xfffd,0xfffd}}
local glyphs={}
function M.chars(text)
    local i=1
    return function()
        if i>#text then return end
        local start=i
        local b=text:byte(i)
        local count=b<0x80 and 1 or b>=0xc2 and b<=0xdf and 2 or
            b>=0xe0 and b<=0xef and 3 or b>=0xf0 and b<=0xf4 and 4 or 0
        local code=count==1 and b or count>1 and b%2^(7-count) or nil
        for offset=1,count-1 do
            local next_byte=text:byte(i+offset)
            if not next_byte or next_byte<0x80 or next_byte>0xbf then code=nil; break end
            code=code*64+next_byte-0x80
        end
        if not code or (count==2 and code<0x80) or (count==3 and code<0x800) or
           (count==4 and code<0x10000) or code>0x10ffff or (code>=0xd800 and code<=0xdfff) then
            i=i+1; return '?',63
        end
        i=i+count
        if code<32 and code~=10 then return '?',63 end
        return text:sub(start,i-1),code
    end
end
local function width(code) return code<256 and 8 or 16 end
function M.wrap(text,max_width)
    local lines,line,used={},'',0
    text=tostring(text or ''):gsub('\r\n','\n'):gsub('\r','\n'):gsub('\t',' ')
    for char,code in M.chars(text) do
        if code==10 then lines[#lines+1]=line; line=''; used=0
        else
            if used+width(code)>max_width then lines[#lines+1]=line; line=''; used=0 end
            line=line..char; used=used+width(code)
        end
    end
    lines[#lines+1]=line
    return lines
end
function M.pages(text)
    local pages={}
    for i,line in ipairs(M.wrap(text,112)) do
        local page=math.floor((i-1)/5)+1
        pages[page]=pages[page] or {}; pages[page][#pages[page]+1]=line
    end
    return pages
end
local function glyph(code)
    if glyphs[code] then return glyphs[code] end
    local offset=4
    for _,range in ipairs(ranges) do
        if code>=range[1] and code<=range[2] then
            local f=io.open('/usr/share/a8-display/unifont.bin','rb')
            if f then
                if f:read(4)=='A8F1' and f:seek('set',offset+(code-range[1])*32) then
                    local data=f:read(32)
                    if data and #data==32 then glyphs[code]=data end
                end
                f:close()
            end
            break
        end
        offset=offset+(range[2]-range[1]+1)*32
    end
    -- Unsupported characters remain visible as boxes rather than vanishing.
    return glyphs[code] or string.rep('\126\0',16)
end
function M.draw(text,x,y,color,point)
    for _,code in M.chars(text) do
        local data=glyph(code)
        for row=0,15 do
            local bits=data:byte(row*2+1)*256+data:byte(row*2+2)
            for col=0,width(code)-1 do
                if math.floor(bits/2^(15-col))%2==1 then point(x+col,y+row,color) end
            end
        end
        x=x+width(code)
    end
end
return M
