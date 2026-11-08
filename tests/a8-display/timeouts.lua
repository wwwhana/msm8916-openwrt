local files=arg[1]
local settings=dofile(files..'/settings.lua')
package.loaded['a8_display.settings']=settings
local model=dofile(files..'/model.lua')
local function new(config)
    local s=model.new(config); model.event(s,'connected'); return s
end
local function ticks(s,n) for _=1,n do model.tick(s) end end
local s=new({screensaver_timeout=15,screen_off_timeout=60})
ticks(s,14); assert(not s.saver and not s.sleeping)
ticks(s,1); assert(s.saver and not s.sleeping)
ticks(s,44); assert(not s.sleeping)
ticks(s,1); assert(s.sleeping)
ticks(s,4); assert(s.sleeping)
assert(model.activity(s,'pressed') and not s.saver and not s.sleeping)
assert(model.activity(s,'long') and model.activity(s,'released:1200'))
assert(s.page=='status')
s=new({screensaver_timeout=300,screen_off_timeout=15})
ticks(s,15); assert(s.sleeping and s.saver)
s=new({screensaver_timeout=0,screen_off_timeout=15})
ticks(s,14); assert(not s.saver); ticks(s,1); assert(s.sleeping and s.saver)
s=new({screensaver_timeout=15,screen_off_timeout=0})
ticks(s,1000); assert(s.saver and not s.sleeping)
s=new({screensaver_timeout=0,screen_off_timeout=0})
ticks(s,1000); assert(not s.saver and not s.sleeping)
model.configure(s,{screensaver_timeout=30,screen_off_timeout=60})
assert(s.idle==0); ticks(s,29); assert(not s.saver)
ticks(s,1); assert(s.saver)
model.pairing(s,{id='wake',device='Phone',code='123456'})
ticks(s,100); assert(not s.saver and not s.sleeping and s.idle==0)
model.pairing(s,nil); s.page='confirm'
ticks(s,100); assert(not s.saver and not s.sleeping)

-- The menu opens at the saved value and refreshes the check mark after saving.
s=new({screensaver_timeout=30,screen_off_timeout=300})
for _=1,5 do model.event(s,'short') end
assert(s.page=='menu' and s.entries[s.selection].id=='display')
model.event(s,'long'); assert(s.page=='display' and s.entries[1].detail=='30 sec')
model.event(s,'long'); assert(s.page=='screensaver_timeout' and s.selection==3 and s.entries[3].current)
model.event(s,'short'); local action=model.event(s,'long')
assert(action.id=='display-timeout' and action.profile=='screensaver_timeout' and action.value=='60')
model.result(s,true,'Please wait')
model.configure(s,{screensaver_timeout=60,screen_off_timeout=300})
model.result(s,true,'Saved',action); model.event(s,'short')
assert(s.page=='screensaver_timeout' and s.selection==4 and s.entries[4].current and not s.entries[3].current)
while s.entries[s.selection].id~='back' do model.event(s,'short') end
model.event(s,'long'); assert(s.page=='display' and s.entries[1].detail=='1 min')

-- Exercise actual UCI read/save code. Pending web edits and failed writes stay separate.
local values={display='lcd',buttons='power',screensaver_timeout='30',screen_off_timeout='300'}
local pending,load_fails,set_fails,commit_fails=false,false,false,false
local commits=0
package.loaded.uci={cursor=function(_,savedir)
    assert(savedir==nil or savedir=='/var/run/a8-menu-uci')
    local loaded,changes=false,{}
    return {
        load=function(_,path) assert(path=='/etc/config/a8_device'); loaded=true; return not load_fails end,
        changes=function(_,name) assert(name=='a8_device'); return pending and {web=true} or {} end,
        get=function(_,name,section,key)
            assert(loaded and name=='a8_device' and section=='settings')
            return key and values[key] or 'settings'
        end,
        set=function(_,name,section,key,value)
            assert(savedir and loaded and name=='a8_device' and section=='settings')
            if set_fails then return false end
            changes[key]=value; return true
        end,
        commit=function(_,name)
            assert(savedir and name=='a8_device')
            if commit_fails then return false end
            for key,value in pairs(changes) do values[key]=value end
            commits=commits+1; return true
        end,
        close=function() end
    }
end}
assert(settings.read().display=='lcd' and settings.read().screen_off_timeout==300)
pending=true
assert(settings.read().screensaver_timeout==30)
local ok,message=settings.save('screensaver_timeout','15')
assert(not ok and message=='Apply web edits' and commits==0)
pending=false
assert(settings.save('screensaver_timeout','15'))
assert(settings.save('screen_off_timeout','0'))
assert(values.screensaver_timeout=='15' and settings.read().screen_off_timeout==0 and commits==2)
assert(values.display=='lcd' and values.buttons=='power')
for _,bad in ipairs({'-1','17','300;reboot','1.5','NaN','',true,{}}) do
    assert(not settings.save('screensaver_timeout',bad))
end
assert(not settings.save('buttons','15') and not settings.save('../display','15'))
assert(commits==2)
set_fails=true; assert(not settings.save('screen_off_timeout','60')); set_fails=false
commit_fails=true; assert(not settings.save('screen_off_timeout','60')); commit_fails=false
load_fails=true; assert(not settings.save('screen_off_timeout','60')); load_fails=false
assert(settings.read().screen_off_timeout==0 and commits==2)
local invalid=settings.normalize({screensaver_timeout='-1',screen_off_timeout='17'})
assert(invalid.screensaver_timeout==30 and invalid.screen_off_timeout==300)
assert(settings.label(0)=='Never' and settings.label(30)=='30 sec' and settings.label(60)=='1 min')
print('Configurable screen timers, saved selection, committed reads and protected UCI writes passed')
