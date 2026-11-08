'use strict';
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');
const root = path.join(__dirname, '../../packages/luci-app-sms-manager/htdocs/luci-static/resources');
const load = (file, context) => vm.runInNewContext('(function(){' + fs.readFileSync(path.join(root, file), 'utf8') + '})()', context);
const sim = '1'.repeat(32), session = '2'.repeat(32), thread = '3'.repeat(64);
let requestSerial = 0, sendCalls = [], reads = 0, failure = false;
const attachment = Uint8Array.from({length: 60000}, (_,index) => index % 256);
let partReads = 0, invalidPartOffset = false;
const body = '<img src=x onerror=alert(1)> 한글\n둘째 줄';
let data = { threads: [{id: thread, subscription_id: sim, peer: '+821000000001', title: '연락처', preview: body, unread: 1, updated_at: '2026-11-07T10:00:00Z'}],
    contacts: [{id: '4'.repeat(32), name: '연락처', addresses: [{address: '+821000000001', label: '휴대전화'}, {address: '+8221234567', label: '집'}]}],
    subscriptions: [{id: sim, slot: 1, provider: 'LG U+'}], service: {registered: true, subscription: sim, session, slot: 1, policy: {provider:"LG U+", message_bytes:1048576, image_width:2592, image_height:1944, attachments:3}} };
const clone = value => JSON.parse(JSON.stringify(value));
const messaging = load('messaging.js', {baseclass: {extend: o => o}, rpc: {declare: spec => async (...args) => {
    switch (spec.method) {
    case 'snapshot': return clone(data);
    case 'thread': return {messages: [{seq: 1, id: 'message', body, protocol: 'sms', direction: 'incoming', timestamp: '2026-11-07T10:00:00Z', attachments: 0}], more: false};
    case 'mark_read': reads++; return {ok: true};
    case 'read_part': {
        partReads++;
        return {offset: args[1] + (invalidPartOffset ? 1 : 0), size: attachment.length,
            mime_type: 'image/png', data: Buffer.from(attachment.slice(args[1], args[1] + 24 * 1024)).toString('base64')};
    }
    case 'measure': { const value = args[2], count = args[3], unicode = /[^\x00-\x7f]/.test(value); const bytes = unicode ? value.length * 2 : Math.ceil(value.length * 7 / 8); const chat = data.service.rcs_ready && args[5] === '+821000000001' && !count; const kind = chat ? 'RCS' : count ? 'MMS' : bytes > 140 || value.length > 80 ? 'LMS' : 'SMS'; const measured = kind === 'SMS' ? bytes : unicode ? value.length * 2 : value.length; const limit = chat ? 8192 : kind === 'SMS' ? 140 : 2000; return {kind, bytes:measured, limit, valid: (!!value.trim() || count > 0) && measured <= limit, reason:measured > limit ? 'message-too-long' : ''}; }
    case 'send': sendCalls.push(args); if (failure) throw new Error('connection lost'); return {id: 'message'};
    case 'save_contact': assert.equal(args[0].addresses.length, 2); return {id: 'contact'};
    case 'delete_contact': return {ok: true};
    case 'delete_upload': return {ok: true};
    default: throw new Error('unexpected method');
    }
}}, window: {crypto: {getRandomValues: bytes => {bytes.fill(++requestSerial);}}}, Uint8Array, atob});
const model = load('messaging_model.js', {baseclass: {extend: o => o}, messaging});
const sms = load('sms_manager.js', {baseclass: {extend: o => o}, fs: {}});

function E(tag, attrs, children) {
    const classes = new Set((attrs && attrs.class || '').split(/\s+/));
    const node = {tag, attrs: attrs || {}, children: children == null ? [] : Array.isArray(children) ? children : [children], value: attrs && attrs.value || '',
        hidden: false, disabled: false, scrollHeight: 100, scrollTop: 0, clientHeight: 100,
        classList: {toggle(name, on){if (on) classes.add(name); else classes.delete(name);}, contains: name => classes.has(name)},
        style: {setProperty(name,value){this[name] = value;}},
        getBoundingClientRect(){return {top: 100};}, setAttribute(name,value){this.attrs[name] = String(value);},
        contains(item){return item === this || this.children.some(child => child && typeof child === 'object' && child.contains(item));},
        focus(){}, remove(){this.removed = true;},
        showModal(){this.open = true;}, close(){this.open = false; if (this.attrs.close) this.attrs.close();},
        appendChild(item){this.children.push(item);}, replaceChildren(...items){this.children = items;}};
    Object.defineProperty(node, 'innerHTML', {set(){throw new Error('user content parsed as HTML');}});
    return node;
}
function text(node) {
    if (node == null) return '';
    if (Array.isArray(node)) return node.map(text).join('\n');
    return typeof node === 'object' ? (node.textContent || '') + text(node.children) : String(node);
}
async function main() {
    const nas = model.create();
    nas.snapshot.service = {registered: true, ims_registered: false, sms_transport: 'nas', sms_ready: true, mms_ready: true,
        subscription: sim, session};
    nas.subscription = sim; nas.session = session; nas.recipient = '+821000000001'; nas.draft = '한글';
    nas.plan = {kind: 'SMS', valid: true}; nas.measuredKey = model.planKey(nas);
    assert(model.canSend(nas), 'NAS send depends on IMS registration');
    nas.snapshot.service.sms_ready = false;
    assert(!model.canSend(nas), 'unready NAS SMS can send');
    nas.plan.kind = 'LMS';
    assert(model.canSend(nas), 'LMS incorrectly depends on SMS readiness');
    nas.snapshot.service.mms_ready = false;
    assert(!model.canSend(nas), 'unready MMS can send');
    let state = model.create(); await model.refresh(state); await model.select(state, data.threads[0]);
    assert.equal(state.messages[0].body, body); assert.equal(reads, 1);
    state.draft = '보관할 입력'; await model.refresh(state); await model.loadThread(state, false);
    assert.equal(state.draft, '보관할 입력', 'poll discarded unsent draft');
    await model.measure(state); assert(model.canSend(state));
    data.service.subscription = '9'.repeat(32); await model.refresh(state);
    assert(!model.canSend(state), 'another SIM enabled old draft');
    assert(!model.useCurrentConnection(state), 'reconnect adopted another SIM');
    data.service.subscription = sim; data.service.session = '8'.repeat(32); await model.refresh(state);
    assert.equal(state.session,data.service.session,'same-SIM reconnect still needed a manual action');
    assert(!model.canSend(state), 'another IMS session enabled stale draft');
    assert(model.useCurrentConnection(state), 'same-SIM connection could not be explicitly refreshed');
    await model.measure(state); assert(model.canSend(state));
    model.newChat(state, '+821000000001'); state.draft = '한 번만 전송';
    await model.measure(state); failure = true; await assert.rejects(model.send(state), /기기와 연결/);
    assert.equal(state.draft, '한 번만 전송');
    assert(!model.useCurrentConnection(state), 'uncertain submission lost its request guard');
    failure = false; await model.send(state);
    assert.equal(sendCalls.length, 2); assert.equal(sendCalls[0][2], sendCalls[1][2], 'retry created a second network request');
    assert.equal(state.draft, '');
    state.writable = false; const before = reads; await model.loadThread(state, false); assert.equal(reads, before);
    state.draft = '입력'; assert(!model.canSend(state), 'read-only user can send');
    assert.equal(messaging.normalize('02-123-4567'), '+8221234567');
    assert.throws(() => messaging.normalize('010;DROP TABLE'), /전화번호/);
    const part = {id: 'a'.repeat(64), mime_type: 'image/png', filename: '<script>.png', byte_size: attachment.length};
    assert.deepEqual(await model.attachment(part), attachment);
    assert.equal(partReads, 3, 'attachment chunks were lost');
    invalidPartOffset = true;
    await assert.rejects(model.attachment(part), /정보가 바뀌/);
    invalidPartOffset = false;
    await assert.rejects(model.attachment(part, () => true), /취소/);
    let uploadChunks = [], uploadDeletes = 0;
    const uploadAPI = load('messaging.js', {baseclass:{extend:o=>o},Uint8Array,atob,btoa,
        window:{crypto:{getRandomValues:bytes=>bytes.fill(1)}}, rpc:{declare:spec=>async(...args)=>{
            if(spec.method==='begin_upload') return {id:'f'.repeat(64),offset:0};
            if(spec.method==='delete_upload'){uploadDeletes++;return {ok:true};}
            assert.equal(spec.method,'write_upload');
            const chunk=Buffer.from(args[4],'base64'); uploadChunks.push(chunk);
            assert(chunk.length<=8192); assert.equal(args[0],sim);assert.equal(args[1],session);
            return {id:args[2],offset:args[3]+chunk.length,complete:args[3]+chunk.length===attachment.length};
        }}});
    const uploadFile={name:'사진.png',type:'image/png',size:attachment.length,arrayBuffer:async()=>attachment.buffer};
    const uploaded=await uploadAPI.uploadAttachment(uploadFile,sim,session,()=>false);
    assert.equal(uploaded.byte_size,attachment.length); assert.equal(uploadChunks.length,8);
    assert.deepEqual(Buffer.concat(uploadChunks),Buffer.from(attachment));
    await assert.rejects(uploadAPI.uploadAttachment(uploadFile,sim,session,()=>true),/취소/);
    assert.equal(uploadDeletes,1,'canceled attachment was retained');
    const draft=model.create();await model.refresh(draft);model.newChat(draft,'01000000001');
    const prepare=messaging.prepareAttachment,upload=messaging.uploadAttachment;
    messaging.prepareAttachment=async file=>file;messaging.uploadAttachment=async()=>uploaded;
    await model.addAttachment(draft,uploadFile);await model.measure(draft);
    assert(model.canSend(draft),'photo-only MMS cannot send');
    assert.equal(draft.plan.kind,'MMS');
    data.service.subscription='9'.repeat(32);await model.refresh(draft);
    assert(!model.canSend(draft));await assert.rejects(model.addAttachment(draft,uploadFile),/SIM/);
    data.service.subscription=sim;await model.refresh(draft);
    await model.removeAttachment(draft,uploaded);assert.equal(draft.attachments.length,0);
    messaging.prepareAttachment=prepare;messaging.uploadAttachment=upload;
    let polling, modal, viewportEvents = {}, windowEvents = {}, createdURLs = [], revokedURLs = [];
    const viewport = {height: 800, offsetTop: 0, addEventListener: (name,fn) => {viewportEvents[name] = fn;}};
    const page = load('view/modem/messages.js', {view: {extend: o => o}, model, messaging, sms, E, URLSearchParams, Blob, setTimeout: () => 1, clearTimeout: () => {},
        URL: {createObjectURL: () => {const url = 'blob:test-' + createdURLs.length; createdURLs.push(url); return url;}, revokeObjectURL: url => revokedURLs.push(url)},
        window: {location: {search: ''}, visualViewport: viewport, requestAnimationFrame: fn => fn(),
            addEventListener: (name, callback) => {windowEvents[name] = callback;},
            IntersectionObserver: class {
                constructor(callback){this.callback = callback; this.nodes = new Set();}
                observe(node){this.nodes.add(node);} unobserve(node){this.nodes.delete(node);} disconnect(){this.nodes.clear();}
            }},
        poll: {add: callback => {polling = callback;}},
        ui: {createHandlerFn: (self, fn) => fn.bind(self), showModal: (title, children) => {modal = {title, children};}, hideModal(){}},
        L: {hasViewPermission: () => true, bind: (fn, self) => fn.bind(self), resource: p => '/' + p, url: (...p) => '/' + p.join('/')}});
    await page.load(); const tree = page.render(); await page.openThread(data.threads[0]);
    assert(text(tree).includes(body), 'stored SQLite text missing from conversation');
    assert(!text(tree).includes('null'), 'null leaked into screen');
    assert(page.root.classList.contains('ko-open-chat'));
    assert(page.recipientRow.hidden, 'existing conversation repeats its phone-number input');
    await model.measure(page.state); page.updateComposer(); assert.equal(page.counter.textContent, '0 / 140 B');
    page.compose.value = '한'.repeat(71); page.compose.attrs.input();
    await model.measure(page.state); page.updateComposer(); assert.equal(page.counter.textContent, '142 / 2000 B'); assert.equal(page.sendKind.textContent, '문자 메시지');
    assert.equal(page.protocol, undefined, 'composer exposes a manual transport selector');
    data.service.rcs_ready = true; await model.refresh(page.state); await model.measure(page.state); page.updateComposer();
    assert.equal(page.sendKind.textContent, 'RCS 메시지'); assert(page.counter.hidden);
    assert.equal(page.compose.placeholder, 'RCS 메시지');
    page.state.recipient = '+821000000002'; await model.measure(page.state); page.updateComposer();
    assert.equal(page.sendKind.textContent, '문자 메시지', 'recipient change retained the previous RCS capability');
    page.state.recipient = '+821000000001'; data.service.rcs_ready = false; await model.refresh(page.state);
    page.compose.value='한'.repeat(1001);page.compose.attrs.input();await model.measure(page.state);page.updateComposer();
    assert.equal(page.counter.textContent,'2002 / 2000 B');assert(page.send.disabled);assert(text(page.composeStatus).includes('길이를 줄여'));
    page.compose.value = ''; page.compose.attrs.input();
    assert(!page.header.children.some(node => node.tag === 'button' && text(node).includes('대화 삭제')),
        'destructive action occupies the chat header');
    const options = page.header.children.find(node => node.tag === 'details');
    assert(options && !options.open && text(options).includes('대화 삭제'), 'chat menu is missing its collapsed actions');
    options.open = true; page.root.attrs.click({target: page.bubbles});
    assert(!options.open, 'chat menu does not close when clicking elsewhere');
    assert.equal(page.root.style['--ko-height'], '688px');
    viewport.height = 420; viewportEvents.resize();
    assert.equal(page.root.style['--ko-height'], '308px', 'keyboard covers the fixed-height composer');
    page.compose.value = '입력 유지'; page.compose.attrs.input(); await polling();
    assert.equal(page.compose.value, '입력 유지'); assert.equal(page.state.draft, '입력 유지');
    const readBeforeBack = reads;
    const back = page.header.children[0]; back.attrs.click(); await polling();
    assert(!page.root.classList.contains('ko-open-chat'));
    assert.equal(reads, readBeforeBack, 'hidden mobile conversation was marked as read');
    assert.equal(page.state.draft, '입력 유지', 'returning to the list discarded the draft');
    await page.openThread(data.threads[0]);
    data.service.registered = false; await polling();
    assert(text(page.composeStatus).includes('문자 연결 대기'));
    assert(page.send.disabled, 'disconnected screen enabled sending');
    data.service.registered = true; await polling();
    page.chatInfo(); assert(text(modal.children).includes('LG U+'));
    page.editContact(data.contacts[0]); assert.equal(modal.title, '연락처 수정');
    const dialog = modal.children[0];
    const save = dialog.children[dialog.children.length - 1].children[1]; await save.attrs.click();
    page.state.messages = [{seq: 2, id: 'mms', protocol: 'mms', direction: 'incoming', body: '', timestamp: '2026-11-07T10:01:00Z',
        attachments: 1, parts: [part], subject: '[제목없음]', state: 'mms_received'}];
    page.lastMessages = ''; page.paint();
    const bubble = page.bubbles.children.find(node => node.tag === 'article');
    const imageButton = bubble.children.find(node => node.tag === 'button');
    assert(bubble.classList.contains('ko-photo-only'), 'photo-only message still has an empty title or body');
    assert(!text(bubble).includes('[제목없음]') && !text(bubble).includes('<script>.png'));
    const thumbnail = imageButton.children.find(node => node.tag === 'img');
    assert(thumbnail && thumbnail.hidden, 'inline photo element missing');
    const readsBeforePhoto = partReads;
    assert(page.photoObserver.nodes.has(imageButton), 'photo fetch was not deferred until visible');
    await imageButton.loadPhoto();
    assert(!thumbnail.hidden && thumbnail.attrs.src === 'blob:test-0', 'photo does not appear inside the message');
    assert.equal(partReads, readsBeforePhoto + 3);
    await imageButton.attrs.click();
    assert(page.viewer.open && page.viewer.attrs['aria-label'] === '사진');
    assert(page.viewer.children[1].children.some(node => node.tag === 'img' && node.attrs.src === 'blob:test-0'));
    assert(page.viewer.children[0].children.some(node => node.tag === 'a' && node.attrs.download === '<script>.png'));
    assert.equal(partReads, readsBeforePhoto + 3, 'opening a photo downloaded it again');
    page.state.messages[0].subject = '[제목 없음]'; page.paint();
    assert(!text(page.bubbles).includes('[제목 없음]'));
    await page.pendingPhotos[0].show();
    assert.equal(partReads, readsBeforePhoto + 3, 'polling downloaded the same photo again');
    page.state.messages[0].subject = '실제 제목'; page.state.messages[0].body = '사진 설명'; page.paint();
    assert(text(page.bubbles).includes('실제 제목') && text(page.bubbles).includes('사진 설명'));
    assert(!page.bubbles.children.find(node => node.tag === 'article').classList.contains('ko-photo-only'));
    page.state.showChat = false; page.paint();
    assert.equal(page.viewer, null, 'photo viewer remained open after leaving the conversation');
    assert.deepEqual(revokedURLs, ['blob:test-0'], 'photo memory was retained after leaving the conversation');
    page.state.showChat = true; page.state.messages[0].subject = '[제목없음]'; page.state.messages[0].body = '';
    page.paint(); invalidPartOffset = true;
    await page.pendingPhotos[0].show();
    const failedButton = page.bubbles.children.find(node => node.tag === 'article').children.find(node => node.tag === 'button');
    assert(text(failedButton).includes('다시 시도'), 'failed preview has no retry action');
    invalidPartOffset = false; await failedButton.attrs.click();
    assert.equal(createdURLs.length, 2, 'failed photo could not be retried');
    windowEvents.pagehide();
    assert.deepEqual(revokedURLs, ['blob:test-0', 'blob:test-1']);
    page.state.tab = 'contacts'; page.paintList(); assert(text(page.list).includes('+8221234567'));
    assert(text(page.list).includes('집'));
    console.log('Conversation display, contacts, draft preservation, SIM guard, read-only access and idempotent retry passed');
}
main().catch(error => {console.error(error); process.exitCode = 1;});
