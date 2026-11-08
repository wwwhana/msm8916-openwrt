'use strict';
'require baseclass';
'require rpc';

var specs = {
    snapshot: [], call_history: ['before'], thread: ['id', 'before'], read_part: ['id', 'offset'],
    send: ['subscription', 'session', 'request_id', 'to', 'text', 'attachments', 'protocol'],
    measure: ['subscription', 'session', 'text', 'attachment_count', 'protocol', 'to'],
    begin_upload: ['subscription', 'session', 'request_id', 'filename', 'mime_type', 'size'],
    write_upload: ['subscription', 'session', 'id', 'offset', 'data'],
    delete_upload: ['subscription', 'session', 'id'],
    mark_read: ['id', 'through'], delete_thread: ['id'],
    save_contact: ['contact'], delete_contact: ['id']
};
var calls = {};
Object.keys(specs).forEach(function(method) {
    calls[method] = rpc.declare({ object: 'modemconfig-ko', method: method, params: specs[method], expect: {} });
});
var errors = {
    'invalid-contact': '이름과 전화번호를 확인하세요.',
    'contact-conflict': '다른 연락처에 저장된 번호입니다.',
    'invalid-message': '받는 번호와 문자 내용을 확인하세요.',
    'attachment-unavailable': '첨부파일을 읽을 수 없습니다.',
    'invalid-attachment': '첨부파일을 다시 선택하세요.',
    'attachment-storage': '첨부파일을 지우고 다시 선택하세요.',
    'attachment-type': 'JPG·PNG·GIF 사진, AMR·MP3 음악, MP4·3GP 동영상을 선택하세요.',
    'attachment-dimensions': '사진 크기를 줄인 뒤 다시 선택하세요.',
    'message-too-long': '통신사의 문자 길이 제한을 넘었습니다.',
    'message-too-large': '첨부파일의 크기를 줄여 주세요.',
    'unsupported-carrier': '이 SIM의 문자 설정을 지원하지 않습니다.',
    'request-conflict': '기존 전송 요청과 내용이 다릅니다. 전송 상태를 확인하세요.',
    'sim-unavailable': '네트워크 연결이 바뀌었습니다. 상태를 다시 확인하세요.',
    'busy': '전송 중인 문자가 있습니다. 전송 상태를 확인한 뒤 삭제하세요.',
    'database-unavailable': '메시지 저장소를 확인할 수 없습니다.'
};
return baseclass.extend({
    call: function(method) {
        return calls[method].apply(null, Array.prototype.slice.call(arguments, 1)).then(function(result) {
            if (result.error) throw new Error(errors[result.error] || '요청을 처리하지 못했습니다.');
            return result;
        }, function() { throw new Error('기기와 연결할 수 없습니다. 연결 후 다시 확인하세요.'); });
    },
    requestID: function() {
        var bytes = new Uint8Array(16);
        window.crypto.getRandomValues(bytes);
        return Array.from(bytes, function(value) { return value.toString(16).padStart(2, '0'); }).join('');
    },
    normalize: function(number) {
        number = String(number || '').replace(/[\s().-]/g, '');
        if (/^0\d{8,10}$/.test(number)) number = '+82' + number.slice(1);
        if (/^\+820/.test(number)) number = '+82' + number.slice(4);
        if (!/^\+?\d{3,15}$/.test(number)) throw new Error('전화번호를 확인하세요.');
        return number;
    },
    prepareAttachment: async function(file, policy, usedBytes) {
        var budget = policy.message_bytes - 16384 - usedBytes;
        if (budget < 1024 || file.size < 1 || file.size > 20 * 1024 * 1024) throw new Error('첨부파일 크기를 줄여 주세요.');
        if (!/^image\/(jpeg|png|gif)$/.test(file.type)) {
            if (!/^(audio\/(amr|mpeg)|video\/(mp4|3gpp))$/.test(file.type)) throw new Error(errors['attachment-type']);
            if (file.size > budget) throw new Error('음악·동영상은 1MB보다 작게 줄여 주세요.');
            return file;
        }
        var bitmap = await createImageBitmap(file);
        try {
            var scale = Math.min(1, policy.image_width / bitmap.width, policy.image_height / bitmap.height);
            if (scale === 1 && file.size <= budget) return file;
            if (file.type === 'image/gif') throw new Error('GIF는 크기를 줄인 뒤 선택하세요.');
            var canvas = document.createElement('canvas');
            for (var attempt = 0; attempt < 15; attempt++) {
                canvas.width = Math.max(1, Math.floor(bitmap.width * scale));
                canvas.height = Math.max(1, Math.floor(bitmap.height * scale));
                var context = canvas.getContext('2d');
                context.fillStyle = '#ffffff'; context.fillRect(0, 0, canvas.width, canvas.height);
                context.drawImage(bitmap, 0, 0, canvas.width, canvas.height);
                var quality = attempt % 3 === 0 ? .9 : attempt % 3 === 1 ? .75 : .6;
                var blob = await new Promise(function(resolve) { canvas.toBlob(resolve, 'image/jpeg', quality); });
                if (blob && blob.size <= budget) return new File([blob], file.name.replace(/\.[^.]*$/, '') + '.jpg', {type: 'image/jpeg'});
                if (attempt % 3 === 2) scale *= .75;
            }
            throw new Error('사진 크기를 줄일 수 없습니다. 다른 사진을 선택하세요.');
        } finally { bitmap.close(); }
    },
    uploadAttachment: async function(file, subscription, session, canceled) {
        var id, complete = false;
        try {
            var begin = await this.call('begin_upload', subscription, session, this.requestID(), file.name, file.type, file.size);
            id = begin.id;
            if (!/^[0-9a-f]{64}$/.test(id) || begin.offset !== 0) throw new Error('첨부파일 저장을 시작하지 못했습니다.');
            var bytes = new Uint8Array(await file.arrayBuffer()), offset = 0;
            while (offset < bytes.length) {
                if (canceled()) throw new Error('SIM 또는 대화가 바뀌어 첨부를 취소했습니다.');
                var chunk = bytes.slice(offset, offset + 8192);
                var result = await this.call('write_upload', subscription, session, id, offset, btoa(String.fromCharCode.apply(null, chunk)));
                if (result.id !== id || result.offset !== offset + chunk.length) throw new Error('첨부파일을 끝까지 저장하지 못했습니다.');
                offset = result.offset; complete = result.complete;
            }
            if (!complete || canceled()) throw new Error('첨부파일 저장을 취소했습니다.');
            return {id: id, filename: file.name, mime_type: file.type, byte_size: file.size};
        } catch (error) {
            if (id) await this.call('delete_upload', subscription, session, id).catch(function() {});
            throw error;
        }
    },
    readAttachment: function(part, canceled) {
        var self = this, chunks = [], offset = 0, size = Number(part.byte_size);
        if (!Number.isSafeInteger(size) || size < 0 || size > 8 * 1024 * 1024)
            return Promise.reject(new Error('첨부파일 크기를 확인할 수 없습니다.'));
        function next() {
            if (canceled && canceled()) return Promise.reject(new Error('첨부파일 읽기를 취소했습니다.'));
            return self.call('read_part', part.id, offset).then(function(result) {
                if (result.offset !== offset || result.size !== size || result.mime_type !== part.mime_type)
                    throw new Error('첨부파일 정보가 바뀌었습니다. 다시 열어 주세요.');
                var decoded = atob(result.data);
                if (decoded.length > 24 * 1024 || offset + decoded.length > size || (!decoded.length && offset < size))
                    throw new Error('첨부파일을 끝까지 읽지 못했습니다.');
                chunks.push(Uint8Array.from(decoded, function(character) { return character.charCodeAt(0); }));
                offset += decoded.length;
                if (offset < size) return next();
                var bytes = new Uint8Array(size), position = 0;
                chunks.forEach(function(chunk) { bytes.set(chunk, position); position += chunk.length; });
                return bytes;
            });
        }
        return next();
    },
    mmsStateLabel: function(state) {
        return ({mms_waiting: 'MMS 수신 대기', mms_downloading: 'MMS 받는 중',
            mms_download_failed: 'MMS 수신 실패 · 다시 시도 중',
            mms_expired: 'MMS 보관 기간이 지났습니다', mms_unavailable: 'MMS를 받을 수 없습니다'})[state] || 'MMS';
    },
    stateLabel: function(state) {
        return ({ queued: '전송 대기', sending: '전송 중', mms_sending: '전송 중', submitted_to_ims: '전송 확인 중', submitted_to_modem: '모뎀 접수',
            accepted_by_mmsc: 'MMS센터 접수',
            accepted_by_smsc: '문자센터 접수', delivered: '전달됨', delivery_completed: '처리됨',
            delivery_unknown: '전달 여부 미확인', submit_unknown: '전송 여부 미확인',
            rejected_by_smsc: '문자센터 거절', delivery_failed: '전달 실패',
            'not-sent': '전송 안 됨', delivery_retrying: '망에서 재시도 중' })[state] || '상태 미확인';
    }
});
