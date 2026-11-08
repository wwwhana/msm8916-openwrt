const assert = require('node:assert/strict');
const fs = require('node:fs');
const vm = require('node:vm');
const source = fs.readFileSync(process.argv[2], 'utf8');
let calls = 0, next = {state: 'unsupported'};
const E = (tag, attrs, children) => {
    if (Array.isArray(children)) assert(!children.includes(null), 'null becomes visible text in LuCI');
    return {tag, attrs, children, replaceChildren(...children) { this.children = children; }};
};
const context = {baseclass: {extend: p => p}, rpc: {declare: spec => {
    if (spec.object === 'modemconfig-ko') { assert(['snapshot','rcs_retry'].includes(spec.method)); return () => Promise.resolve({}); }
    if (spec.object === 'modemconfig-ko') return () => Promise.resolve(spec.method === 'snapshot' ? {service:{subscription:'fake',session:'test'}} : {});
    assert.equal(spec.object, 'a8.apn'); assert(['ims_status', 'status'].includes(spec.method));
    return () => { calls++; return Promise.resolve(spec.method === 'status' ? {provider: 'LG U+', modem_state: 'connected', selected: {apn: 'internet.example'}} : next); };
}}, ui: {createHandlerFn: (self, fn) => fn.bind(self)}, E,
L: {hasViewPermission: () => true, resolveDefault: (promise, fallback) => promise.catch(() => fallback),
    url: (...args) => '/' + args.join('/'), hasViewPermission: () => true}};
const page = vm.runInNewContext('(function() {' + source + '})()', context);
function strings(tree) {
    if (!tree) return '';
    if (Array.isArray(tree)) return tree.map(strings).join('\n');
    return typeof tree === 'object' ? strings(tree.children) : String(tree);
}
(async () => {
    const data = await page.load();
    const tree = page.render(data);
    assert.match(strings(tree), /IMS 등록 여부는 미확인/);
    assert.doesNotMatch(strings(tree), /미등록/); // Unavailable does not mean deregistered.
    assert.equal(calls, 2);
    next = {state: 'available', registration: {state: 'available', status: 'registered'},
        settings: {state: 'available', ims: 'yes', sms: 'no'},
        services: {state: 'available', sms: {status: 'full-service'}, voice: {status: 'no-service'}}};
    await tree.children[1].attrs.click();
    assert.equal(calls, 4);
    assert.doesNotMatch(strings(tree), /모바일 네트워크|통신사|모뎀 연결|데이터 APN|LG U\+/);
    assert.equal(data.apn, 'internet.example');
    assert.match(strings(tree), /등록됨/);
    assert.doesNotMatch(strings(tree), /IMS 등록 설정|IMS 문자 설정|IMS 음성 설정/);
    assert.match(strings(tree), /사용 가능/);
    assert.match(strings(tree), /사용 불가/);
    const native = page.renderStatus({state: 'available', services: {state: 'available',
        sms: {status: 'available'}, voice: {status: 'unavailable'}}});
    assert.match(strings(native), /사용 가능/);
    assert.match(strings(native), /사용 불가/);
    assert.doesNotMatch(strings(native), /unavailable|\bnull\b/);
    const client = page.renderStatus({state: 'available', backend: 'modemconfig-ko', sim_slot: 2,
        registration: {state: 'available', status: 'registered'},
        services: {state: 'available', sms: {status: 'ready'}, voice: {status: 'not-implemented'}, rcs: {status: 'not-implemented'}}});
    assert.match(strings(client), /ModemConfig-KO/);
    assert.match(strings(client), /SIM 2/);
    assert.match(strings(client), /준비됨/);
    assert.match(strings(client), /준비 중/);
    const nas = page.renderStatus({state: 'available', backend: 'modemconfig-ko', sms_transport: 'nas', sim_slot: 2,
        registration: {state: 'available', status: 'not-registered'},
        services: {state: 'available', sms: {status: 'ready'}}});
    assert.match(strings(nas), /LTE NAS/);
    assert.match(strings(nas), /미등록/);
    assert.match(strings(nas), /준비됨/);
    assert.doesNotMatch(strings(nas), /IMS 문자 서비스/);
    next = null;
    assert.doesNotThrow(() => page.renderStatus({state:'error'}));
    assert.match(strings(page.render(await page.load())), /조회 실패/);

    console.log('IMS tab states, duplicate removal and manual refresh checks passed');
})().catch(error => { console.error(error); process.exitCode = 1; });
