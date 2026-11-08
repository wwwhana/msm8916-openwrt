const assert = require('node:assert/strict');
const fs = require('node:fs');
const vm = require('node:vm');
const header = fs.readFileSync(process.argv[2], 'utf8');
const invalidate = header.match(/L\.require\('session'\)[\s\S]*?(?=<\/script>)/)[0];
const data = {menu: {old: true}, tab: {selected: 'qcom-soc'}, 'ui-resource-version': 'old'};
const session = {
    getLocalData: key => data[key],
    setLocalData: (key, value) => { if (value == null) delete data[key]; else data[key] = value; }
};
const L = {env: {resource_version: 'new'}, require: name => {
    assert.equal(name, 'session'); return Promise.resolve(session);
}};
(async () => {
    await vm.runInNewContext(invalidate, {L});
    assert.equal(data.menu, undefined); // Old menu must go before the theme reads it.
    assert.equal(data['ui-resource-version'], 'new');
    assert.deepEqual(data.tab, {selected: 'qcom-soc'});
    data.menu = {fresh: true};
    await vm.runInNewContext(invalidate, {L});
    assert.deepEqual(data.menu, {fresh: true});
    L.env.resource_version = 'newer';
    await vm.runInNewContext(invalidate, {L});
    assert.equal(data.menu, undefined);
    for (const path of ['/usr/share/luci/menu.d/a8-apn.json',
        '/www/luci-static/resources/a8-ims.js',
        '/www/luci-static/resources/view/modemmanager/status.js']) assert.ok(header.includes(`stat('${path}')`));
    assert.match(header, /luci\.js\?v=[^"\n]+\{\{ a8_ui_version \}\}/);
    console.log('A8 live asset version and session menu invalidation passed');
})().catch(error => { console.error(error); process.exitCode = 1; });
