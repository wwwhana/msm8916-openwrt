'use strict';
'require view';
'require form';
'require uci';
'require poll';
'require uplink_model';

var labels = { modem: _('LTE'), sta: _('Wi-Fi'), ethernet: _('USB 유선랜'), none: _('연결 없음') };
function statusTable(data) {
    var link = data.uplink || {}, rows = [];
    ['modem', 'sta', 'ethernet'].forEach(function(name) {
        var state = link[name] || {};
        var text = state.healthy == 1 ? _('연결됨') : state.present == 1 ? _('확인 중') : _('연결 안 됨');
        if (name === 'ethernet' && data.usb) {
            if (data.usb.role === 'off') text = _('사용 안 함');
            else if (!(data.usb.adapters || []).length) text = _('어댑터 연결 대기');
            else if (data.usb.role === 'lan') text = _('하위 장비에 공유');
        }
        rows.push(E('tr', { 'class': 'tr' }, [
            E('td', { 'class': 'td' }, labels[name]),
            E('td', { 'class': 'td' }, text),
            E('td', { 'class': 'td' }, state.device || '—')
        ]));
    });
    var nodes = [
        E('p', {}, [_('사용 중인 인터넷 회선: '), labels[link.active] || _('확인 중')]),
        E('table', { 'class': 'table' }, rows)
    ];
    if (data.usb && data.usb.error) nodes.push(E('p', { 'class': 'alert-message warning' },
        _('USB 유선랜 설정을 적용하지 못했습니다. 어댑터와 USB 연결을 확인한 뒤 다시 적용하세요.')));
    return E('div', {}, nodes);
}
return view.extend({
    load: function() {
        return uci.load('uplink').then(function() { return uplink_model.status(); });
    },
    render: function(data) {
        data = data || {};
        var m = new form.Map('uplink', _('인터넷 연결'),
            _('상위 회선은 LTE·Wi-Fi·USB 유선랜을 사용합니다. 하위 장비에는 Wi-Fi·블루투스·USB 유선랜으로 공유합니다.'));
        var s = m.section(form.NamedSection, 'general', 'uplink');
        s.addremove = false;
        var policy = s.option(form.ListValue, 'policy', _('인터넷 회선 우선순위'));
        policy.value('auto', _('LTE 우선 (유선랜·Wi-Fi 대체)'));
        policy.value('modem', _('LTE만 사용'));
        policy.value('sta', _('Wi-Fi 우선 (유선랜·LTE 대체)'));
        if (data.usb) policy.value('ethernet', _('유선랜 우선 (LTE·Wi-Fi 대체)'));
        policy.default = 'auto';
        if (data.usb) {
            var role = s.option(form.ListValue, 'usb_ethernet', _('USB 유선랜 용도'),
                _('유선랜 어댑터는 OTG 연결이 필요합니다. 이 설정은 Wi-Fi로 관리 페이지에 연결한 상태에서 적용하세요.'));
            role.value('off', _('사용 안 함 · PC 연결'));
            role.value('wan', _('상위 인터넷 회선 (WAN)'));
            role.value('lan', _('하위 장비에 공유 (LAN)'));
            role.default = 'off';
            var adapter = s.option(form.ListValue, 'usb_device', _('유선랜 어댑터'));
            adapter.value('', _('자동 선택'));
            var configured = data.usb.device || '', found = false;
            (data.usb.adapters || []).forEach(function(a) {
                adapter.value(a.device, a.device + ' · ' + a.driver);
                if (a.device === configured) found = true;
            });
            if (configured && !found) adapter.value(configured, configured + ' · ' + _('연결 대기'));
            adapter.depends('usb_ethernet', 'wan');
            adapter.depends('usb_ethernet', 'lan');
            adapter.rmempty = false;
        }
        var dns = s.option(form.Value, 'sta_dns', _('Wi-Fi 회선 DNS'), _('비우면 상위 공유기가 제공한 DNS를 사용합니다.'));
        dns.datatype = 'list(ipaddr)';
        var target = s.option(form.Value, 'probe_target', _('연결 확인 주소'));
        target.datatype = 'host'; target.default = '1.1.1.1';
        [['probe_interval', _('확인 주기 (초)'), '30'],
            ['probe_timeout', _('응답 대기 (초)'), '3'],
            ['fail_threshold', _('끊김으로 판단할 연속 실패 횟수'), '3']].forEach(function(spec) {
            var o = s.option(form.Value, spec[0], spec[1]);
            o.datatype = 'min(1)'; o.default = spec[2];
        });
        var status = E('div', {});
        function paint(next) {
            while (status.firstChild) status.removeChild(status.firstChild);
            status.appendChild(statusTable(next));
        }
        paint(data);
        poll.add(function() { return uplink_model.status().then(paint); }, 5);
        return m.render().then(function(node) { return E('div', {}, [status, node]); });
    }
});
