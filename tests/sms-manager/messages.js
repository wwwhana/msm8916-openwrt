'use strict';
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');

const resources = path.join(__dirname, '../../packages/luci-app-sms-manager/htdocs/luci-static/resources');
function load(file, context) {
    const format = 'String.prototype.format=function(...a){let i=0;return this.replace(/%[sd]/g,()=>a[i++]);};String.format=(s,...a)=>s.format(...a);';
    return vm.runInNewContext(format + '(function() {' + fs.readFileSync(path.join(resources, file), 'utf8') + '})()', context);
}

async function main() {
    const body = '한국어 문자, "큰따옴표"와 \'작은따옴표\'\n둘째 줄  공백 유지. ' + '가'.repeat(75);
    const files = new Map(), calls = [];
    let failure = false, serial = 0, centre = '';
    const remote = {
        exec: async (cmd, args) => {
            calls.push({cmd, args});
            if (cmd === '/bin/mktemp')
                return {code: 0, stdout: '/tmp/smsmgr_text.' + String(++serial).padStart(6, '0') + '\n'};
            assert.equal(cmd, '/usr/bin/mmcli');
            if (args.includes('--send'))
                return {code: failure ? 1 : 0, stdout: '', stderr: failure ? 'registration failed' : ''};
            const file = args.find(x => x.startsWith('--messaging-create-sms-with-text=')).split('=')[1];
            assert.equal(files.get(file), body);
            assert.equal(args.find(x => x.startsWith('--messaging-create-sms=')), '--messaging-create-sms=number=+821012345678' + (centre ? ',smsc=' + centre : ''));
            return failure ? {code: 1, stderr: 'modem busy'} : {code: 0, stdout: '/org/freedesktop/ModemManager1/SMS/91'};
        },
        write: async (file, value, mode) => { assert.equal(mode, 0o600); files.set(file, value); },
        remove: async file => { assert(files.delete(file)); }
    };
    const sms = load('sms_manager.js', {baseclass: {extend: o => o}, fs: remote, _: s => s});
    for (const number of ['010-1234-5678', '+82 10 1234 5678', '+8201012345678'])
        assert.equal(sms.normalizeNumber(number, '+82'), '+821012345678');
    assert.equal(sms.normalizeNumber('02-123-4567', '+82'), '+8221234567');
    assert.equal(sms.normalizeNumber('114', '+82'), '114');
    assert.equal(sms.normalizeNumber('+44 1234567890', '+82'), '+441234567890');
    assert.equal(sms.normalizeNumber('01012345678', '+81'), '01012345678');
    assert.throws(() => sms.normalizeNumber('+82,number=123', '+82'), /valid phone/);

    assert.equal(sms.measure('한'.repeat(70)).parts, 1);
    assert.equal(sms.measure('한'.repeat(71)).parts, 2);
    assert.equal(sms.measure('a'.repeat(160)).parts, 1);
    assert.equal(sms.measure('a'.repeat(161)).parts, 2);
    assert.equal(sms.measure('^'.repeat(153)).parts, 3);
    assert.equal(sms.measure('😀'.repeat(34)).units, 68);
    assert.equal(sms.measure('').parts, 0);

    const item = sms.parse({sms: {content: {number: '15881234', text: body},
        properties: {storage: 'sm', timestamp: '2026-11-07T12:34:56+09'}}}, '91');
    assert.equal(item.content, body);
    assert.equal(item.sender, '15881234');
    assert.equal(item.date.toISOString(), '2026-11-07T03:34:56.000Z');
    assert.throws(() => sms.parse('{}', '91'), /Invalid SMS/);

    await sms.create('0', '010-1234-5678', body, '+82');
    assert.equal(files.size, 0);
    assert(!calls.some(c => c.args.includes('--send')));
    centre = '+821099999999';
    await sms.create('0', '01012345678', body, '+82', centre);
    assert.equal(files.size, 0);
    centre = '';
    await assert.rejects(sms.create('0', '01012345678', body, '+82', '123,text=bad'), /valid phone/);
    failure = true;
    await assert.rejects(sms.create('0', '01012345678', body, '+82'), /modem busy/);
    assert.equal(files.size, 0);
    const before = calls.length;
    await assert.rejects(sms.create('0', '123,text=bad', body, '+82'), /valid phone/);
    assert.equal(calls.length, before);
    failure = false;

    const nodes = {}, notifications = [];
    function E(tag, attrs, children) {
        if (typeof attrs !== 'object' || Array.isArray(attrs)) { children = attrs; attrs = {}; }
        attrs = attrs || {};
        const node = {tag, attrs, children, style: {}, value: attrs.value || '', focus: () => {},
            appendChild: child => { node.children.push(child); }};
        if (attrs.id) nodes[attrs.id] = node;
        return node;
    }
    const page = load('view/modem/sms_manager_sendsms.js', {
        view: {extend: o => o}, sms, fs: remote, E, _: s => s,
        uci: {sections: () => [{sendport: '/org/freedesktop/ModemManager1/Modem/0', pnumber: '+82'}]},
        ui: {createHandlerFn: () => () => {}, addNotification: (...args) => notifications.push(args)},
        L: {bind: (fn, self) => fn.bind(self)},
        dom: {content: (node, children) => { node.children = children; }},
        document: {getElementById: id => nodes[id], querySelectorAll: () => [], querySelector: () => nodes.ans}
    });
    page.render(['']);
    assert.equal(nodes.phonenumber.attrs.type, 'tel');
    assert.equal(nodes.phonenumber.attrs.oninput, undefined);
    assert.equal(nodes.smstext.attrs.keydown, undefined);
    nodes.smstext.value = body;
    page.updateMessageCounter();
    assert.equal(nodes.smstext.value, body);
    assert.equal(nodes.encoding.textContent, 'Unicode');
    assert.match(nodes.counter.textContent, /[2-9] SMS$/);
    nodes.phonenumber.value = '010-1234-5678';
    await page.handleGo({target: {getAttribute: () => 'send'}});
    assert(calls.some(c => c.args.includes('--send')));
    assert.equal(nodes.ans.children[0], 'SMS sent successfully!');
    failure = true;
    await page.handleCommand('/usr/bin/mmcli', ['-s', '91', '--send']);
    assert.match(nodes.ans.children[0], /^Error sending SMS: registration failed/);
    assert.equal(notifications.length, 0);
    assert.equal(files.size, 0);

    const readPage = load('view/modem/sms_manager_readsms.js', {
        view: {extend: o => o}, sms, E, _: s => s,
        rpc: {declare: () => () => Promise.resolve({})},
        fs: {exec: () => { throw new Error('Unexpected read-page command'); }, exec_direct: async (cmd, args, type) => {
            assert.equal(cmd, '/usr/bin/mmcli');
            if (args.includes('--messaging-list-sms')) return '/org/freedesktop/ModemManager1/SMS/91 (received)';
            assert.equal(type, 'json'); assert(args.includes('-J'));
            return {sms: {content: {number: '15881234', text: body}, properties: {storage: 'sm', timestamp: '2026-11-07T12:34:56+09'}}};
        }},
        uci: {load: () => Promise.resolve(), get: (config, section, key) => key === 'readport' ? '/org/freedesktop/ModemManager1/Modem/0' : '',
            set: () => {}, save: () => Promise.resolve(), apply: () => Promise.resolve()},
        ui: {createHandlerFn: () => () => {}, addNotification: (...args) => notifications.push(args)},
        L: {resolveDefault: (promise, value) => promise.catch(() => value), resource: p => p},
        document: {getElementById: id => nodes[id]}
    });
    await readPage.render();
    await new Promise(resolve => setImmediate(resolve));
    const row = nodes.smsTable.children[1];
    assert.equal(row.children[3].children, body);
    assert.equal(notifications.length, 0);
    console.log('Korean numbers, Unicode/multipart preservation, SMS JSON, temporary-file cleanup and send UI checks passed');
}
main().catch(err => { console.error(err); process.exitCode = 1; });
