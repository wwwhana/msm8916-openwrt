'use strict';
'require view';
'require form';
'require uci';
'require rpc';

var status = rpc.declare({ object: 'a8.apn', method: 'status', expect: {} });

return view.extend({
    load: function() {
        return Promise.all([uci.load('a8_apn'), L.resolveDefault(status(), {})]);
    },
    render: function(data) {
        var state = data[1], m, s, o;
        var states = { 'no-sim': 'SIM 없음', 'waiting-sim': 'SIM 정보 대기 중',
            'sim-locked': 'SIM 잠금 해제 필요', 'no-match': '자동 검색 결과 없음 — 수동 프로파일을 선택하세요',
            'profile-not-found': '저장한 프로파일을 선택하세요', 'ready': '프로파일 준비됨',
            'pending-changes': '적용하지 않은 설정이 있습니다. 설정을 적용하거나 취소하세요',
            'disabled': '자동 설정 꺼짐' };
        m = new form.Map('a8_apn', 'APN 프로파일',
            'SIM의 본래 통신사 정보로 인터넷 APN을 찾습니다. 로밍 중에는 방문한 통신사의 APN으로 바꾸지 않습니다. ' +
            '여행 SIM·알뜰폰의 APN이 자동 후보와 다르면 프로파일을 직접 저장해 선택하세요.');
        s = m.section(form.NamedSection, 'settings', 'settings');
        o = s.option(form.DummyValue, '_status', '현재 상태');
        o.cfgvalue = function() {
            var parts = [states[state.state] || state.state || '정보 대기 중'];
            if (state.provider) parts.push(state.provider);
            if (state.home_plmn) parts.push(state.home_plmn);
            if (state.selected) parts.push(state.selected.apn);
            return parts.join(' · ');
        };
        o = s.option(form.Flag, 'enabled', 'APN 설정 관리');
        o.default = '1'; o.rmempty = false;
        o.description = '끄면 네트워크 → 인터페이스의 기존 APN 설정을 사용합니다.';
        o = s.option(form.ListValue, 'mode', '선택 방법');
        o.value('auto', 'SIM으로 자동 검색'); o.value('profile', '저장한 프로파일 선택');
        o.default = 'auto'; o.depends('enabled', '1'); o.rmempty = false;
        o = s.option(form.ListValue, 'database_profile', '자동 검색 후보');
        o.value('auto', '자동 선택');
        (Array.isArray(state.candidates) ? state.candidates : []).forEach(function(p) {
            o.value(p.id, (p.provider || p.name) + ' — ' + p.apn);
        });
        o.default = 'auto'; o.depends({ enabled: '1', mode: 'auto' });
        o.description = 'SIM을 바꾸면 새 SIM에 맞는 후보를 사용합니다. 목록은 인터넷 연결 없이도 조회됩니다.';
        o = s.option(form.ListValue, 'profile', '사용할 프로파일');
        uci.sections('a8_apn', 'profile', function(p) { o.value(p['.name'], p.name || p['.name']); });
        o.depends({ enabled: '1', mode: 'profile' }); o.rmempty = false;
        o = s.option(form.ListValue, 'iptype', 'IP 방식');
        o.value('profile', '프로파일 설정 사용'); o.value('ipv4v6', 'IPv4 + IPv6');
        o.value('ipv4', 'IPv4'); o.value('ipv6', 'IPv6'); o.default = 'profile';
        o = s.option(form.Flag, 'allow_roaming', '데이터 로밍 허용');
        o.default = '0'; o.rmempty = false;
        o.description = '여행 SIM과 요금제의 로밍 조건에 맞춰 설정하세요.';

        s = m.section(form.TypedSection, 'profile', '저장한 프로파일');
        s.addremove = true; s.anonymous = false; s.sortable = true;
        o = s.option(form.Flag, 'enabled', '사용'); o.default = '1'; o.rmempty = false;
        o = s.option(form.Value, 'name', '이름');
        o = s.option(form.Value, 'apn', 'APN'); o.placeholder = 'internet.lguplus.co.kr';
        o.validate = function(section, value) {
            return !value || /^[A-Za-z0-9][A-Za-z0-9.-]{0,99}$/.test(value) ? true : 'APN을 확인하세요.';
        };
        o = s.option(form.Value, 'home_plmn', '자동 선택할 SIM 통신사 코드');
        o.placeholder = '45006';
        o.validate = function(section, value) { return !value || /^\d{5,6}$/.test(value) ? true : 'MCC+MNC 5~6자리를 입력하세요.'; };
        o.description = '비워 두면 직접 선택할 때만 사용합니다. 입력하면 자동 모드에서 이 통신사의 SIM에 우선 사용합니다.';
        o = s.option(form.Value, 'provider_match', 'SIM 통신사 이름 조건');
        o.description = '같은 통신사 코드를 쓰는 알뜰폰을 구분할 때 입력합니다.';
        o = s.option(form.ListValue, 'auth', '인증 방식');
        [['none','없음'],['pap','PAP'],['chap','CHAP'],['both','PAP 또는 CHAP']].forEach(function(v) { o.value(v[0], v[1]); });
        o.default = 'none';
        o = s.option(form.Value, 'username', '사용자 이름');
        o.depends('auth', 'pap'); o.depends('auth', 'chap'); o.depends('auth', 'both');
        o = s.option(form.Value, 'password', '비밀번호'); o.password = true;
        o.depends('auth', 'pap'); o.depends('auth', 'chap'); o.depends('auth', 'both');
        o = s.option(form.ListValue, 'iptype', 'IP 방식');
        o.value('ipv4v6', 'IPv4 + IPv6'); o.value('ipv4', 'IPv4'); o.value('ipv6', 'IPv6'); o.default = 'ipv4v6';
        return m.render();
    }
});
