local ims=dofile(arg[1])
local unsupported="error: couldn't create client for the 'imsa' service: QMI protocol error (31): 'InvalidServiceType'"
assert(ims.registration(unsupported).state == 'unsupported')
assert(ims.registration('cancelling the operation...').state == 'error')
assert(ims.registration('IMS registration:\n').status == nil)
local registration=ims.registration("[/dev/wwan0qmi0] IMS registration:\n\tStatus: 'registered'\n\tTechnology: 'wwan'\n")
assert(registration.state == 'available' and registration.status == 'registered' and registration.technology == 'wwan')
assert(ims.registration("IMS registration:\nStatus: 'not-registered'\n").status == 'not-registered')
local services=ims.services([[IMS services:
	SMS service
		    Status: 'full-service'
		Technology: 'wwan'
	Voice service
		    Status: 'no-service'
		Technology: 'wwan'
	Video Telephony service
		    Status: 'limited-service'
]])
assert(services.sms.status == 'full-service' and services.voice.status == 'no-service')
local settings=ims.settings('IMS services:\nIMS registration enabled: yes\nSMS service enabled: no\nVoice service enabled: yes\n')
assert(settings.state == 'available' and settings.ims == 'yes' and settings.sms == 'no' and settings.voice == 'yes')

local client={version=1, pid=123, slot=2, updated_at=100, registration='registered', sms='ready'}
local runtime=ims.client(client, 101)
assert(runtime.backend == 'modemconfig-ko' and runtime.sim_slot == 2)
assert(runtime.registration.status == 'registered' and runtime.services.sms.status == 'ready')
assert(runtime.services.voice.status == 'unavailable' and runtime.services.rcs.status == 'unavailable')
assert(ims.client(client, 200) == nil)
client.slot=0; assert(ims.client(client, 101) == nil)
client.slot=1; client.registration='private text'; assert(ims.client(client, 101) == nil)
client.registration='not-registered'; client.sms_transport='nas'; client.sms_ready=true
local nas=ims.client(client, 101)
assert(nas.registration.status == 'not-registered' and nas.services.sms.status == 'ready')
assert(nas.sms_transport == 'nas' and nas.settings.ims == 'no')
client.sms_ready=false
assert(ims.client(client, 101).services.sms.status == 'unavailable')
client.sms_transport='cs'; assert(ims.client(client, 101) == nil)

-- Exercise the real service discovery: do not allocate unsupported clients.
local calls={}
local execute, popen=os.execute, io.popen
os.execute=function(cmd) assert(cmd == 'test -c /dev/wwan0qmi0'); return 0 end
io.popen=function(cmd)
    calls[#calls+1]=cmd
    assert(cmd:find('flock -n /var/lock/sim-hotplug.lock', 1, true))
    assert(cmd:find('--device-open-proxy', 1, true))
    assert(not cmd:find('client-no-release', 1, true))
    return {read=function() return '[/dev/wwan0qmi0] Supported versions:\n\twms (1.10)\n\twds (1.36)\n' end, close=function() end}
end
local status=ims.status()
assert(status.state == 'unsupported' and status.registration.state == 'unsupported')
assert(#calls == 1 and calls[1]:find('--get-service-version-info', 1, true))
os.execute=function() return 1 end
assert(ims.status().state == 'no-device' and #calls == 1)
os.execute, io.popen=execute, popen
print('IMS parsing, unsupported firmware and read-only query checks passed')

client.sms_transport='nas'; client.voice_ready=true; client.rcs={state="registered",sip_status=200}; client.registration="registered"
local connected=ims.client(client,101)
assert(connected.services.voice.status=="ready" and connected.services.rcs.status=="registered" and connected.settings.ims=="yes")
