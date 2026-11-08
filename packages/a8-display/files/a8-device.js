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
        var durations = [['0', '사용 안 함'], ['15', '15초'], ['30', '30초'], ['60', '1분'],
                         ['120', '2분'], ['300', '5분'], ['600', '10분'], ['1800', '30분']];
        [['screensaver_timeout', '화면보호기 전환 시간', '30'],
         ['screen_off_timeout', '화면 끄기 시간', '300']].forEach(function(setting) {
            var option = s.option(form.ListValue, setting[0], setting[1], '마지막 버튼 조작부터 계산합니다.');
            durations.forEach(function(choice) { option.value(choice[0], choice[1]); });
            option.default = setting[2]; option.rmempty = false; option.retain = true;
            option.depends('display', 'lcd');
            option.validate = function(section, value) {
                return durations.some(function(choice) { return choice[0] === value; }) || '목록에서 시간을 선택해 주세요.';
            };
        });
        return m.render();
    }
});
