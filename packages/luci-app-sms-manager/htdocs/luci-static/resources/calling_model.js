'use strict';
'require calling';
'require baseclass';
'require messaging';

return baseclass.extend({
    create: function() { return { api: calling.create(), call: { state: 'idle' }, sim: {}, contacts: [], history:[], historyMore:false, historyAt:0, error: '', route: 'browser', microphone:'',microphones:[],microphonePending:false,timer: null, leaseTimer: null, closed: false, pending: false, epoch:0, cleanup:null }; },
    refreshMicrophones: function(state) {return state.api.listMicrophones().then(function(devices) {state.microphones=devices;}).catch(function() {});},
    microphonePresentation: function(state) {
        var info=state.api.microphoneInfo();
        if (state.microphoneTesting) return {level:info.level,hint:'3초 동안 말해 보세요.'};
        if (state.microphoneTest && ['idle','ended','failed'].includes(state.call.state))
            return {level:Math.min(1,state.microphoneTest.peak*4),hint:state.microphoneTest.signal ? '마이크 입력 확인됨' : '마이크 입력 없음 · 장치를 바꿔 확인하세요.'};
        return {level:info.level,hint:info.muted ? '마이크가 일시 중지되었습니다.' : info.silent ? '마이크 입력 없음 · 입력 장치를 확인하세요.' : info.label};
    },
    testMicrophone: function(state) {
        if (state.pending || state.microphoneTesting || state.call.occupied) return Promise.reject(new Error('진행 중인 통화를 먼저 종료해 주세요.'));
        state.microphoneTesting=true;state.microphoneTest=null;
        return state.api.testAudio(state.microphone).then(function(info) {if (!state.closed)state.microphoneTest=info;})
            .finally(function() {state.microphoneTesting=false;});
    },
    selectMicrophone: function(state,device) {
        if (state.microphonePending || state.microphoneTesting) return Promise.resolve();
        var epoch=state.epoch;state.microphonePending=true;
        state.microphoneTest=null;
        var live=state.api.context || state.api.claimed && !['idle','ended','failed'].includes(state.call.state);
        return (live ? state.api.openAudio(device) : Promise.resolve()).then(function() {
            if (!state.closed && epoch===state.epoch) state.microphone=device;
        }).catch(function(error) {if (!state.closed && epoch===state.epoch)throw error;})
            .finally(function() {state.microphonePending=false;});
    },
    refresh: function(state) {
        var epoch = state.epoch, pending = state.pending;
        return Promise.all([state.api.status(), messaging.call('snapshot')]).then(function(results) {
            if (state.closed || epoch !== state.epoch) return state.call;
            state.call = results[0]; state.sim = results[1].service || {}; state.contacts = results[1].contacts || [];
            state.microphones=state.api.microphones || state.microphones;
            if (!pending && !state.pending && !state.microphoneTesting && ['idle', 'ended', 'failed'].includes(state.call.state)) {
                state.api.closeAudio();
                if (!state.call.owned) {state.api.claimed=false;window.clearTimeout(state.leaseTimer);}
                state.cleanup = state.api.release().catch(function() {}).finally(function() { state.cleanup = null; });
            }
            if (!state.historyAt || Date.now()-state.historyAt>10000 || state.historyCall!==state.call.state) {
                state.historyAt=Date.now();state.historyCall=state.call.state;
                return messaging.call('call_history',0).then(function(result) { state.history=result.calls || [];state.historyMore=!!result.more;return state.call; }).catch(function() { state.historyAt=0;return state.call; });
            }
            return state.call;
        });
    },
    contactOptions: function(state, filter) {
        filter = String(filter || '').toLocaleLowerCase().replace(/[\s()-]/g, '').replace(/\+82/g, '0');
        var choices = [];
        state.contacts.forEach(function(contact) {
            (contact.addresses || []).forEach(function(address) {
                var label = contact.name + ' · ' + (address.label ? address.label + ' · ' : '') + address.address;
                if (label.toLocaleLowerCase().replace(/[\s()-]/g, '').replace(/\+82/g, '0').includes(filter))
                    choices.push({number:address.address, label:label, name:contact.name});
            });
        });
        return choices;
    },
    peerLabel: function(state) {
        var peer = state.call.peer || '', normalized;
        try { normalized = messaging.normalize(peer); } catch (_) { return peer; }
        var match = this.contactOptions(state, '').find(function(contact) { return contact.number === normalized; });
        return match ? match.name + ' · ' + peer : peer;
    },
    presentation: function(state) {
        var call = state.call, idle = ['idle','ended','failed'].includes(call.state), incoming = call.direction === 'incoming' && call.state === 'ringing';
        var labels = { idle:'대기 중',dialing:'전화 거는 중',ringing:'연결 중',early_media:'연결 중',answering:'연결 중',confirmed:'통화 중',active:'통화 중',ending:'종료 중',ended:'통화 종료',failed:'연결 실패' };
        var duration = Math.max(0,Math.floor(Number(call.duration) || 0));
        return {idle:idle,incoming:incoming,active:!idle || state.pending,locked:call.occupied && !call.owned,
            status:state.canceling ? '종료 중' : state.pending ? '음성 연결 준비 중' : incoming ? '전화 수신 중' : labels[call.state] || '연결 확인 중',
            duration:(duration>=3600 ? Math.floor(duration/3600)+':' : '')+String(Math.floor(duration/60)%60).padStart(2,'0')+':'+String(duration%60).padStart(2,'0'),
            connected:['confirmed','active'].includes(call.state)};
    },
    guardLeave: function(state, event) {
        if (!state.api.claimed && !state.pending) return;
        event.preventDefault(); event.returnValue = ''; return '';
    },
    historyItem: function(row) {
        var labels={completed:row.direction==='incoming' ? '수신 통화' : '발신 통화',missed:'부재중 전화',rejected:'거절한 전화',canceled:'발신 취소',failed:'연결 실패',interrupted:'중단된 통화'};
        var duration=Math.max(0,Number(row.duration_seconds)||0);
        return {title:row.name || row.peer || '알 수 없는 번호',status:labels[row.result] || (row.direction==='incoming' ? '수신 중' : '발신 중'),
            duration:row.connected_at ? Math.floor(duration/60)+'분 '+duration%60+'초' : '',
            date:new Date(row.started_at).toLocaleString('ko-KR',{month:'numeric',day:'numeric',hour:'2-digit',minute:'2-digit',hour12:false}),
            missed:row.result==='missed',sim:(row.provider || '')+' · SIM '+row.slot};
    },
    moreHistory: function(state) {
        var last=state.history[state.history.length-1];
        if (!last || !state.historyMore) return Promise.resolve();
        return messaging.call('call_history',last.seq).then(function(result) {state.history=state.history.concat(result.calls || []);state.historyMore=!!result.more;});
    },
    maintainLease: function(state, onError) {
        var epoch=state.epoch;
        function heartbeat() {
            if (state.closed || state.canceling || epoch!==state.epoch || !state.api.claimed) return;
            state.api.lease(state.sim).catch(function(error) {
                if (state.closed || state.canceling || epoch!==state.epoch) return;
                state.error = error.message; state.api.closeAudio(); onError(error);
            }).finally(function() { if (!state.closed && !state.canceling && epoch===state.epoch && state.api.claimed) state.leaseTimer = window.setTimeout(heartbeat,3000); });
        }
        window.clearTimeout(state.leaseTimer); heartbeat();
    },
    dial: function(state, number) {
        if (state.microphoneTesting) return Promise.reject(new Error('마이크 확인 중입니다.'));
        if (state.pending) return Promise.reject(new Error('연결 중입니다.'));
        state.pending = true; state.canceling=false; var operation=++state.epoch;
        var selected = state.route.split(':');
        var self = this;
        return Promise.resolve(state.cleanup).then(function() { if (state.closed || operation!==state.epoch) throw new Error(state.closed ? '통화 화면을 벗어났습니다.' : '발신을 취소했습니다.');return state.claiming=state.api.claim().finally(function() {state.claiming=null;}); }).then(function() { if (state.closed || operation!==state.epoch) throw new Error(state.closed ? '통화 화면을 벗어났습니다.' : '발신을 취소했습니다.'); self.maintainLease(state,function() {}); return selected[0] === 'browser' ? state.api.openAudio(state.microphone) : undefined; })
            .then(function() { if (state.closed || operation!==state.epoch) throw new Error(state.closed ? '통화 화면을 벗어났습니다.' : '발신을 취소했습니다.'); return state.api.dial(number, selected[0], selected[1] || ''); })
            .catch(function(error) { if (!state.closed && operation!==state.epoch) return;state.api.closeAudio(); return state.api.release().catch(function() {}).then(function() { throw error; }); }).finally(function() { if (operation===state.epoch) {state.pending = false; state.canceling=false;} });
    },
    answer: function(state) {
        if (state.microphoneTesting) return Promise.reject(new Error('마이크 확인 중입니다.'));
        if (state.pending) return Promise.reject(new Error('연결 중입니다.'));
        state.pending = true; state.canceling=false; var operation=++state.epoch;
        var selected = state.route.split(':');
        var self = this;
        return Promise.resolve(state.cleanup).then(function() { if (state.closed || operation!==state.epoch) throw new Error(state.closed ? '통화 화면을 벗어났습니다.' : '발신을 취소했습니다.');return state.claiming=state.api.claim().finally(function() {state.claiming=null;}); }).then(function() { if (state.closed || operation!==state.epoch) throw new Error(state.closed ? '통화 화면을 벗어났습니다.' : '발신을 취소했습니다.'); self.maintainLease(state,function() {}); return selected[0] === 'browser' ? state.api.openAudio(state.microphone) : undefined; })
            .then(function() { if (state.closed || operation!==state.epoch) throw new Error(state.closed ? '통화 화면을 벗어났습니다.' : '발신을 취소했습니다.'); return state.api.answer(state.call.id, selected[0], selected[1] || ''); })
            .catch(function(error) { if (!state.closed && operation!==state.epoch) return;state.api.closeAudio(); return state.api.release().catch(function() {}).then(function() { throw error; }); }).finally(function() { if (operation===state.epoch) {state.pending = false; state.canceling=false;} });
    },
    hangup: function(state) {
        state.epoch++;state.canceling=true;state.api.closeAudio();
        state.cleanup=(state.claiming || (state.api.claimed || state.pending ? Promise.resolve() : state.api.claim())).then(function() { return state.api.release(); }).then(function() {
            state.call=Object.assign({},state.call,{state:'ended',owned:false,occupied:false,error:''});state.error='';
            state.pending=false;
        }).finally(function() { state.api.closeAudio();state.pending=false;state.canceling=false;state.cleanup=null; });
        return state.cleanup;
    },
    startAudio: function(state, onError) {
        function tick() {
            if (state.closed) return;
            var epoch=state.epoch,started=Date.now();
            state.api.exchangeAudio(state.sim, state.call).catch(function(error) {if (!state.closed && epoch===state.epoch && !state.canceling)onError(error);}).finally(function() {
                if (!state.closed) state.timer = window.setTimeout(tick, Math.max(0,100-(Date.now()-started)));
            });
        }
        tick();
    },
    close: function(state) { state.closed = true; window.clearTimeout(state.timer); window.clearTimeout(state.leaseTimer); state.api.leave(state.sim); state.api.closeAudio(); }
});
