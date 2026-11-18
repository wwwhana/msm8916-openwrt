'use strict';
const assert = require('node:assert/strict');
const fs = require('node:fs');
const vm = require('node:vm');
const source = fs.readFileSync(process.argv[2], 'utf8');

function render(side, saved, single_button = false) {
    let options = {};
    function Map() {}
    Map.prototype.section = () => ({option: (type, name) => {
        const o = {values: [], labels: {}, dependencies: [], value: function(value, label) { this.values.push(value); this.labels[value] = label; },
                   depends: function(key, value) { this.dependencies.push([key, value]); }};
        options[name] = o;
        return o;
    }});
    Map.prototype.render = () => options;
    const context = {
        view: {extend: o => o}, form: {Map},
        uci: {get: () => saved}, rpc: {declare: () => () => Promise.resolve({})}
    };
    const page = vm.runInNewContext('(function() {' + source + '})()', context);
    page.render([{}, {supported: true, side, single_button, display: 'lcd', active: {display: 'lcd', buttons: 'power'}}]);
    return options;
}
let options = render(false, 'power');
assert.deepEqual(options.display.values, ['lcd', 'led']);
assert.deepEqual(options.buttons.values, ['power']);
for (const [name, defaultValue] of [['screensaver_timeout', '30'], ['screen_off_timeout', '300']]) {
    assert.deepEqual(options[name].values, ['0', '15', '30', '60', '120', '300', '600', '1800']);
    assert.equal(options[name].default, defaultValue);
    assert.equal(options[name].rmempty, false);
    assert.equal(options[name].retain, true);
    assert.deepEqual(options[name].dependencies, [['display', 'lcd']]);
    assert.equal(options[name].validate('settings', '0'), true);
    assert.equal(options[name].validate('settings', '300'), true);
    assert.equal(typeof options[name].validate('settings', '17'), 'string');
}
options = render(true, 'power');
assert.deepEqual(options.buttons.values, ['power', 'side']);
options = render(false, 'side');
assert.deepEqual(options.buttons.values, ['power', 'side']);
options = render(true, 'side', true);
assert.deepEqual(options.buttons.values, ['power']);
assert.equal(options.buttons.labels.power, '사이드 버튼: 짧게 이동 / 길게 선택');
assert.equal(options._status.cfgvalue(), 'LCD · 사이드 짧게 이동 / 길게 선택');
console.log('LuCI LCD/LED, screen timeout choices and side-key availability checks passed');
