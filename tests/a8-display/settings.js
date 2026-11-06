'use strict';
const assert = require('node:assert/strict');
const fs = require('node:fs');
const vm = require('node:vm');
const source = fs.readFileSync(process.argv[2], 'utf8');

function render(side, saved) {
    let options = {};
    function Map() {}
    Map.prototype.section = () => ({option: (type, name) => {
        const o = {values: [], value: function(value) { this.values.push(value); }, depends: () => {}};
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
options = render(true, 'power');
assert.deepEqual(options.buttons.values, ['power', 'side']);
options = render(false, 'side');
assert.deepEqual(options.buttons.values, ['power', 'side']);
console.log('LuCI LCD/LED options and side-key availability checks passed');
