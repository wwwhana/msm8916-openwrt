const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');
const elements = {}, polls = [], calls = [];
let nap_enabled = false, nap_active = false;
let powered = true, discoverable = false, alias = 'A8', failed = false;
let pairing_compat = false;
const E = (tag, attrs, children) => {
    if (Array.isArray(tag)) { children = attrs; attrs = {}; }
    if (typeof attrs === 'string') { children = attrs; attrs = {}; }
    const element = {tag, attrs: attrs || {}, children, appendChild() {}};
    if (element.attrs.id) elements[element.attrs.id] = element;
    return element;
};
const context = {view: {extend: page => page}, E, _: s => s,
    poll: {add: fn => polls.push(fn)}, document: {getElementById: id => elements[id]},
    dom: {content: (element, children) => { element.children = children; }},
    ui: {createHandlerFn: (self, fn) => fn.bind(self), showModal() {}, hideModal() {}, addNotification() {}},
    rpc: {declare: spec => (...args) => {
        calls.push([spec.method, ...args]);
        if (spec.method === 'get_devices') return Promise.resolve({devices: []});
        if (spec.method === 'get_status') return Promise.resolve({powered, alias, discoverable, nap_enabled, nap_active, pairing_compat, pairing_compat_available: true});
        if (!failed && spec.method === 'set_power') powered = args[0] === 1;
        if (!failed && spec.method === 'set_discoverable') discoverable = args[0] === 1;
        if (!failed && spec.method === 'set_nap') nap_active = nap_enabled = args[0] === 1;
        if (!failed && spec.method === 'set_pairing_compat') pairing_compat = args[0] === 1;
        if (!failed && spec.method === 'set_alias') alias = args[0];
        return Promise.resolve({success: !failed});
    }}
};
vm.runInNewContext('String.prototype.format = function() { return String(this); };', context);
const source = fs.readFileSync(path.join(__dirname, '../../packages/luci-app-bluetooth/htdocs/luci-static/resources/view/bluetooth/devices.js'), 'utf8');
const page = vm.runInNewContext('(function() {' + source + '})()', context);
(async () => {
    const tree = page.render(await page.load());
    assert.equal(elements.power_button.children, 'Turn Off');
    assert.equal(polls.length, 1);
    await elements.power_button.attrs.click();
    assert.equal(powered, false);
    await polls[0]();
    assert.equal(elements.power_button.innerText, 'Turn On');
    // Alias and scan refresh in place rather than installing another polling loop.
    const buttons = [];
    function visit(node) {
        if (Array.isArray(node)) return node.forEach(visit);
        if (!node || typeof node !== 'object') return;
        if (node.tag === 'button') buttons.push(node);
        visit(node.children);
    }
    visit(tree);
    elements.bt_alias.value = 'Travel A8';
    await buttons.find(b => b.children === 'Rename Adapter').attrs.click();
    await buttons.find(b => b.children === 'Scan for Devices').attrs.click();
    await elements.pairing_button.attrs.click();
    assert.equal(discoverable, true);
    await elements.nap_button.attrs.click();
    assert.equal(nap_active, true);
    assert.equal(elements.nap_button.innerText, 'Stop Internet Sharing');
    assert.equal(elements.pairing_button.innerText, 'Stop Pairing');
    await elements.pairing_compat_button.attrs.click();
    assert.equal(pairing_compat, true);
    assert.equal(elements.pairing_compat_button.innerText, 'Disable Pairing Compatibility');
    await elements.pairing_compat_button.attrs.click();
    assert.equal(pairing_compat, false);
    assert.equal(polls.length, 1);
    assert.equal(alias, 'Travel A8');
    assert(calls.some(call => call[0] === 'start_scan' && call[1] === 1));
    failed = true;
    await assert.rejects(elements.pairing_compat_button.attrs.click(), /Bluetooth command failed/);
    assert.equal(pairing_compat, false);
    await assert.rejects(elements.power_button.attrs.click(), /Bluetooth command failed/);
    console.log('Bluetooth page state, power and in-place refresh checks passed');
})().catch(error => { console.error(error); process.exitCode = 1; });
