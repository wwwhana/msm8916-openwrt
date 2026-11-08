'use strict';
'require baseclass';
'require rpc';
'require ui';

var inspect = rpc.declare({ object: 'a8.apn', method: 'ims_status', expect: {} });
var inspectNetwork = rpc.declare({ object: 'a8.apn', method: 'status', expect: {} });
var inspectMessages = rpc.declare({object:'modemconfig-ko',method:'snapshot',expect:{}});
var retryRCS = rpc.declare({object:'modemconfig-ko',method:'rcs_retry',params:['subscription','session'],expect:{}});
var labels = { available: '조회 가능', unsupported: '모뎀이 제공하지 않음',
    'tool-unsupported': '설치된 도구에서 제공하지 않음', error: '조회 실패',
    'no-device': '모뎀을 찾지 못함', registered: '등록됨', 'not-registered': '미등록',
    registering: '등록 중', 'limited-registered': '제한적으로 등록됨',
    unavailable: '사용 불가', 'limited-available': '제한적으로 사용 가능',
    'no-service': '사용 불가', 'limited-service': '제한적으로 사용 가능',
    'full-service': '사용 가능', ready: '준비됨', 'not-implemented': '준비 중',
    yes: '켜짐', no: '꺼짐', wwan: '이동통신망', wlan: 'Wi-Fi' };
Object.assign(labels, {configured:'설정 수신됨',configuring:'설정 받는 중','verifying-sms':'문자 인증 중',
    'disabled-by-carrier':'통신사에서 꺼짐','unsupported-carrier':'지원하지 않는 통신사','number-required':'SIM 전화번호 확인 필요',
    'rcs-sip-rejected':'등록 거절','rcs-sip-network':'서버 연결 실패','rcs-sip-dns':'서버 주소 확인 실패',
    'rcs-sip-timeout':'서버 응답 대기 만료','rcs-sip-connect':'서버 연결 실패','rcs-sip-tls-rejected':'보안 연결 실패',
    'rcs-sip-certificate-invalid':'서버 인증서 확인 실패','rcs-sip-certificate-host':'서버 인증서 주소 불일치',
    'rcs-profile-invalid':'통신사 설정 확인 필요','rcs-auth-failed':'통신사 인증 실패','rcs-auth-required':'문자 인증 필요',
    'rcs-otp-timeout':'인증 문자 대기 만료','rcs-http-rejected':'설정 요청 거절'});

function value(section, field) {
    if (!section || section.state !== 'available')
        return labels[section && section.state] || '미확인';
    if (field === 'status' && section[field] === 'available') return '사용 가능';
    return labels[section[field]] || section[field] || '미확인';
}

return baseclass.extend({
    load: function() {
        return Promise.all([L.resolveDefault(inspect(), {state:'error'}), L.resolveDefault(inspectNetwork(), {})])
            .then(function(data) {
                var network=data[1] || {};
                return {ims:data[0] || {state:'error'},apn:network.selected && network.selected.apn};
            });
    },
    renderStatus: function(data) {
        var rows = [[data.backend === 'modemconfig-ko' ? '모뎀 처리' : '모뎀 IMS 조회',
            data.backend === 'modemconfig-ko' ? 'ModemConfig-KO' : labels[data.state] || '미확인'],
            ['IMS 등록', value(data.registration, 'status')],
            ['등록망', value(data.registration, 'technology')]];
        if (data.sim_slot) rows.splice(1, 0, ['SIM', 'SIM ' + data.sim_slot]);
        if (data.sms_transport === 'ims' || data.sms_transport === 'nas')
            rows.push(['문자 전달 방식', data.sms_transport === 'nas' ? 'LTE NAS' : 'IMS']);
        ['sms', 'voice', 'rcs'].forEach(function(id) {
            var section = data.services || {}, service = section[id] || {};
            rows.push([id === 'sms' ? '문자 서비스' : id === 'voice' ? 'IMS 음성 서비스' : 'RCS',
                value({ state: section.state, status: service.status }, 'status') + (id==='rcs'&&service.sip_status>=300?' · SIP '+service.sip_status:'')]);
        });
        var children = [];
        if (data.state === 'unsupported') children.push(E('p', {},
            '현재 모뎀은 IMS 상태를 제공하지 않습니다. IMS 등록 여부는 미확인입니다.'));
        children.push(E('table', { 'class': 'table' }, rows.map(function(row) {
                return E('tr', { 'class': 'tr' }, [
                    E('td', { 'class': 'td', 'style': 'width:35%' }, row[0]), E('td', { 'class': 'td' }, row[1])
                ]);
            })));
        return E('div', {}, children);
    },
    render: function(data) {
        var self = this, content = E('div', {}, this.renderStatus(data.ims));
        return E('div', {}, [
            content,
            E('button', { 'class': 'cbi-button cbi-button-action',
                'click': ui.createHandlerFn(this, function() {
                    return self.load().then(function(next) { content.replaceChildren(self.renderStatus(next.ims)); });
                }) }, '다시 조회'),
            ' ', E('a', { 'class': 'cbi-button', 'href': L.url('admin', 'messages') }, '메시지'),
            ' ', E('a', { 'class': 'cbi-button', 'href': L.url('admin', 'calls') }, '전화'),
            ' ', E('button', {'class':'cbi-button cbi-button-action',disabled:!L.hasViewPermission(),click:ui.createHandlerFn(this,function(){
                return inspectMessages().then(function(result){var s=result.service||{};return retryRCS(s.subscription,s.session);}).then(function(result){if(result.error)throw new Error('RCS 연결을 확인해 주세요.');});
            })},'RCS 다시 연결'),
            ' ', E('a', { 'class': 'cbi-button', 'href': L.url('admin', 'modem', 'apn') }, '모뎀 설정')
        ]);
    }
});
