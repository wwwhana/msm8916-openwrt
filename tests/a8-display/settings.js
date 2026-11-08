'use strict';
const assert = require('node:assert/strict');
const fs = require('node:fs');
const vm = require('node:vm');
const source = fs.readFileSync(process.argv[2], 'utf8');

function render(side, saved) {
    let options = {};
    function Map() {}
    Map.prototype.section = () => ({option: (type, name) => {
        const o = {values: [], dependencies: [], value: function(value) { this.values.push(value); },
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
    page.render([{}, {supported: true, side, display: 'lcd', active: {display: 'lcd', buttons: 'power'}}]);
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
console.log('LuCI LCD/LED, screen timeout choices and side-key availability checks passed');
