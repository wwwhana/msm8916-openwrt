'use strict';
'require view';
'require form';
'require uci';
'require rpc';

var status = rpc.declare({ object: 'a8.device', method: 'status', expect: {} });

return view.extend({
    load: function() {
        return Promise.all([uci.load('a8_device'), L.resolveDefault(status(), {})]);
    },
    render: function(data) {
        var hardware = data[1], active = hardware.active || {}, m, s, o;
        m = new form.Map('a8_device', '화면·버튼');
        s = m.section(form.NamedSection, 'settings', 'settings');
        o = s.option(form.DummyValue, '_status', '현재 조작');
        o.cfgvalue = function() {
            if (!hardware.supported) return '정보 없음';
            if (active.display === 'lcd')
                return active.buttons === 'side' ? 'LCD · 사이드 이동 / 전원 선택' : 'LCD · 전원 짧게 이동 / 길게 선택';
            return hardware.display === 'led' ? 'LED 전용' : 'LCD 대기 중';
        };
        o = s.option(form.ListValue, 'display', '표시 방식');
        o.value('lcd', 'LCD 메뉴');
        o.value('led', 'LED 전용');
        o.default = 'lcd'; o.rmempty = false;
        o = s.option(form.ListValue, 'buttons', '메뉴 조작');
        o.value('power', '전원 버튼만: 짧게 이동 / 길게 선택');
        if (hardware.side || uci.get('a8_device', 'settings', 'buttons') === 'side')
            o.value('side', '사이드로 위·아래 / 전원으로 선택');
        o.default = 'power'; o.rmempty = false; o.retain = true; o.depends('display', 'lcd');
        return m.render();
    }
});
