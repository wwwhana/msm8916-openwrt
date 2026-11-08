'use strict';
'require baseclass';
'require messaging';

// Screen state and actions are independent of DOM rendering.
return baseclass.extend({
    create: function() {
        return { snapshot: { threads: [], contacts: [], subscriptions: [], service: {} },
            selected: null, messages: [], more: false, before: 0, draft: '', recipient: '',
            subscription: '', session: '', filter: '', tab: 'chats', busy: false, error: '',
            sendRequest: null, generation: 0, showChat: false, writable: true,
            attachments: [], uploading: false, plan: null, measuredKey: '', measuringKey: '' };
    },
    refresh: function(state) {
        var self = this;
        return messaging.call('snapshot').then(function(data) {
            state.snapshot = data;
            var service = data.service || {};
            if (!state.subscription && service.subscription) {
                state.subscription = service.subscription; state.session = service.session;
            }
            if (service.subscription === state.subscription && service.session !== state.session && !state.uploading)
                self.useCurrentConnection(state);
            if (state.selected) {
                var thread = data.threads.find(function(item) { return item.id === state.selected; });
                if (!thread) { state.selected = null; state.messages = []; }
            }
            return data;
        });
    },
    select: function(state, thread) {
        this.clearAttachments(state);
        state.generation++; state.selected = thread.id; state.recipient = thread.peer;
        state.showChat = true;
        state.subscription = thread.subscription_id;
        state.session = state.snapshot.service.session;
        state.messages = []; state.more = false; state.before = 0; state.draft = ''; state.sendRequest = null;
        return this.loadThread(state, false);
    },
    loadThread: function(state, older) {
        if (!state.selected || !state.showChat) return Promise.resolve();
        var id = state.selected, generation = state.generation;
        var before = older && state.messages.length ? state.messages[0].seq : 0;
        return messaging.call('thread', id, before).then(function(data) {
            if (state.selected !== id || generation !== state.generation || !state.showChat) return;
            var prefix = !older && data.messages.length ? state.messages.filter(function(item) { return item.seq < data.messages[0].seq; }) : [];
            state.messages = older ? data.messages.concat(state.messages) : prefix.concat(data.messages);
            if (older || !prefix.length) state.more = data.more;
            if (state.writable && state.messages.length) return messaging.call('mark_read', id, state.messages[state.messages.length - 1].seq);
        });
    },
    newChat: function(state, number) {
        this.clearAttachments(state);
        state.generation++; state.selected = null; state.recipient = number || '';
        state.showChat = true;
        state.messages = []; state.more = false; state.draft = ''; state.sendRequest = null;
        state.subscription = state.snapshot.service.subscription || '';
        state.session = state.snapshot.service.session || '';
    },
    saveContact: function(state, contact) {
        var self = this;
        return messaging.call('save_contact', contact).then(function() { return self.refresh(state); });
    },
    deleteContact: function(state, id) {
        var self = this;
        return messaging.call('delete_contact', id).then(function() { return self.refresh(state); });
    },
    attachment: function(part, canceled) {
        return messaging.readAttachment(part, canceled);
    },
    clearAttachments: function(state) {
        var subscription = state.subscription, session = state.session;
        state.attachments.forEach(function(part) { messaging.call('delete_upload', subscription, session, part.id).catch(function() {}); });
        state.attachments = []; state.plan = null; state.measuredKey = ''; state.measuringKey = '';
    },
    addAttachment: async function(state, file) {
        var service = state.snapshot.service || {}, policy = service.policy || {};
        if (state.busy || state.uploading || !state.writable || !service.registered || service.mms_ready === false || service.subscription !== state.subscription || service.session !== state.session)
            throw new Error('현재 SIM의 연결을 확인하세요.');
        if (state.attachments.length >= policy.attachments) throw new Error('첨부파일은 최대 ' + policy.attachments + '개까지 보낼 수 있습니다.');
        state.uploading = true;
        var generation = state.generation, subscription = state.subscription, session = state.session;
        var canceled = function() { var now = state.snapshot.service || {}; return state.generation !== generation || now.subscription !== subscription || now.session !== session; };
        try {
            file = await messaging.prepareAttachment(file, policy, state.attachments.reduce(function(total, item) { return total + item.byte_size; }, 0));
            if (canceled()) throw new Error('SIM 또는 대화가 바뀌어 첨부를 취소했습니다.');
            var part = await messaging.uploadAttachment(file, subscription, session, canceled);
            state.attachments.push(part); state.sendRequest = null;
        } finally { state.uploading = false; }
    },
    removeAttachment: function(state, part) {
        state.attachments = state.attachments.filter(function(item) { return item.id !== part.id; });
        state.sendRequest = null;
        return messaging.call('delete_upload', state.subscription, state.session, part.id);
    },
    planKey: function(state) {
        return state.subscription + '\0' + state.session + '\0' + state.draft + '\0' + state.attachments.length + '\0' + state.recipient + '\0' + Boolean((state.snapshot.service || {}).rcs_ready);
    },
    measure: function(state) {
        var key = this.planKey(state), self = this;
        if (state.measuredKey === key && state.plan) return Promise.resolve(state.plan);
        if (state.measuringKey === key && state.measurePromise) return state.measurePromise;
        state.measuringKey = key;
        state.measurePromise = messaging.call('measure', state.subscription, state.session, state.draft, state.attachments.length, 'auto', state.recipient).then(function(plan) {
            if (self.planKey(state) === key) { state.plan = plan; state.measuredKey = key; }
            return plan;
        }).finally(function() { if (state.measuringKey === key) state.measuringKey = ''; });
        return state.measurePromise;
    },
    deleteThread: function(state) {
        var self = this;
        return messaging.call('delete_thread', state.selected).then(function() {
            self.newChat(state, ''); return self.refresh(state);
        });
    },
    canSend: function(state) {
        var service = state.snapshot.service || {};
        return state.writable && !state.busy && !state.uploading && service.registered && service.subscription === state.subscription &&
            service.session === state.session && state.plan && state.plan.valid &&
            (state.plan.kind === 'RCS' ? service.rcs_ready === true : state.plan.kind === 'SMS' ? service.sms_ready !== false : service.mms_ready !== false) &&
            state.measuredKey === this.planKey(state) && state.recipient.length > 0;
    },
    useCurrentConnection: function(state) {
        var service = state.snapshot.service || {};
        if (!service.registered || state.busy || state.sendRequest ||
            (state.subscription && state.subscription !== service.subscription)) return false;
        if (state.attachments.length && state.session !== service.session) {
            this.clearAttachments(state); state.error = '연결이 바뀌었습니다. 첨부파일을 다시 선택하세요.';
        }
        state.subscription = service.subscription; state.session = service.session;
        return true;
    },
    send: function(state) {
        if (!this.canSend(state)) return Promise.reject(new Error('현재 SIM의 문자 연결과 내용을 확인하세요.'));
        var self = this, to = messaging.normalize(state.recipient), text = state.draft;
        var attachments = state.attachments.map(function(part) { return part.id; });
        var key = state.subscription + '\0' + to + '\0' + text + '\0' + attachments.join(',') ;
        if (!state.sendRequest || state.sendRequest.key !== key)
            state.sendRequest = { key: key, id: messaging.requestID() };
        state.busy = true;
        return messaging.call('send', state.subscription, state.session, state.sendRequest.id, to, text, attachments, 'auto').then(function() {
            state.draft = ''; state.attachments = []; state.sendRequest = null;
            return self.refresh(state);
        }).then(function() {
            var thread = state.snapshot.threads.find(function(item) {
                return item.subscription_id === state.subscription && item.peer === to;
            });
            if (thread) { state.selected = thread.id; state.recipient = to; return self.loadThread(state, false); }
        }).finally(function() { state.busy = false; });
    }
});
