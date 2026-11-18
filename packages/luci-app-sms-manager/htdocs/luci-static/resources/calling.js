'use strict';
'require rpc';
'require baseclass';
'require messaging';

// Voice and lease requests must not wait for LuCI's next paint frame.
function declare(method, params) { return rpc.declare({ object: 'modemconfig-ko', method: method, params: params || [], expect: {}, reject: true, nobatch: true }); }
var status = declare('voice_status', ['owner']);
var claim = declare('voice_claim', ['subscription', 'session', 'owner']);
var lease = declare('voice_lease', ['subscription', 'session', 'owner']);
var release = declare('voice_release', ['subscription', 'session', 'owner', 'reason']);
var dial = declare('voice_dial', ['subscription', 'session', 'to', 'audio', 'device', 'owner']);
var answer = declare('voice_answer', ['subscription', 'session', 'id', 'audio', 'device', 'owner']);
var audio = declare('voice_audio', ['subscription', 'session', 'id', 'sequence', 'data', 'owner']);
var errors = { 'service-unavailable': '모뎀 서비스에 연결할 수 없습니다.', 'sim-unavailable': 'SIM 연결을 다시 확인해 주세요.',
    busy: '진행 중인 통화를 먼저 종료해 주세요.', 'invalid-number': '전화번호를 확인해 주세요.',
    'call-in-use': '다른 창에서 통화를 사용 중입니다.', 'call-unavailable': '통화가 종료되었습니다.', 'call-failed': '통화를 연결하지 못했습니다.', 'audio-busy': '음성 전송이 지연되고 있습니다.', 'bluetooth-unavailable': '기기에 통화용 블루투스 헤드셋을 연결하세요.', 'bluetooth-audio-failed': '블루투스 음성을 열지 못했습니다.', 'bluetooth-disconnected': '블루투스 헤드셋 연결이 끊겼습니다.' };

function checked(result) { if (result.error) throw new Error(errors[result.error] || '통화 처리 중 오류가 발생했습니다.'); return result; }
function withSIM(work) { return messaging.call('snapshot').then(function(snapshot) { return work(snapshot.service || {}); }).then(checked); }

var Calling = L.Class.extend({
    status: function() { return status(this.owner).then(checked); },
    claim: function() {
        var self = this;
        return withSIM(function(sim) { return claim(sim.subscription, sim.session, self.owner); }).then(function(result) { self.claimed = true; return result; });
    },
    lease: function(sim) { return lease(sim.subscription, sim.session, this.owner).then(checked); },
    dial: function(to, route, device) { var owner = this.owner; return withSIM(function(sim) { return dial(sim.subscription, sim.session, to, route, device, owner); }); },
    answer: function(id, route, device) { var owner = this.owner; return withSIM(function(sim) { return answer(sim.subscription, sim.session, id, route, device, owner); }); },
    release: function() {
        if (!this.claimed) return Promise.resolve();
        if (this.releasing) return this.releasing;
        var owner = this.owner, self = this;
        this.releasing = withSIM(function(sim) { return release(sim.subscription, sim.session, owner, 'hangup'); })
            .then(function(result) { self.claimed = false; return result; })
            .finally(function() { self.releasing = null; });
        return this.releasing;
    },
    leave: function(sim) {
        if (!this.claimed) return;
        this.claimed = false;
        // pagehide runs only after the user accepts the browser's leave warning.
        window.fetch(rpc.getBaseURL(), {method:'POST',credentials:'same-origin',keepalive:true,
            headers:{'Content-Type':'application/json'},body:JSON.stringify({jsonrpc:'2.0',id:1,method:'call',
                params:[rpc.getSessionID(),'modemconfig-ko','voice_release',{subscription:sim.subscription,session:sim.session,owner:this.owner,reason:'page-left'}]})}).catch(function() {});
    },
    listMicrophones: function() {
        var self=this;
        if (!navigator.mediaDevices || !navigator.mediaDevices.enumerateDevices) return Promise.resolve([]);
        return navigator.mediaDevices.enumerateDevices().then(function(devices) {
            self.microphones=devices.filter(function(device) {return device.kind==='audioinput' && device.deviceId && device.deviceId!=='default';})
                .map(function(device,index) {return {id:device.deviceId,label:device.label || '마이크 '+(index+1)};});
            return self.microphones;
        });
    },
    microphoneInfo: function() {
        var track=this.stream && this.stream.getAudioTracks()[0],level=Math.min(1,(this.micPeak || 0)*4);
        this.micPeak=0;
        return {level:level,peak:this.micObservedPeak || 0,signal:!!this.micSignal,label:track && track.label || '',muted:!!track && (track.muted || track.enabled===false),
            silent:!!track && Date.now()-this.micLastSignal>4000};
    },
    testAudio: function(device) {
        var self=this,opening=self.openAudio(device),generation=self.audioGeneration;
        return opening.then(function() {
            return new Promise(function(resolve,reject) {window.setTimeout(function() {
                if (generation!==self.audioGeneration) return reject(new Error('마이크 확인을 취소했습니다.'));
                resolve(self.microphoneInfo());
            },3000);});
        }).finally(function() {if (generation===self.audioGeneration)self.closeAudio();});
    },
    openAudio: function(device) {
        var self = this;
        if (!window.isSecureContext || !navigator.mediaDevices) return Promise.reject(new Error('HTTPS로 접속해야 마이크를 사용할 수 있습니다.'));
        this.closeAudio();
        var generation = this.audioGeneration;
        var constraints={channelCount:1,echoCancellation:true,noiseSuppression:true,autoGainControl:true};
        if (device) constraints.deviceId={exact:device};
        return navigator.mediaDevices.getUserMedia({ audio: constraints, video: false })
            .then(function(stream) {
                if (generation !== self.audioGeneration) {
                    stream.getTracks().forEach(function(track) { track.stop(); });
                    throw new Error('음성 연결을 취소했습니다.');
                }
                self.stream = stream;
                self.context = new (window.AudioContext || window.webkitAudioContext)();
                self.input = self.context.createMediaStreamSource(stream);
                self.processor = self.context.createScriptProcessor(2048, 1, 1);
                self.samples = []; self.sampleOffset = 0; self.sequence = self.sequence || 0; self.nextPlay = 0;
                self.micPeak=0;self.micObservedPeak=0;self.micSignal=false;self.micLastSignal=Date.now();
                self.processor.onaudioprocess = function(event) {
                    var input = event.inputBuffer.getChannelData(0), ratio = self.context.sampleRate / 16000;
                    var peak=0;for (var i=0;i<input.length;i++)peak=Math.max(peak,Math.abs(input[i]));
                    self.micPeak=Math.max(self.micPeak,peak);self.micObservedPeak=Math.max(self.micObservedPeak,peak);
                    if (peak>0) {self.micLastSignal=Date.now();self.micSignal=true;}
                    if (!self.current || !['active', 'confirmed', 'early_media'].includes(self.current.state)) { self.samples = []; return; }
                    var position = self.sampleOffset;
                    for (; position < input.length; position += ratio) {
                        var index = Math.floor(position), fraction = position - index;
                        var value = input[index] * (1 - fraction) + input[Math.min(index + 1, input.length - 1)] * fraction;
                        self.samples.push(Math.round(Math.max(-1, Math.min(1, value)) * 32767));
                    }
                    self.sampleOffset = position - input.length;
                    if (self.samples.length > 6400) self.samples.splice(0, self.samples.length - 6400);
                };
                self.input.connect(self.processor); self.processor.connect(self.context.destination);
                return self.context.resume().then(function() {return self.listMicrophones().catch(function() {return [];});});
            });
    },
    exchangeAudio: function(sim, current) {
        var self = this; self.current = current;
        if (current.audio === 'bluetooth' || !self.context || !current.id || !['active', 'confirmed', 'early_media'].includes(current.state)) return Promise.resolve();
        var count = Math.min(3200, Math.floor(self.samples.length / 320) * 320), samples = self.samples.splice(0, count);
        var raw = new Uint8Array(count * 2), view = new DataView(raw.buffer);
        samples.forEach(function(value, index) { view.setInt16(index * 2, value, true); });
        var encoded = ''; raw.forEach(function(value) { encoded += String.fromCharCode(value); });
        if (!current.owned) return Promise.resolve();
        var generation = self.audioGeneration;
        return audio(sim.subscription, sim.session, current.id, ++self.sequence, btoa(encoded), self.owner).then(checked).then(function(result) {
            if (!result.data || !self.context || generation !== self.audioGeneration) return;
            var decoded = atob(result.data), pcm = new DataView(new ArrayBuffer(decoded.length));
            for (var i = 0; i < decoded.length; i++) pcm.setUint8(i, decoded.charCodeAt(i));
            var buffer = self.context.createBuffer(1, decoded.length / 2, 16000), values = buffer.getChannelData(0);
            for (var j = 0; j < values.length; j++) values[j] = pcm.getInt16(j * 2, true) / 32768;
            var source = self.context.createBufferSource(); source.buffer = buffer; source.connect(self.context.destination);
            self.nextPlay = Math.max(self.context.currentTime + 0.025, Math.min(self.nextPlay, self.context.currentTime + 0.3));
            source.start(self.nextPlay); self.nextPlay += buffer.duration;
        });
    },
    closeAudio: function() {
        this.audioGeneration = (this.audioGeneration || 0) + 1;
        this.current = null; this.samples = [];
        if (this.processor) { this.processor.onaudioprocess = null; this.processor.disconnect(); this.processor = null; }
        if (this.input) { this.input.disconnect(); this.input = null; }
        if (this.stream) { this.stream.getTracks().forEach(function(track) { track.stop(); }); this.stream = null; }
        if (this.context) { this.context.close(); this.context = null; }
    }
});

return baseclass.extend({ create: function() {
    var api = new Calling(), bytes = new Uint8Array(16);
    window.crypto.getRandomValues(bytes);
    api.owner = Array.from(bytes, function(value) { return value.toString(16).padStart(2,'0'); }).join('');
    return api;
} });
