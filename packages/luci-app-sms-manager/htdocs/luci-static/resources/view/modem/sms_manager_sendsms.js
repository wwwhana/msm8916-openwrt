'use strict';
'require dom';
'require form';
'require fs';
'require ui';
'require uci';
'require view';
'require sms_manager as sms';

/*
	Copyright 2022-2026 Rafał Wabik - IceG - From eko.one.pl forum
	
	Licensed to the GNU General Public License v3.0.
*/

return view.extend({

	updateMessageCounter: function() {
		const info = sms.measure(document.getElementById('smstext').value);
		document.getElementById('encoding').textContent = info.encoding;
		document.getElementById('counter').textContent = info.units + ' / ' + info.parts + ' SMS';
	},

	handleCommand: function(exec, args) {
		let buttons = document.querySelectorAll('.cbi-button');

		for (let i = 0; i < buttons.length; i++)
			buttons[i].setAttribute('disabled', 'true');

		return fs.exec(exec, args).then(function(res) {
			let out = document.querySelector('.smscommand-output');
			out.style.display = '';

			dom.content(out, [res.code === 0 ? _('SMS sent successfully!') :
				_('Error sending SMS: ') + (res.stderr || res.stdout || _('Unknown error'))]);

		}).catch(function(err) {
			ui.addNotification(null, E('p', [ _('Error: ') + err ]));
		}).finally(function() {
			for (let i = 0; i < buttons.length; i++)
				buttons[i].removeAttribute('disabled');
		});
	},

	handleGo: function(ev) {
		let sections = uci.sections('sms_manager');
		let modemPath = sections[0].sendport;
		let num = document.getElementById('phonenumber').value;
		let body = document.getElementById('smstext').value;

		let sendtype = ev.target.getAttribute('data-action');
		if (sendtype !== 'sendg' && num.length < 3) {
			ui.addNotification(null, E('p', _('Please specify the phone number')), 'info');
			return false;
		}

		if (body.length < 1) {
			ui.addNotification(null, E('p', _('Please specify the message to send')), 'info');
			return false;
		}

		if (!modemPath) {
			ui.addNotification(null, E('p', _('Please set the modem for communication')), 'info');
			return false;
		}

		if (sendtype === 'sendg') {
			return fs.read_direct('/etc/modem/sms_manager_phonebook.user').then(L.bind(function(phonebook) {
				if (!phonebook || phonebook.trim().length === 0) {
					ui.addNotification(null, E('p', _('Phone book is empty')), 'info');
					return false;
				}

				let lines = phonebook.trim().split('\n');
				let numbers = [];
				
				lines.forEach(function(line) {
					let parts = line.split(';');
					if (parts.length >= 2) {
						numbers.push(parts[1].trim());
					}
				});

				if (numbers.length === 0) {
					ui.addNotification(null, E('p', _('No valid phone numbers in phonebook')), 'info');
					return false;
				}

				let modemNum = modemPath.split('/').pop();
		
				let dx = 4000;
				
				for (let i = 0; i < numbers.length; i++) {
					(function(index) {
						setTimeout(function() {
							sms.create(modemNum, numbers[index], body, sections[0].pnumber, sections[0].smsc)
								.then(function(createRes) {
									let smsMatch = createRes.stdout.match(/\/SMS\/(\d+)/);
									if (smsMatch) {
										let smsId = smsMatch[1];
										// Send SMS
										return fs.exec('/usr/bin/mmcli', ['-s', smsId, '--send']).then(function(res) {
											if (res.code !== 0) throw new Error(res.stderr || _('Failed to send SMS'));
										});
									} else {
										throw new Error(_('Failed to create SMS'));
									}
								}).catch(function(err) {
									ui.addNotification(null, E('p', _('Error sending SMS: ') + err.message), 'error');
								});
						}, dx * index);
					})(i);
				}

				ui.addNotification(null, E('p', _('Sending SMS to %d contacts...').format(numbers.length)), 'info');
				
			}, this));
		} else {
			let modemNum = modemPath.split('/').pop();

			return sms.create(modemNum, num, body, sections[0].pnumber, sections[0].smsc)
				.then(L.bind(function(createRes) {
					let smsMatch = createRes.stdout.match(/\/SMS\/(\d+)/);
					if (smsMatch) {
						let smsId = smsMatch[1];
						return this.handleCommand('/usr/bin/mmcli', ['-s', smsId, '--send']);
					} else {
						ui.addNotification(null, E('p', _('Failed to create SMS')), 'error');
						return false;
					}
				}, this))
				.catch(function(err) {
					ui.addNotification(null, E('p', _('Error creating SMS: ') + err), 'error');
				});
		}
	},

	handleClear: function(ev) {
		let out = document.querySelector('.smscommand-output');
		out.style.display = '';
		out.style.display = 'none';

		let ovc = document.getElementById('phonenumber');
		let ov2 = document.getElementById('smstext');
		ov2.value = '';

		this.updateMessageCounter();

		let prefixnum, sections = uci.sections('sms_manager');
		let addprefix = sections[0].prefix;
		if ( addprefix == '1' )
			{
			prefixnum = sections[0].pnumber;
			ovc.value = prefixnum;
			}
		else {
			ovc.value = '';
		}

		document.getElementById('phonenumber').focus();
	},

	handleCopy: function(ev) {
		let out = document.querySelector('.smscommand-output');
		out.style.display = 'none';

		let ov = document.getElementById('phonenumber');
		ov.value = '';
		let x = document.getElementById('pb').value;
		ov.value = x;
	},

	load: function() {
		return Promise.all([
			L.resolveDefault(fs.read_direct('/etc/modem/sms_manager_phonebook.user'), null),
			uci.load('sms_manager')
		]);
	},

	render: function (loadResults) {

	let group, prefixnum, sections = uci.sections('sms_manager');
	let self = this;

	if ( sections[0].sendingroup == '1' )
		{
		group = 1;
	}
	else {
	group = '';
	}
	
	if ( sections[0].prefix == '1' ) {	
		prefixnum = sections[0].pnumber;
	}
	if ( sections[0].information == '1' ) {
		ui.addNotification(null, E('p', _('Enter a domestic number or an international number beginning with +.') ), 'info');
	}
		
		let info = _('User interface for sending messages using ModemManager.');
	
		return E('div', { 'class': 'cbi-map', 'id': 'map' }, [
				E('h2', {}, [ _('SMS Messages') ]),
				E('div', { 'class': 'cbi-map-descr'}, info),
				E('hr'),
				E('div', { 'class': 'cbi-section' }, [
					E('div', { 'class': 'cbi-section-node' }, [
						E('div', { 'class': 'cbi-value' }, [
							E('label', { 'class': 'cbi-value-title' }, [ _('User contacts') ]),
							E('div', { 'class': 'cbi-value-field' }, [
								E('select', { 'class': 'cbi-input-select',
										'id': 'pb',
										'style': 'margin:5px 0; width:100%;',
										'change': ui.createHandlerFn(this, 'handleCopy'),
										'mousedown': ui.createHandlerFn(this, 'handleCopy')
									    },
									(loadResults[0] || "").trim().split("\n").map(function(cmd) {
                                        let fields = cmd.split(/;/);
                                        let name = fields[0];
                                        let code = fields[1] || fields[0];
                                    return E('option', { 'value': code }, name );
                                    })
								)
							]) 
						]),
						E('div', { 'class': 'cbi-value' }, [
							E('label', { 'class': 'cbi-value-title' }, [ _('Send to') ]),
							E('div', { 'class': 'cbi-value-field' }, [
							E('input', {
								'style': 'margin:5px 0; width:100%;',
								'type': 'tel',
								'id': 'phonenumber',
								'value': prefixnum,
								'inputmode': 'tel',
								'placeholder': sections[0].pnumber === '+82' ? '010-1234-5678' : '+...',
								}),
							])
						]),
						E('div', { 'class': 'cbi-value' }, [
							E('label', { 'class': 'cbi-value-title' }, [ _('Message format') ]),
							E('div', { 'class': 'cbi-value-field' }, [
								E('span', { 'id': 'encoding' }, _('Automatic'))
							])
						]),
						E('div', { 'class': 'cbi-value' }, [
							E('label', { 'class': 'cbi-value-title' }, [ _('Message text') ]),
							E('div', { 'class': 'cbi-value-field' }, [
							E('textarea', {
								'id': 'smstext',
								'style': 'width: 100%; resize: vertical; height:80px; max-height:80px; min-height:80px; min-width:100%;',
								'wrap': 'on',
								'rows': '3',
								'placeholder': _(''),
								'input': function(ev) {
									self.updateMessageCounter();
								}
							}),
								E('div', { 'class': 'left' }, [
								E('br'),
								E('label', {}, [ _('Length / estimated SMS segments:')+' ' ]),
								E('label', { 'id': 'counter' }, [ '0 / 0 SMS' ])
								])
							]),
						]),

					])
				]),
				E('hr'),
				E('div', { 'class': 'right' }, [
					E('button', {
						'class': 'cbi-button cbi-button-remove',
						'id': 'clr',
						'click': ui.createHandlerFn(this, 'handleClear')
					}, [ _('Clear form') ]),
					'\xa0\xa0\xa0',
						E('span', { 'class': 'diag-action' }, [
							group ? new ui.ComboButton('send', {
								'send': '%s %s'.format(_('Send'), _('to number')),
								'sendg': '%s %s'.format(_('Send'), _('to group')),
							}, {
								'click': ui.createHandlerFn(this, 'handleGo'),
								'id': 'execute',
								'classes': {'send': 'cbi-button cbi-button-action important',
                                            'sendg': 'cbi-button cbi-button-action important',
                            },
                                'id': 'execute',
                            }).render() : E('button', {
                                'class': 'cbi-button cbi-button-action important',
                                'id': 'execute',
                                'click': ui.createHandlerFn(this, 'handleGo')
                            }, [ _('Send to number') ]),
                        ]),
                    ]),
                E('p', _('Status')),
                    E('pre', { 'class': 'smscommand-output', 'id': 'ans', 'style': 'display:none; border: 1px solid var(--border-color-medium); border-radius: 5px; font-family: monospace' }),
                ]);
            },

handleSaveApply: null,
handleSave: null,
handleReset: null
});
