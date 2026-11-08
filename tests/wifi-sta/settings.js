const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');

async function options(file, data) {
    const found = {};
    function Map() {}
    Map.prototype.section = () => ({option(type, name) {
        return found[name] = {values: [], value(value, label) { this.values.push([value, label]); }, depends() {}};
    }});
    Map.prototype.render = () => Promise.resolve({});
    const element = (tag, attrs, children) => ({tag, attrs, children,
        appendChild() {}, removeChild() {}});
    const context = {view: {extend: p => p}, form: {Map}, E: element,
        _: s => s, fs: {}, poll: {add() {}}, ui: {createHandlerFn: (self, fn) => fn},
        uci: {get() {}, sections: () => [{'.name': 'spare', ssid: ''}]} };
    vm.runInNewContext('String.prototype.format = function() { return String(this); };', context);
    const page = vm.runInNewContext('(function() {' + fs.readFileSync(path.join(process.argv[2], file), 'utf8') + '})()', context);
    await page.render(data);
    return found;
}

(async () => {
    const config = await options('config.js', [{}, {}, {enabled: '1'}]);
    assert.equal(config.enabled.rmempty, false); // A saved off flag must stay off after reboot.
    const uplink = await options('uplink.js', null);
    assert.deepEqual(uplink.policy.values.map(v => v[0]), ['auto', 'modem', 'sta']);
    assert.match(uplink.policy.values[2][1], /Wi-Fi.*LTE/);
    assert.equal(uplink.policy.default, 'auto'); // Existing LTE-first installations retain their policy.
    const usb = await options('uplink.js', {usb:{device:'eth2',adapters:[{device:'eth0',driver:'r8152'}]}});
    assert.deepEqual(usb.policy.values.map(v=>v[0]), ['auto','modem','sta','ethernet']);
    assert.deepEqual(usb.usb_ethernet.values.map(v=>v[0]), ['off','wan','lan']);
    assert.deepEqual(usb.usb_device.values.map(v=>v[0]), ['', 'eth0', 'eth2']);
    console.log('Station toggle and LTE/Wi-Fi/Ethernet priority and LAN/WAN choices passed');
})().catch(error => { console.error(error); process.exitCode = 1; });
