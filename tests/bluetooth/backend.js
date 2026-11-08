const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');
const source = fs.readFileSync(path.join(__dirname, '../../packages/luci-app-bluetooth/root/usr/share/rpcd/ucode/bluetooth.uc'), 'utf8')
    .replace(/^#![^\n]*\n/, '')
    .replace("import { popen } from 'fs';", '');
const commands = [];
let failed = false;
let powered = true, available = true;
let compatibility = '0\n';
const context = {
    type: value => typeof value === 'number' && Number.isInteger(value) ? 'int' : typeof value,
    length: value => value.length, match: (value, regex) => value.match(regex), trim: value => value.trim(),
    replace: (value, pattern, replacement) => typeof pattern === 'string' ? value.split(pattern).join(replacement) : value.replace(pattern, replacement),
    json: JSON.parse,
    popen(command) {
        commands.push(command);
        const text = command.includes('bt_device.sh') ? '{"devices":[]}' :
            command.includes('show') ? `Controller 00:11:22:33:44:55\nPowered: ${powered ? 'yes' : 'no'}\nAlias: A8\nUUID: 00001116-0000-1000-8000-00805f9b34fb\n` :
            command.includes('get bt-nap.main.enabled') ? '1\n' : command.includes('bt-compat status') ? compatibility : '';
        return {read: () => text, close: () => failed || command.includes('show') && !available ? 1 : 0};
    }
};
const methods = vm.runInNewContext('(function() {' + source + '})()', context)['luci.bluetooth'];
assert.equal(methods.get_status.call().powered, true);
assert.equal(methods.get_status.call().nap_active, true);
assert.equal(methods.get_status.call().pairing_compat, false);
assert.equal(methods.get_status.call().pairing_compat_available, true);
compatibility = '1\n'; assert.equal(methods.get_status.call().pairing_compat, true);
compatibility = ''; assert.equal(methods.get_status.call().pairing_compat_available, false);
compatibility = '0\n';
assert.equal(methods.set_pairing_compat.call({args: {on: 1}}).success, true);
assert.match(commands.at(-1), /bt-compat on/);
assert.equal(methods.set_pairing_compat.call({args: {on: '1; reboot'}}).success, false);
powered = false;
assert.equal(methods.get_status.call().nap_active, false);
available = false;
assert.equal(methods.get_status.call().nap_active, false);
assert.equal(methods.get_status.call().nap_enabled, true);
powered = available = true;
assert.equal(methods.get_devices.call().devices.length, 0);
assert.equal(methods.set_nap.call({args: {on: 1}}).success, true);
assert.match(commands.at(-1), /bt-nap on/);
assert.equal(methods.set_nap.call({args: {on: 'reboot'}}).success, false);
assert.equal(methods.set_power.call({args: {on: 0}}).success, true);
assert.match(commands.at(-1), /timeout 8 bluetoothctl --timeout 6 power off/);
failed = true;
assert.equal(methods.set_pairing_compat.call({args: {on: 0}}).success, false);
assert.equal(methods.set_power.call({args: {on: 1}}).success, false);
failed = false;
assert.equal(methods.set_discoverable.call({args: {on: 1}}).success, true);
assert.match(commands.at(-3), /discoverable-timeout 180/);
assert.match(commands.at(-2), /pairable on/);
assert.match(commands.at(-1), /discoverable on/);
assert.equal(methods.set_discoverable.call({args: {on: '1; reboot'}}).success, false);
const mac = '00:11:22:33:44:55';
assert.equal(methods.pair.call({args: {mac}}).success, true);
assert.match(commands.at(-1), /--agent NoInputNoOutput --timeout 25 pair/);
assert.doesNotMatch(commands.at(-1), /expect|unbuffer/);
const before = commands.length;
for (const method of ['pair', 'connect', 'disconnect', 'remove_device'])
    assert.equal(methods[method].call({args: {mac: '00:11:22:33:44:55; reboot'}}).success, false);
assert.equal(methods.set_volume.call({args: {data: {mac, volume: '0; reboot'}}}).success, false);
assert.equal(methods.set_volume.call({args: {data: {mac, volume: 128}}}).success, false);
assert.equal(methods.set_alias.call({args: {alias: 'A8\nreboot'}}).success, false);
assert.equal(commands.length, before);
assert.equal(methods.set_volume.call({args: {data: {mac, volume: 50}}}).success, true);
console.log('Bluetooth command results, pairing and input checks passed');
