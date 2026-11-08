'use strict';
'require baseclass';
'require fs';

const gsm7 = '@£$¥èéùìòÇ\nØø\rÅåΔ_ΦΓΛΩΠΨΣΘΞÆæßÉ !"#¤%&\'()*+,-./0123456789:;<=>?¡ABCDEFGHIJKLMNOPQRSTUVWXYZÄÖÑÜ§¿abcdefghijklmnopqrstuvwxyzäöñüà';
const extended = '\f^{}\\[~]|€';

return baseclass.extend({

	normalizeNumber: function(value, prefix) {
		let number = String(value).replace(/[\s().-]/g, '');
		if (!/^\+?[0-9]{3,15}$/.test(number))
			throw new Error(_('Please specify a valid phone number'));
		if (prefix === '+82') {
			if (/^0[1-9][0-9]{7,9}$/.test(number))
				number = '+82' + number.slice(1);
			number = number.replace(/^\+820(?=[1-9])/, '+82');
		}
		return number;
	},

	measure: function(text) {
		const chars = Array.from(text);
		const unicode = chars.some(c => !gsm7.includes(c) && !extended.includes(c));
		const lengths = chars.map(c => unicode ? c.length : (extended.includes(c) ? 2 : 1));
		const units = lengths.reduce((a, b) => a + b, 0);
		let parts = text ? 1 : 0, used = 0;
		if (units > (unicode ? 70 : 160)) {
			const capacity = unicode ? 67 : 153;
			for (const length of lengths) {
				if (used + length > capacity) { parts++; used = 0; }
				used += length;
			}
		}
		return {encoding: unicode ? 'Unicode' : 'GSM-7', units: units, parts: parts};
	},

	create: function(modem, number, text, prefix, centre) {
		let path, properties;
		try {
			number = this.normalizeNumber(number, prefix);
			properties = 'number=' + number;
			if (centre) properties += ',smsc=' + this.normalizeNumber(centre);
			if (!text || text.includes('\0'))
				throw new Error(_('Please specify a valid message to send'));
		} catch (err) {
			return Promise.reject(err);
		}
		// mmcli's key/value parser cannot escape both kinds of quotes in a body.
		return fs.exec('/bin/mktemp', ['/tmp/smsmgr_text.XXXXXX']).then(function(res) {
			if (res.code !== 0 || !/^\/tmp\/smsmgr_text\.[A-Za-z0-9]{6}$/.test(res.stdout.trim()))
				throw new Error(_('Unable to prepare SMS text'));
			path = res.stdout.trim();
			return fs.write(path, text, 384);
		}).then(function() {
			return fs.exec('/usr/bin/mmcli', ['-m', modem, '--messaging-create-sms=' + properties,
				'--messaging-create-sms-with-text=' + path]);
		}).then(function(res) {
			if (res.code !== 0)
				throw new Error(res.stderr || _('Failed to create SMS'));
			return res;
		}).finally(function() {
			return path ? fs.remove(path) : null;
		});
	},

	parse: function(value, id) {
		const sms = (typeof value === 'string' ? JSON.parse(value) : value).sms;
		if (!sms || !sms.content || !sms.properties)
			throw new Error(_('Invalid SMS response'));
		const timestamp = sms.properties.timestamp === '--' ? '' : (sms.properties.timestamp || '');
		return {index: id, sender: sms.content.number || '', content: sms.content.text || '',
			timestamp: timestamp, storage: (sms.properties.storage || '').toLowerCase(),
			date: new Date(timestamp.replace(/([+-]\d{2})$/, '$1:00') || 0)};
	}
});
