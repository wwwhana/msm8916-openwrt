const assert = require('node:assert/strict');
const fs = require('node:fs');
const vm = require('node:vm');
const source = fs.readFileSync(process.argv[2], 'utf8');
function render(modes, saved) {
    const options = {};
    function Map() {}
    Map.prototype.section = () => ({option: (type, name) => {
        const option = {values: [], value(value) { this.values.push(value); }, depends() {}};
        options[name] = option;
        return option;
    }});
    Map.prototype.render = () => options;
    const context = {view: {extend: page => page}, form: {Map},
        uci: {sections() {}, get: () => saved}, rpc: {declare: () => () => Promise.resolve({})}};
    const page = vm.runInNewContext('(function() {' + source + '})()', context);
    return page.render([{}, {radio_modes: modes, current_radio_mode: 'lte-preferred'}]);
}
const modes = ['lte-preferred', 'lte-only', '3g-preferred', '3g-only'];
let options = render(modes);
assert.deepEqual(options.radio_mode.values, ['keep', ...modes]);
assert.equal(options.radio_mode.default, 'keep');
assert.equal(options._radio_current.cfgvalue(), 'LTE 우선');
options = render([], 'lte-only');
assert.deepEqual(options.radio_mode.values, ['keep', 'lte-only']);
options = render(['unsupported-mode']);
assert.deepEqual(options.radio_mode.values, ['keep']);
console.log('LuCI modem-supported technology choices passed');
