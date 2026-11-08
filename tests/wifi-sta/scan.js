const assert = require('node:assert/strict');
const fs = require('node:fs');
const vm = require('node:vm');
const elements = {}, clicks = [], calls = [];
let response;
function E(tag, attrs, children) {
    const el = {tag, attrs, children: [], appendChild(child) { this.children.push(child); },
        removeChild() { this.children.shift(); }, get firstChild() { return this.children[0]; }};
    if (attrs && attrs.id) elements[attrs.id] = el;
    if (attrs && attrs.click) clicks.push([children, attrs.click]);
    if (Array.isArray(children)) children.forEach(c => el.appendChild(c));
    else if (children != null) el.appendChild(children);
    return el;
}
function Map() {}
Map.prototype.section = () => ({option() { return {value() {}}; }});
Map.prototype.render = () => Promise.resolve(E('div', {}, []));
const context = {view: {extend: p => p}, form: {Map}, E, _: s => s, TextEncoder, TextDecoder,
    document: {getElementById: id => elements[id]}, ui: {createHandlerFn: (self, fn) => fn},
    uci: {get() {}, sections: () => [{'.name': 'spare', ssid: ''}]},
    fs: {exec: (cmd, args) => { calls.push([cmd, ...args]); return Promise.resolve(response); }}};
vm.runInNewContext('String.prototype.format = function() { return String(this); };', context);
const source = fs.readFileSync(process.argv[2], 'utf8');
const page = vm.runInNewContext('(function() {' + source + '})()', context);
const parse = vm.runInNewContext('(function() {' + source.split('return view.extend')[0] + '; return parseScan; })()', context);
const dump = String.raw`scan_source=cache
BSS 00:11:22:33:44:55(on phy3-ap0)
    freq: 2437
    signal: -54.00 dBm
    capability: ESS Privacy
    SSID: \xed\x95\x9c\xea\xb8\x80
    RSN:
        * Authentication suites: PSK SAE
BSS 00:11:22:33:44:56(on phy3-ap0)
    freq: 5180
    signal: -70.00 dBm
    SSID: Open WiFi
BSS 00:11:22:33:44:57(on phy3-ap0)
    SSID: 
`;
const found = parse(dump);
assert.equal(found.length, 2);
assert.equal(found[0].ssid, '한글');
assert.equal(found[0].channel, '6');
assert.equal(found[0].enc, 'WPA2/WPA3');
assert.equal(found[1].channel, '36');
assert.equal(found[1].enc, 'none');
assert.equal(parse('  Cell 01\n ESSID: "old"\n Channel: 11\n Encryption: none')[0].ssid, 'old');
(async () => {
    await page.render([{}, {}, {ap_iface: 'old-ap0'}]);
    response = {code: 0, stdout: dump};
    await clicks.find(([name]) => name === 'Scan')[1]();
    assert.deepEqual(calls.at(-1), ['/usr/sbin/wifi-sta-up', 'scan']);
    assert.equal(elements['wifi-sta-scan'].children.at(-1).tag, 'table');
    response = {code: 1, stderr: 'Radio busy'};
    await clicks.find(([name]) => name === 'Scan')[1]();
    assert.equal(elements['wifi-sta-scan'].children.length, 1);
    assert.equal(elements['wifi-sta-scan'].children[0].tag, 'em');
    console.log('Wi-Fi scan parser, fresh backend lookup and error display passed');
})().catch(e => { console.error(e); process.exitCode = 1; });
