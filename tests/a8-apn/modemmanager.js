const assert = require('node:assert/strict');
const fs = require('node:fs');
const vm = require('node:vm');
const source = fs.readFileSync(process.argv[2], 'utf8');
const menu = JSON.parse(fs.readFileSync(process.argv[3], 'utf8'));
let noModem = false, missingIMS = false, failModem = false, poller, tabs;
function E(tag, attrs, children) {
    if (Array.isArray(tag)) { children = attrs; attrs = {}; }
    const node = {tag, attrs: attrs || {}, childNodes: [], appendChild(child) { this.childNodes.push(child); }};
    for (const child of Array.isArray(children) ? children : children == null ? [] : [children]) node.appendChild(child);
    Object.defineProperties(node, {
        firstElementChild: {get() { return this.childNodes[0]; }},
        lastElementChild: {get() { return this.childNodes[this.childNodes.length - 1]; }}
    });
    return node;
}
function strings(node) {
    if (!node) return '';
    return typeof node === 'object' ? node.childNodes.map(strings).join('\n') : String(node);
}
const ims = {load: () => Promise.resolve({ims: {state: 'available'}, apn: 'internet.example'}),
    render: data => { assert.equal(data.ims.state, 'available'); return E('div', {}, 'IMS registration'); }};
const context = {E, _: text => text, window: {location: {hash: '#ims'}}, view: {extend: value => value},
    dom: {content: (node, child) => { node.childNodes = [child]; }},
    ui: {tabs: {initTabGroup: panes => { tabs = panes; }}}, poll: {add: fn => { poller = fn; }},
    helper: {
        getModems: () => failModem ? Promise.reject(new Error('unavailable')) : Promise.resolve(noModem ? [] : [{
            modem: {generic: {device: 'qcom-soc', 'own-numbers': [], 'access-technologies': ['lte'],
                'signal-quality': {value: '75'}}, '3gpp': {'operator-name': 'Test operator'}}}]),
        getModemSims: () => Promise.resolve([]), getModemLocation: () => Promise.resolve(null)
    },
    L: {bind: (fn, self) => fn.bind(self), resolveDefault: (promise, fallback) => promise.catch(() => fallback),
        require: name => { assert.equal(name, 'a8-ims'); return missingIMS ? Promise.reject(new Error('not installed')) : Promise.resolve(ims); }}
};
const page = vm.runInNewContext(`String.prototype.format = function(...args) {
    let i = 0; return this.replace(/%[sd]/g, () => args[i++]);
}; (function() {${source}})()`, context);
(async () => {
    const data = await page.load();
    const tree = page.render(data);
    assert.equal(tabs.length, 2);
    assert.equal(tabs[1].attrs['data-tab'], 'ims');
    assert.equal(tabs[1].attrs['data-tab-title'], 'IMS');
    assert.equal(tabs[1].attrs['data-tab-active'], 'true');
    assert.match(strings(tree), /Test operator/);
    assert.equal((strings(tree).match(/internet.example/g) || []).length, 1);
    assert.doesNotMatch(strings(tabs[1]), /Test operator|internet.example/);
    await poller();
    assert.equal(tabs[1].attrs['data-tab-active'], null); // Polling lets LuCI retain the user's current tab.
    noModem = true; page.render(await page.load()); assert.equal(tabs.length, 1);
    assert.equal(tabs[0].attrs['data-tab'], 'ims');
    failModem = true; page.render(await page.load()); assert.equal(tabs[0].attrs['data-tab'], 'ims');
    failModem = false; noModem = false; missingIMS = true;
    page.render(await page.load()); assert.equal(tabs.length, 2);
    assert.equal(tabs[0].attrs['data-tab'], 'qcom-soc');
    assert.equal(tabs[1].attrs['data-tab'], 'ims');
    assert.match(strings(tabs[1]), /IMS 상태를 불러오지 못했습니다/);
    for (const route of ['admin/status/mobile-network', 'admin/network/a8-ims', 'admin/modem/modemconfig-ko']) {
        assert.equal(menu[route].title, undefined);
        assert.equal(menu[route].action.type, 'alias');
        assert.equal(menu[route].action.path, 'admin/status/modemmanager');
    }
    console.log('ModemManager IMS tab, deep link, polling, missing modem/component and hidden aliases passed');
})().catch(error => { console.error(error); process.exitCode = 1; });
