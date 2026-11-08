'use strict';
'require view';
'require ui';
'require poll';
'require messaging';
'require messaging_model as model';

function button(label, click, extra) {
    return E('button', Object.assign({ type: 'button', 'class': 'ko-button', click: click }, extra || {}), label);
}
function date(value) {
    var parsed = new Date(value);
    return isNaN(parsed.getTime()) ? '' : parsed.toLocaleString('ko-KR', { month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit' });
}
function time(value) {
    var parsed = new Date(value);
    return isNaN(parsed.getTime()) ? '' : parsed.toLocaleTimeString('ko-KR', { hour: '2-digit', minute: '2-digit', hour12: false });
}
function day(value) {
    var parsed = new Date(value);
    return isNaN(parsed.getTime()) ? '' : parsed.toLocaleDateString('ko-KR', { year: 'numeric', month: 'long', day: 'numeric' });
}
function listDate(value) {
    if (day(value) === day(new Date().toISOString())) return time(value);
    var parsed = new Date(value);
    return isNaN(parsed.getTime()) ? '' : parsed.toLocaleDateString('ko-KR', { month: 'numeric', day: 'numeric' });
}
function numberLabel(value) {
    var number = String(value || '').replace(/^\+82/, '0');
    return number.replace(/^(01\d)(\d{3,4})(\d{4})$/, '$1-$2-$3').replace(/^(02)(\d{3,4})(\d{4})$/, '$1-$2-$3');
}
function icon(name) {
    return E('span', { 'class': 'ko-icon ko-icon-' + name, 'aria-hidden': 'true' });
}
function avatar(name, number) {
    return E('span', { 'class': 'ko-avatar', 'aria-hidden': 'true' },
        name && name !== number ? Array.from(name.trim())[0] || icon('chat') : icon('chat'));
}
function isPhoto(part) {
    return ['image/jpeg', 'image/png', 'image/gif', 'image/webp'].includes(part.mime_type);
}
function messageSubject(message) {
    var subject = String(message.subject || '').trim();
    return message.protocol === 'mms' && /^\[\s*제목\s*없음\s*\]$/.test(subject) ? '' : subject;
}

return view.extend({
    load: function() {
        this.media = new Map(); this.mediaQueue = Promise.resolve();
        this.state = model.create(); this.state.writable = !!L.hasViewPermission();
        if (new URLSearchParams(window.location.search).get('tab') === 'contacts') this.state.tab = 'contacts';
        return model.refresh(this.state).catch(L.bind(function(error) { this.state.error = error.message; }, this));
    },
    action: function(work) {
        var self = this; this.state.error = '';
        return Promise.resolve().then(work).catch(function(error) { self.state.error = error.message; })
            .finally(function() { self.paint(); });
    },
    contactFor: function(number) {
        try { number = messaging.normalize(number); } catch (_) { return null; }
        return this.state.snapshot.contacts.find(function(contact) {
            return contact.addresses.some(function(address) { return address.address === number; });
        });
    },
    openThread: function(thread) {
        return this.action(L.bind(function() {
            var task = model.select(this.state, thread); this.compose.value = ''; this.recipient.value = thread.peer;
            this.lastMessages = ''; this.scrollToEnd = true; this.paint(); return task;
        }, this));
    },
    newChat: function(number) {
        var service = this.state.snapshot.service || {};
        var thread = this.state.snapshot.threads.find(function(item) {
            return item.peer === number && item.subscription_id === service.subscription;
        });
        if (thread) return this.openThread(thread);
        model.newChat(this.state, number); this.state.tab = 'chats';
        this.compose.value = ''; this.recipient.value = number || ''; this.lastMessages = '';
        this.paint(); (number ? this.compose : this.recipient).focus();
    },
    editContact: function(contact) {
        var self = this, rows = [], error = E('p', { 'class': 'ko-error', role: 'alert' });
        var name = E('input', { type: 'text', maxlength: 80, value: contact ? contact.name : '', placeholder: '이름', 'aria-label': '이름', 'class': 'ko-input' });
        var numbers = E('div', { 'class': 'ko-contact-numbers' });
        function add(address) {
            if (rows.length >= 10) return;
            var number = E('input', { type: 'tel', value: address.address || '', placeholder: '전화번호', 'aria-label': '전화번호', 'class': 'ko-input' });
            var label = E('input', { type: 'text', maxlength: 24, value: address.label || '', placeholder: '구분', 'aria-label': '전화번호 구분', 'class': 'ko-input ko-label' });
            var item = { number: number, label: label };
            var row = E('div', { 'class': 'ko-number-row' }, [number, label, button('−', function() {
                if (rows.length === 1) return;
                rows.splice(rows.indexOf(item), 1); row.remove();
            }, { 'aria-label': '번호 삭제' })]);
            rows.push(item); numbers.appendChild(row);
        }
        (contact ? contact.addresses : [{ address: this.state.recipient, label: '' }]).forEach(add);
        var save = button('저장', ui.createHandlerFn(this, function() {
            error.textContent = '';
            return model.saveContact(self.state, { id: contact ? contact.id : '', name: name.value,
                addresses: rows.map(function(row) { return { address: row.number.value, label: row.label.value }; })
            }).then(function() { ui.hideModal(); self.paint(); }).catch(function(failure) { error.textContent = failure.message; });
        }), { 'class': 'ko-button ko-primary' });
        ui.showModal(contact ? '연락처 수정' : '새 연락처', [E('div', { 'class': 'ko-dialog' }, [name, numbers,
            button('+ 번호 추가', function() { add({}); }), error,
            E('div', { 'class': 'ko-dialog-actions' }, [button('취소', ui.hideModal), save])])]);
        name.focus();
    },
    confirmDelete: function(kind, contact) {
        var self = this;
        ui.showModal(kind === 'contact' ? '연락처 삭제' : '대화 삭제', [E('div', { 'class': 'ko-dialog' }, [
            E('p', {}, kind === 'contact' ? '이 연락처를 삭제합니다. 문자 내용은 보관됩니다.' : '이 기기에 저장된 대화 내용을 삭제합니다.'),
            E('div', { 'class': 'ko-dialog-actions' }, [button('취소', ui.hideModal),
                button('삭제', ui.createHandlerFn(this, function() {
                    ui.hideModal(); return self.action(function() {
                        return kind === 'contact' ? model.deleteContact(self.state, contact.id) : model.deleteThread(self.state);
                    });
                }), { 'class': 'ko-button ko-danger' })])])]);
    },
    chatInfo: function() {
        var self = this, contact = this.contactFor(this.state.recipient), children = [
            E('p', { 'class': 'ko-info-number' }, numberLabel(this.state.recipient))
        ];
        var subscription = this.state.snapshot.subscriptions.find(function(item) { return item.id === self.state.subscription; });
        if (subscription) children.push(E('p', { 'class': 'ko-info-subscription' }, subscription.provider + ' · SIM ' + subscription.slot));
        children.push(E('div', { 'class': 'ko-dialog-actions' }, [button('닫기', ui.hideModal)]));
        ui.showModal(contact ? contact.name : '대화 정보', [E('div', { 'class': 'ko-dialog' }, children)]);
    },
    releaseMedia: function(keep) {
        var self = this;
        this.media.forEach(function(entry, id) {
            if (keep.has(id)) return;
            if (self.viewer && self.viewer.partID === id) self.viewer.close();
            entry.canceled = true;
            if (entry.url) URL.revokeObjectURL(entry.url);
            self.media.delete(id);
        });
    },
    mediaURL: function(part) {
        var entry = this.media.get(part.id);
        if (entry) return entry.promise;
        entry = { canceled: false, url: null };
        // Read one visible photo at a time, sharing it with the larger viewer.
        entry.promise = this.mediaQueue.then(function() {
            if (entry.canceled) throw new Error('취소됨');
            return model.attachment(part, function() { return entry.canceled; });
        }).then(function(bytes) {
            if (entry.canceled) throw new Error('취소됨');
            entry.url = URL.createObjectURL(new Blob([bytes], { type: part.mime_type }));
            return entry.url;
        });
        this.mediaQueue = entry.promise.catch(function() {});
        this.media.set(part.id, entry);
        return entry.promise;
    },
    photo: function(part) {
        var self = this, failed = false, followBottom = false, previousTop = 0;
        var image = E('img', { alt: '사진', decoding: 'async',
            load: function() {
                if (followBottom && Math.abs(self.bubbles.scrollTop - previousTop) < 2)
                    self.bubbles.scrollTop = self.bubbles.scrollHeight;
            },
            error: function() { fail(); } });
        image.hidden = true;
        var label = E('span', { 'class': 'ko-photo-status', role: 'status' }, '사진을 불러오는 중…');
        var preview = button([image, label], function() {
            if (!failed) return self.openPhoto(part);
            var entry = self.media.get(part.id);
            if (entry && entry.url) URL.revokeObjectURL(entry.url);
            self.media.delete(part.id); failed = false;
            label.textContent = '사진을 불러오는 중…';
            return show();
        }, { 'class': 'ko-photo', 'aria-label': '사진 확대' });
        function fail() {
            failed = true; image.hidden = true; label.hidden = false;
            label.textContent = '사진을 불러오지 못했습니다. 다시 시도';
            preview.setAttribute('aria-label', '사진 다시 불러오기');
        }
        function show() {
            return self.mediaURL(part).then(function(url) {
                if (!self.bubbles.contains(preview)) return;
                followBottom = self.bubbles.scrollHeight - self.bubbles.scrollTop - self.bubbles.clientHeight < 60;
                previousTop = self.bubbles.scrollTop;
                image.setAttribute('src', url); image.hidden = false; label.hidden = true;
                preview.setAttribute('aria-label', '사진 확대');
            }).catch(function() { if (self.bubbles.contains(preview)) fail(); });
        }
        this.pendingPhotos.push({ node: preview, show: show });
        return preview;
    },
    openPhoto: function(part) {
        var self = this;
        if (this.viewer) this.viewer.close();
        var image = E('img', { alt: '사진', 'class': 'ko-viewer-image', decoding: 'async',
            error: function() { image.hidden = true; status.hidden = false; status.textContent = '사진을 표시할 수 없습니다.'; } });
        image.hidden = true;
        var status = E('p', { role: 'status' }, '사진을 불러오는 중…');
        var save = E('a', { 'class': 'ko-button', download: part.filename || 'photo', rel: 'noopener' }, '파일 저장');
        save.hidden = true;
        var viewer = E('dialog', { 'class': 'ko-media-viewer', 'aria-label': '사진',
            close: function() { if (self.viewer === viewer) self.viewer = null; viewer.remove(); },
            click: function(event) { if (event.target === viewer) viewer.close(); }
        }, [E('div', { 'class': 'ko-viewer-toolbar' }, [save,
            button('×', function() { viewer.close(); }, { 'class': 'ko-button ko-icon-button', 'aria-label': '닫기', autofocus: true })]),
            E('div', { 'class': 'ko-viewer-content' }, [image, status])]);
        viewer.partID = part.id; this.viewer = viewer;
        this.root.appendChild(viewer); viewer.showModal();
        return this.mediaURL(part).then(function(url) {
            if (self.viewer !== viewer) return;
            image.setAttribute('src', url); image.hidden = false; status.hidden = true;
            save.setAttribute('href', url); save.hidden = false;
        }).catch(function(error) { if (self.viewer === viewer) status.textContent = error.message; });
    },
    openAttachment: function(part) {
        if (isPhoto(part)) return this.openPhoto(part);
        var closed = false, objectURL;
        var content = E('div', {}, '첨부파일을 읽는 중입니다.');
        var close = function() {
            closed = true;
            if (objectURL) URL.revokeObjectURL(objectURL);
            ui.hideModal();
        };
        ui.showModal('첨부파일', [E('div', { 'class': 'ko-dialog' }, [content,
            E('div', { 'class': 'ko-dialog-actions' }, [button('닫기', close)])])]);
        return model.attachment(part, function() { return closed; }).then(function(bytes) {
            if (closed) return;
            objectURL = URL.createObjectURL(new Blob([bytes], { type: part.mime_type }));
            var children = [];
            children.push(E('a', { 'class': 'ko-button', href: objectURL,
                download: part.filename || 'attachment', rel: 'noopener' }, '파일 저장'));
            content.replaceChildren.apply(content, children);
        }).catch(function(error) { if (!closed) content.textContent = error.message; });
    },
    render: function() {
        var self = this, state = this.state;
        this.list = E('div', { 'class': 'ko-list' });
        this.header = E('div', { 'class': 'ko-chat-heading' });
        this.bubbles = E('div', { 'class': 'ko-bubbles', role: 'log', 'aria-label': '대화 내용', 'aria-live': 'polite' });
        this.alert = E('p', { 'class': 'ko-error', role: 'alert' });
        this.connection = E('div', { 'class': 'ko-connection' });
        this.counter = E('small', { 'class': 'ko-counter' });
        this.sendKind = E('span', {}, 'SMS');
        this.draftParts = E('div', { 'class': 'ko-draft-parts', 'aria-label': '첨부파일' });
        this.fileInput = E('input', { type: 'file', multiple: true, hidden: true,
            accept: 'image/jpeg,image/png,image/gif,audio/amr,audio/mpeg,video/mp4,video/3gpp',
            change: function() {
                var files = Array.from(self.fileInput.files || []), generation = state.generation;
                self.fileInput.value = '';
                self.action(async function() {
                    for (var file of files) {
                        if (state.generation !== generation) return;
                        var task = model.addAttachment(state, file); self.updateComposer(); await task; self.updateComposer();
                    }
                });
            } });
        this.attach = button(icon('attach'), function() { self.fileInput.click(); },
            { 'class': 'ko-button ko-icon-button', 'aria-label': '사진·파일 첨부', title: '사진·파일 첨부' });
        this.recipient = E('input', { id: 'ko-recipient', type: 'tel', autocomplete: 'tel', 'class': 'ko-input', placeholder: '전화번호', 'aria-label': '받는 번호',
            input: function() { state.recipient = self.recipient.value; self.updateComposer(); } });
        this.recipientRow = E('div', { 'class': 'ko-recipient' }, [E('label', { 'for': 'ko-recipient' }, '받는 사람'), this.recipient]);
        this.compose = E('textarea', { rows: 1, placeholder: '메시지', 'aria-label': '메시지 입력', 'class': 'ko-compose',
            input: function() { state.draft = self.compose.value; self.updateComposer(); },
            keydown: function(event) {
                if (event.key === 'Enter' && (event.ctrlKey || event.metaKey) && !event.isComposing && !self.send.disabled) {
                    event.preventDefault(); self.send.click();
                }
            } });
        this.send = button(icon('send'), ui.createHandlerFn(this, function() {
            var task = self.action(function() { return model.send(state).then(function() { self.compose.value = state.draft; }); });
            self.updateComposer(); return task;
        }), { 'class': 'ko-button ko-primary ko-icon-button', 'aria-label': '문자 보내기', title: '문자 보내기 (Ctrl+Enter)' });
        this.composeStatus = E('div', { 'class': 'ko-compose-status', role: 'status' });
        this.composer = E('div', { 'class': 'ko-composer' }, [this.composeStatus, this.draftParts, this.fileInput,
            E('div', { 'class': 'ko-compose-row' }, [this.attach, this.compose, this.send]),
            E('div', { 'class': 'ko-compose-caption' }, [this.sendKind, this.counter])]);
        var search = E('input', { type: 'search', 'class': 'ko-input', placeholder: '검색', 'aria-label': '대화 및 연락처 검색',
            input: function() { state.filter = search.value; self.paintList(); } });
        this.chatTab = button('대화', function() { state.tab = 'chats'; self.paintList(); });
        this.contactTab = button('연락처', function() { state.tab = 'contacts'; self.paintList(); });
        this.root = E('div', { 'class': 'ko-messenger',
            click: function(event) {
                if (self.options && self.options.open && !self.options.contains(event.target)) self.options.open = false;
            },
            keydown: function(event) {
                if (event.key === 'Escape' && self.options && self.options.open) {
                    self.options.open = false; self.options.firstElementChild.focus();
                }
            } }, [
            E('link', { rel: 'stylesheet', href: L.resource('messaging.css') + '?v=2026110707' }),
            E('aside', { 'class': 'ko-sidebar' }, [
                E('div', { 'class': 'ko-sidebar-heading' }, [E('h2', {}, '메시지'),
                    button(icon('write'), function() { self.newChat(''); }, { 'class': 'ko-button ko-icon-button', 'aria-label': '새 메시지', title: '새 메시지', disabled: !state.writable })]),
                E('div', { 'class': 'ko-search' }, [icon('search'), search]),
                E('nav', { 'class': 'ko-tabs', 'aria-label': '메시지 메뉴' }, [this.chatTab, this.contactTab]),
                this.list,
                E('div', { 'class': 'ko-sidebar-footer' }, [this.connection,
                    E('a', { 'class': 'ko-settings-link', href: L.url('admin', 'modem', 'sms') }, '모뎀 설정')])]),
            E('section', { 'class': 'ko-chat', 'aria-label': '선택한 대화' }, [this.header, this.recipientRow, this.bubbles, this.alert, this.composer])]);
        // visualViewport also shrinks when a phone's keyboard opens.
        var viewport = window.visualViewport;
        if (window.IntersectionObserver) this.photoObserver = new window.IntersectionObserver(function(entries) {
            entries.forEach(function(entry) {
                if (!entry.isIntersecting) return;
                self.photoObserver.unobserve(entry.target); entry.target.loadPhoto();
            });
        }, { root: this.bubbles, rootMargin: '160px' });
        if (window.addEventListener) window.addEventListener('pagehide', function() {
            if (self.photoObserver) self.photoObserver.disconnect();
            self.releaseMedia(new Set());
        }, { once: true });
        this.paint();
        function fitViewport() {
            if (self.root.isConnected === false) return;
            var top = self.root.getBoundingClientRect().top;
            var height = viewport ? viewport.height + viewport.offsetTop : window.innerHeight;
            self.root.style.setProperty('--ko-height', Math.max(220, Math.floor(height - Math.max(0, top) - 12)) + 'px');
        }
        if (window.requestAnimationFrame) window.requestAnimationFrame(fitViewport);
        if (window.addEventListener) window.addEventListener('resize', fitViewport, { passive: true });
        if (viewport) {
            viewport.addEventListener('resize', fitViewport, { passive: true });
            viewport.addEventListener('scroll', fitViewport, { passive: true });
        }
        poll.add(function() {
            if (state.busy || self.refreshing) return Promise.resolve();
            self.refreshing = true;
            return model.refresh(state).then(function() { return model.loadThread(state, false); })
                .then(function() { state.error = ''; })
                .catch(function(error) { state.error = error.message; })
                .finally(function() { self.refreshing = false; self.paint(); });
        }, 3);
        return this.root;
    },
    paintList: function() {
        var self = this, state = this.state, filter = state.filter.toLocaleLowerCase(), children = [];
        this.chatTab.classList.toggle('ko-active', state.tab === 'chats');
        this.contactTab.classList.toggle('ko-active', state.tab === 'contacts');
        this.chatTab.setAttribute('aria-pressed', state.tab === 'chats');
        this.contactTab.setAttribute('aria-pressed', state.tab === 'contacts');
        if (state.tab === 'contacts') {
            if (state.writable) children.push(button('+ 새 연락처', function() { self.editContact(null); }));
            var contacts = state.snapshot.contacts.filter(function(contact) {
                return (contact.name + ' ' + contact.addresses.map(function(a) { return a.address; }).join(' ')).toLocaleLowerCase().includes(filter);
            });
            contacts.forEach(function(contact) {
                var details = contact.addresses.map(function(address) {
                    return button((address.label ? address.label + ' · ' : '') + address.address, function() { self.newChat(address.address); });
                });
                var controls = [];
                if (state.writable) controls = [button('수정', function() { self.editContact(contact); }),
                    button('삭제', function() { self.confirmDelete('contact', contact); })];
                children.push(E('div', { 'class': 'ko-contact' }, [E('div', { 'class': 'ko-contact-heading' }, [avatar(contact.name), E('strong', {}, contact.name)]),
                    E('div', { 'class': 'ko-contact-addresses' }, details), E('div', { 'class': 'ko-contact-actions' }, controls)]));
            });
            if (!contacts.length) children.push(E('p', { 'class': 'ko-empty' }, filter ? '검색 결과가 없습니다.' : '저장된 연락처가 없습니다.'));
        } else {
            var threads = state.snapshot.threads.filter(function(thread) {
                return (thread.title + ' ' + thread.peer + ' ' + (thread.preview || '')).toLocaleLowerCase().includes(filter);
            });
            threads.forEach(function(thread) {
                var title = E('div', { 'class': 'ko-thread-title' }, [E('strong', {}, thread.title === thread.peer ? numberLabel(thread.peer) : thread.title),
                    E('time', { datetime: thread.updated_at, title: date(thread.updated_at) }, listDate(thread.updated_at))]);
                var preview = [E('span', {}, thread.preview || (thread.protocol === 'mms' ? messaging.mmsStateLabel(thread.state) : '첨부파일'))];
                if (thread.unread) preview.push(E('span', { 'class': 'ko-unread' }, String(thread.unread)));
                children.push(button('', function() { return self.openThread(thread); }, {
                    'class': 'ko-thread' + (state.selected === thread.id ? ' ko-selected' : ''),
                    'aria-label': thread.title,
                    'aria-current': state.selected === thread.id ? 'true' : 'false'
                }));
                var subscription = state.snapshot.subscriptions.find(function(item) { return item.id === thread.subscription_id; });
                children[children.length - 1].appendChild(avatar(thread.title, thread.peer));
                children[children.length - 1].appendChild(E('div', { 'class': 'ko-thread-content' }, [title,
                    E('div', { 'class': 'ko-thread-preview' }, preview),
                    E('small', { 'class': 'ko-sim' }, subscription ? subscription.provider + ' · SIM ' + subscription.slot : '보관된 대화')]));
            });
            if (!threads.length) children.push(E('p', { 'class': 'ko-empty' }, filter ? '검색 결과가 없습니다.' : '새 대화를 시작해 보세요.'));
        }
        this.list.replaceChildren.apply(this.list, children);
    },
    updateComposer: function() {
        var self = this, state = this.state, service = state.snapshot.service || {}, notice = [];
        this.send.disabled = !model.canSend(state);
        this.compose.disabled = !state.writable || state.busy;
        this.recipient.disabled = !state.writable || !!state.selected || state.busy;
        this.recipientRow.hidden = !state.showChat || !!state.selected;
        var measured = state.plan && state.measuredKey === model.planKey(state);
        this.sendKind.textContent = measured ? (state.plan.kind === 'RCS' ? 'RCS 메시지' : '문자 메시지') : '';
        this.compose.placeholder = measured && state.plan.kind === 'RCS' ? 'RCS 메시지' : '문자 메시지';
        this.counter.hidden = measured && state.plan.kind === 'RCS';
        this.counter.textContent = measured ? state.plan.bytes + ' / ' + state.plan.limit + ' B' : '길이 확인 중';
        this.counter.classList.toggle('ko-over-limit', !!(measured && state.plan.reason));
        this.draftParts.replaceChildren.apply(this.draftParts, state.attachments.map(function(part) {
            return E('div', { 'class': 'ko-draft-part' }, [E('span', {}, part.filename),
                button('×', function() { self.action(function() { return model.removeAttachment(state, part); }); },
                    { 'aria-label': part.filename + ' 첨부 취소', disabled: state.busy || state.uploading })]);
        }));
        this.draftParts.hidden = !state.attachments.length;
        this.attach.disabled = !state.writable || state.busy || state.uploading || !service.registered || service.mms_ready === false ||
            service.subscription !== state.subscription || service.session !== state.session ||
            state.attachments.length >= ((service.policy || {}).attachments || 0);
        clearTimeout(this.measureTimer);
        if (!measured && state.showChat && service.registered && service.subscription === state.subscription && service.session === state.session) {
            this.measureTimer = setTimeout(function() {
                model.measure(state).then(function() { self.updateComposer(); }).catch(function(error) { state.error = error.message; self.alert.textContent = error.message; self.alert.hidden = false; });
            }, 180);
        }
        this.composer.hidden = !state.showChat;
        if (state.uploading) notice = [E('span', {}, '첨부파일 저장 중…')];
        else if (measured && state.plan.reason) notice = [E('span', {}, '문자 길이를 줄여 주세요.')];
        else if (!state.writable) notice = [E('span', {}, '읽기 전용입니다.')];
        else if (!service.registered) notice = [E('span', {}, '문자 연결 대기 중입니다.'),
            E('a', { href: (L.url('admin', 'status', 'modemmanager') + '#ims') }, '연결 상태')];
        else if (state.subscription && service.subscription !== state.subscription)
            notice = [E('span', {}, '이 대화의 SIM이 연결되어 있지 않습니다.'),
                button('현재 SIM으로 새 대화', function() { self.newChat(state.recipient); })];
        else if (service.session !== state.session) {
            if (state.sendRequest) notice = [E('span', {}, '이전 메시지의 전송 결과를 확인해 주세요.')];
        }
        else if (measured && (state.plan.kind === 'RCS' ? service.rcs_ready !== true : state.plan.kind === 'SMS' ? service.sms_ready === false : service.mms_ready === false))
            notice = [E('span', {}, '메시지 연결을 확인해 주세요.'),
                E('a', { href: (L.url('admin', 'status', 'modemmanager') + '#ims') }, '연결 상태')];
        this.composeStatus.replaceChildren.apply(this.composeStatus, notice);
        this.composeStatus.hidden = !notice.length;
        this.compose.style.setProperty('height', 'auto', 'important');
        this.compose.style.setProperty('height', Math.min(128, Math.max(46, this.compose.scrollHeight)) + 'px', 'important');
    },
    paint: function() {
        var self = this, state = this.state, service = state.snapshot.service || {};
        this.root.classList.toggle('ko-open-chat', state.showChat);
        this.alert.textContent = state.error;
        this.connection.textContent = service.registered ? 'SIM ' + service.slot + ' · 연결됨' : 'SMS 연결 대기';
        this.connection.classList.toggle('ko-connected', !!service.registered);
        this.paintList(); this.updateComposer();
        var contact = this.contactFor(state.recipient), title = contact ? contact.name : numberLabel(state.recipient) || '새 메시지';
        var subscription = state.snapshot.subscriptions.find(function(item) { return item.id === state.subscription; });
        var subtitle = subscription ? subscription.provider + ' · SIM ' + subscription.slot : 'SMS';
        if (contact) subtitle = numberLabel(state.recipient) + ' · ' + subtitle;
        var headerSignature = JSON.stringify([state.showChat, state.selected, title, subtitle]);
        if (headerSignature !== this.lastHeader) {
            this.lastHeader = headerSignature;
            this.options = null;
            var actions = [button(icon('back'), function() { state.showChat = false; self.lastMessages = ''; self.paint(); },
                { 'class': 'ko-button ko-icon-button ko-back', 'aria-label': '대화 목록으로', title: '대화 목록으로' })];
            if (state.showChat) actions.push(avatar(title, numberLabel(state.recipient)));
            actions.push(E('div', { 'class': 'ko-chat-title' }, [E('strong', {}, state.showChat ? title : '메시지'),
                E('small', {}, state.showChat ? subtitle : 'SMS · 연락처')]));
            if (state.showChat && state.recipient) {
                if (state.writable) actions.push(E('a', {'class':'ko-button', href:L.url('admin','calls')+'?to='+encodeURIComponent(state.recipient), 'aria-label':title+'에게 전화'}, '전화'));
                var details = E('details', { 'class': 'ko-chat-options' });
                this.options = details;
                var options = [button('대화 정보', function() { details.open = false; self.chatInfo(); }, { 'class': 'ko-button ko-menu-item' })];
                if (state.writable) {
                    options.push(button(contact ? '연락처 수정' : '연락처 저장', function() {
                        details.open = false; self.editContact(contact);
                    }, { 'class': 'ko-button ko-menu-item' }));
                    if (state.selected) options.push(button('대화 삭제', function() {
                        details.open = false; self.confirmDelete('thread');
                    }, { 'class': 'ko-button ko-menu-item ko-danger' }));
                }
                details.appendChild(E('summary', { 'class': 'ko-button ko-icon-button', 'aria-label': '대화 메뉴', title: '대화 메뉴' }, icon('more')));
                details.appendChild(E('div', { 'class': 'ko-chat-menu' }, options));
                actions.push(details);
            }
            this.header.replaceChildren.apply(this.header, actions);
        }
        var signature = JSON.stringify([state.selected, state.messages, state.more, state.showChat]);
        if (signature === this.lastMessages) return;
        this.lastMessages = signature;
        var keptMedia = new Set();
        (state.showChat ? state.messages : []).forEach(function(message) {
            if (!message.canceled_at) (message.parts || []).filter(isPhoto).forEach(function(part) { keptMedia.add(part.id); });
        });
        this.releaseMedia(keptMedia);
        if (this.photoObserver) this.photoObserver.disconnect();
        this.pendingPhotos = [];
        var nearBottom = this.bubbles.scrollHeight - this.bubbles.scrollTop - this.bubbles.clientHeight < 60;
        var oldHeight = this.bubbles.scrollHeight, oldTop = this.bubbles.scrollTop;
        var content = [], previousDay = '';
        if (state.showChat && state.messages.length) content.push(E('div', { 'class': 'ko-bubble-spacer', 'aria-hidden': 'true' }));
        if (state.more) content.push(button('이전 메시지', function() { return self.action(function() { return model.loadThread(state, true); }); }, { 'class': 'ko-button ko-older' }));
        (state.showChat ? state.messages : []).forEach(function(message) {
            var currentDay = day(message.timestamp);
            if (currentDay && currentDay !== previousDay) {
                content.push(E('div', { 'class': 'ko-day' }, currentDay)); previousDay = currentDay;
            }
            var outgoing = message.direction === 'outgoing';
            var meta = time(message.timestamp);
            if (outgoing) meta += ' · ' + messaging.stateLabel(message.state);
            var body = [], subject = messageSubject(message), photos = (message.parts || []).filter(isPhoto);
            if (subject && !message.canceled_at) body.push(E('strong', { 'class': 'ko-message-subject' }, subject));
            if (message.protocol === 'mms' && messaging.mmsStateLabel(message.state) !== 'MMS')
                body.push(E('small', { 'class': 'ko-attachment-label', role: 'status' }, messaging.mmsStateLabel(message.state)));
            (message.canceled_at ? [] : message.parts || []).forEach(function(part) {
                if (isPhoto(part)) { body.push(self.photo(part)); return; }
                body.push(button('첨부파일 · ' + (part.filename || '파일'),
                    function() { return self.openAttachment(part); }, { 'class': 'ko-button ko-attachment' }));
            });
            if (message.canceled_at || message.body)
                body.push(E('span', { 'class': 'ko-message-text' }, message.canceled_at ? '취소된 메시지입니다' : message.body));
            if (message.attachments && !(message.parts || []).length)
                body.push(E('small', { 'class': 'ko-attachment-label' }, '첨부파일 ' + message.attachments + '개'));
            var kind = message.protocol === 'mms' && !message.attachments && message.body ? 'LMS' : message.protocol.toUpperCase();
            body.push(E('small', { 'class': 'ko-message-meta', title: date(message.timestamp) + ' · ' + kind }, meta));
            var photoOnly = photos.length === 1 && (message.parts || []).length === 1 && !subject && !message.body && !message.canceled_at;
            content.push(E('article', { 'class': 'ko-bubble' + (outgoing ? ' ko-outgoing' : '') +
                (photos.length && !message.canceled_at ? ' ko-has-photo' : '') + (photoOnly ? ' ko-photo-only' : '') }, body));
        });
        if (!content.length) content.push(E('div', { 'class': 'ko-welcome' }, [icon('chat'),
            E('strong', {}, state.showChat ? '새 메시지' : '내 메시지'),
            E('p', {}, state.showChat ? '받는 사람을 확인하고 메시지를 작성하세요.' : '대화를 선택하거나 새 메시지를 작성하세요.')]));
        this.bubbles.replaceChildren.apply(this.bubbles, content);
        this.pendingPhotos.forEach(function(photo) {
            photo.node.loadPhoto = photo.show;
            if (self.photoObserver) self.photoObserver.observe(photo.node);
            else photo.show();
        });
        if (nearBottom || !oldHeight || this.scrollToEnd) this.bubbles.scrollTop = this.bubbles.scrollHeight;
        else this.bubbles.scrollTop = oldTop + Math.max(0, this.bubbles.scrollHeight - oldHeight);
        this.scrollToEnd = false;
    },
    handleSaveApply: null,
    handleSave: null,
    handleReset: null
});
