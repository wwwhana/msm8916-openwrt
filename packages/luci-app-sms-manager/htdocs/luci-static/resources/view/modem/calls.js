'use strict';
'require view';
'require ui';
'require poll';
'require calling_model as callModel';

function icon(name) { return E('span', {'class':'call-icon call-icon-'+name,'aria-hidden':'true'}); }
function action(label, name, handler, extra) {
    return E('button', Object.assign({type:'button','class':'call-button',click:handler},extra || {}), [icon(name),E('span',{},label)]);
}

return view.extend({
    load: function() { this.model = callModel; this.state = this.model.create(); return Promise.all([this.model.refresh(this.state),this.model.refreshMicrophones(this.state)]).catch(L.bind(function(e) { this.state.error = e.message; }, this)); },
    action: function(work) {
        var self = this; self.state.error = '';
        var pending = Promise.resolve().then(work);
        // Pending microphone permission must already be visible and protected.
        pending = pending.then(function() { return self.model.refresh(self.state); });
        window.setTimeout(function() { self.paint(); },0);
        return pending.catch(function(error) { self.state.error = error.message; }).finally(function() { self.paint(); });
    },
    paintContacts: function() {
        var self = this, choices = this.model.contactOptions(this.state,this.contactSearch.value), signature = JSON.stringify(choices);
        if (signature === this.lastContacts) return;
        this.lastContacts = signature;
        this.contactList.replaceChildren.apply(this.contactList, choices.length ? choices.map(function(contact) {
            return E('button', {type:'button','class':'call-contact',click:function() { self.number.value = contact.number; self.number.focus(); }}, [
                E('span', {'class':'call-contact-avatar','aria-hidden':'true'},Array.from(contact.name || '?')[0]),
                E('span', {'class':'call-contact-text'},[E('strong',{},contact.name),E('small',{},contact.label.slice(contact.name.length+3))]),icon('arrow')]);
        }) : [E('p',{'class':'call-muted'},this.contactSearch.value ? '검색 결과가 없습니다.' : '저장된 연락처가 없습니다.')]);
    },
    paintHistory: function() {
        var self=this,signature=JSON.stringify(this.state.history);
        if (signature===this.lastHistory) return;
        this.lastHistory=signature;
        this.historyList.replaceChildren.apply(this.historyList,this.state.history.length ? this.state.history.map(function(row) {
            var item=self.model.historyItem(row);
            return E('button',{type:'button','class':'call-contact'+(item.missed ? ' call-missed' : ''),click:function() {self.number.value=row.peer;self.number.focus();}},[
                E('span',{'class':'call-contact-avatar','aria-hidden':'true'},icon('phone')),
                E('span',{'class':'call-contact-text'},[E('strong',{},item.title),E('small',{},[item.status,item.duration,item.sim].filter(Boolean).join(' · '))]),
                E('time',{'class':'call-history-time',datetime:row.started_at},item.date)]);
        }) : [E('p',{'class':'call-muted'},'통화 기록이 없습니다.')]);
        this.historyMore.hidden=!this.state.historyMore;
    },
    showSide: function(tab) {
        this.sideTab=tab;
        this.historyList.hidden=this.historyMore.hidden=tab!=='history';
        if (tab==='history') this.historyMore.hidden=!this.state.historyMore;
        this.contactSearch.hidden=this.contactList.hidden=this.manageContacts.hidden=tab!=='contacts';
        this.historyTab.setAttribute('aria-pressed',String(tab==='history'));
        this.contactTab.setAttribute('aria-pressed',String(tab==='contacts'));
    },
    paint: function() {
        var call = this.state.call, screen = this.model.presentation(this.state), permitted = L.hasViewPermission();
        this.root.classList.toggle('call-ongoing',screen.active);
        this.root.classList.toggle('call-connected',screen.connected);
        this.status.textContent = screen.status;
        this.peer.textContent = screen.active ? this.model.peerLabel(this.state) || '연결 준비 중' : '전화';
        this.duration.textContent = screen.duration; this.duration.hidden = !screen.connected && !(screen.idle && call.duration);
        this.avatar.textContent = '';
        var name = this.model.peerLabel(this.state);
        if (screen.active && name && name !== call.peer) this.avatar.textContent = Array.from(name)[0];
        else this.avatar.appendChild(icon('phone'));
        this.detail.textContent = call.ready ? 'VoLTE · SIM ' + (this.state.sim.slot || '') : '음성 연결 대기';
        this.error.textContent = this.state.error || (call.error ? '통화를 연결하지 못했습니다.' : '');
        this.notice.textContent = screen.locked ? '다른 창에서 통화 중입니다.' : screen.active ? '이 화면을 벗어나면 통화가 종료됩니다.' : '';
        this.dialPanel.hidden = this.contactsPanel.hidden = screen.active;
        this.number.disabled = !screen.idle || this.state.pending;
        this.paintContacts();
        this.paintHistory();this.showSide(this.sideTab || 'history');
        var devices = call.bluetooth && call.bluetooth.devices || [], signature = JSON.stringify(devices);
        if (signature !== this.lastDevices) {
            this.lastDevices = signature;
            this.route.replaceChildren.apply(this.route,[E('option',{value:'browser'},'브라우저 마이크·스피커')].concat(devices.map(function(device) {
                return E('option',{value:'bluetooth:'+device.id},'A8 블루투스 · '+device.label);
            })));
            this.route.value = this.state.route;
        }
        this.routePanel.hidden = screen.active && !screen.incoming;
        this.route.disabled = this.state.pending || screen.locked;
        var microphones=this.state.microphones, microphoneSignature=JSON.stringify(microphones);
        if (microphoneSignature!==this.lastMicrophones) {
            this.lastMicrophones=microphoneSignature;
            this.microphone.replaceChildren.apply(this.microphone,[E('option',{value:''},'시스템 기본 마이크')].concat(microphones.map(function(device) {return E('option',{value:device.id},device.label);})));
        }
        this.microphone.value=this.state.microphone;
        this.microphone.disabled=this.state.pending || this.state.microphonePending || this.state.microphoneTesting || screen.locked;
        this.microphoneTest.disabled=screen.active || this.state.microphonePending || this.state.microphoneTesting || screen.locked;
        this.microphoneTest.hidden=screen.active;
        this.microphoneTest.textContent=this.state.microphoneTesting ? '확인 중…' : '마이크 확인';
        this.microphonePanel.hidden=this.state.route!=='browser' || screen.locked;
        var microphone=this.model.microphonePresentation(this.state);
        this.microphoneLevel.value=microphone.level;
        this.microphoneLevel.hidden=this.microphoneHint.hidden=!(screen.active && !screen.incoming || this.state.microphoneTesting || this.state.microphoneTest);
        this.microphoneHint.textContent=microphone.hint;
        this.audio.textContent = call.audio === 'bluetooth' ? 'A8 블루투스 헤드셋' : '브라우저 마이크·스피커';
        this.audio.hidden = !screen.active || screen.incoming;
        this.answer.hidden = !screen.incoming || screen.locked;
        this.answer.disabled = this.state.pending || this.state.microphoneTesting || !permitted;
        this.hangup.hidden = !screen.active || screen.locked;
        this.hangup.disabled = this.state.canceling || call.state==='ending' || !permitted;
        this.hangup.lastChild.textContent = screen.incoming ? '거절' : '통화 종료';
        this.dial.disabled = !call.ready || !screen.idle || screen.locked || this.state.pending || this.state.microphoneTesting || !permitted;
    },
    render: function() {
        var self = this;
        this.status = E('span',{'class':'call-status','role':'status','aria-live':'polite'});
        this.peer = E('h3',{'class':'call-peer'}); this.detail = E('p',{'class':'call-muted'});
        this.avatar = E('div',{'class':'call-avatar','aria-hidden':'true'});
        this.duration = E('time',{'class':'call-duration','aria-label':'통화 시간'});
        this.audio = E('p',{'class':'call-muted'});
        this.error = E('p',{'class':'call-error','role':'alert'}); this.notice = E('p',{'class':'call-notice'});
        this.number = E('input',{type:'tel',inputmode:'tel',autocomplete:'tel',placeholder:'전화번호','aria-label':'전화번호',
            value:new URLSearchParams(window.location.search).get('to') || ''});
        this.contactSearch = E('input',{type:'search',placeholder:'이름 또는 전화번호','aria-label':'연락처 검색',input:function() { self.paintContacts(); }});
        this.contactList = E('div',{'class':'call-contacts-list'});
        this.historyList=E('div',{'class':'call-contacts-list call-history-list'});
        this.historyMore=E('button',{type:'button','class':'call-button',click:function() {self.action(function() {return self.model.moreHistory(self.state);});}},'이전 통화 더 보기');
        this.historyTab=E('button',{type:'button',click:function() {self.showSide('history');}},'최근 통화');
        this.contactTab=E('button',{type:'button',click:function() {self.showSide('contacts');}},'연락처');
        this.manageContacts=E('a',{href:L.url('admin','messages')+'?tab=contacts'},'연락처 관리');
        this.dial = action('전화 걸기','phone',ui.createHandlerFn(this,function() { return self.action(function() { return self.model.dial(self.state,self.number.value); }); }),{'class':'call-button call-positive'});
        this.answer = action('받기','phone',ui.createHandlerFn(this,function() { return self.action(function() { return self.model.answer(self.state); }); }),{'class':'call-button call-positive'});
        this.hangup = action('통화 종료','hangup',ui.createHandlerFn(this,function() { return self.action(function() { return self.model.hangup(self.state); }); }),{'class':'call-button call-negative'});
        this.route = E('select',{'aria-label':'통화 음성 장치',change:function() { self.state.route = self.route.value; self.paint(); }});
        this.routePanel = E('label',{'class':'call-route'},[E('span',{},'음성 장치'),this.route]);
        this.microphone=E('select',{'aria-label':'브라우저 마이크',change:function() {var selected=self.microphone.value;self.action(function() {return self.model.selectMicrophone(self.state,selected);});}});
        this.microphoneTest=E('button',{type:'button','class':'call-button',click:function() {self.action(function() {return self.model.testMicrophone(self.state);});}},'마이크 확인');
        this.microphoneLevel=E('meter',{min:0,max:1,value:0,'aria-label':'마이크 입력 음량'});
        this.microphoneHint=E('small',{'class':'call-muted','role':'status'});
        this.microphonePanel=E('div',{'class':'call-route call-microphone'},[E('span',{},'마이크'),this.microphone,this.microphoneTest,this.microphoneLevel,this.microphoneHint]);
        this.dialPanel = E('div',{'class':'call-dial'},[this.number,this.dial]);
        this.contactsPanel = E('section',{'class':'call-contacts'},[
            E('div',{'class':'call-tabs','aria-label':'전화 목록'},[this.historyTab,this.contactTab]),
            this.contactSearch,this.contactList,this.manageContacts,this.historyList,this.historyMore]);
        this.root = E('div',{'class':'call-page'},[
            E('link',{rel:'stylesheet',href:L.resource('calling.css')+'?v=2026110801'}),
            E('section',{'class':'call-card'},[E('div',{'class':'call-summary'},[
                this.detail,this.avatar,this.peer,this.status,this.duration,this.audio]),
                this.routePanel,this.microphonePanel,this.dialPanel,E('div',{'class':'call-actions'},[this.answer,this.hangup]),this.notice,this.error]),this.contactsPanel]);
        this.paint();
        poll.add(function() { return self.model.refresh(self.state).then(function() { self.paint(); }).catch(function(e) { self.state.error = e.message; self.paint(); }); },1);
        this.model.startAudio(this.state,function(error) { self.state.error = error.message; self.paint(); });
        window.addEventListener('beforeunload',function(event) { self.model.guardLeave(self.state,event); });
        window.addEventListener('pagehide',function() { self.model.close(self.state); },{once:true});
        window.addEventListener('pageshow',function(event) { if (event.persisted) window.location.reload(); });
        return E('div',{'class':'cbi-map'},[E('h2',{},'전화'),this.root]);
    },
    handleSaveApply:null, handleSave:null, handleReset:null
});
