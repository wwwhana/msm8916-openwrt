'use strict';
'require dom';
'require form';
'require fs';
'require ui';
'require uci';
'require view';
'require rpc';
'require sms_manager as sms';

/*
	Copyright 2026 Rafał Wabik - IceG - From eko.one.pl forum
	
	Licensed to the GNU General Public License v3.0.
*/

var callForwardSMS = rpc.declare({
	object: 'sms_manager_sms_forward',
	method: 'forward',
	params: ['subject', 'message']
});

function popTimeout(a, message, timeout, severity) {
    ui.addTimeLimitedNotification(a, message, timeout, severity);
}

return view.extend({
	load: function() {
		document.head.append(E('style', {'type': 'text/css'},
		`
		#smsTable {
			width: 100%;
			border: 1px solid var(--border-color-medium) !important;
			border-collapse: collapse;
		}
		
		#smsTable th, #smsTable td {
			padding: 10px;
			vertical-align: top !important;
		}
		
		#smsTable th {
			text-align: left !important;
			border-top: 1px solid var(--border-color-medium) !important;
			border-bottom: 1px solid var(--border-color-medium) !important;
		}
		
		#smsTable td {
			border-bottom: 1px solid var(--border-color-medium) !important;
		}
		
		#smsTable td input[type="checkbox"] {
			float: left !important;
			margin: 0 auto !important;
			width: 17px !important;
		}
		
		#smsTable .message {
			text-align: left !important;
			white-space: pre-wrap;
			word-break: break-word;
		}
		
		@media screen and (min-width: 769px) {
			#smsTable .checker {
				width: 7%;
			}
			#smsTable .from {
				width: 11%;
			}
			#smsTable .received {
				width: 15%;
			}
			#smsTable .message {
				width: 67%;
			}
		}
		
		/* tablet */
		@media screen and (max-width: 768px) and (min-width: 481px) {
			#smsTable .checker {
				width: 10%;
			}
			#smsTable .from {
				width: 25%;
			}
			#smsTable .received {
				width: 25%;
			}
			#smsTable .message {
				width: 40%;
			}
		}
		`));
		
		return uci.load('sms_manager');
	},

	handleDelete: function(ev) {
		let checked = document.querySelectorAll('input[name="smsn"]:checked');
		let allCheckboxes = document.querySelectorAll('input[name="smsn"]');
		
		if (checked.length === 0) {
			ui.addNotification(null, E('p', _('Please select the message(s) to be deleted')), 'info');
			return;
		}

		let confirmMessage;
		if (checked.length === allCheckboxes.length) {
			confirmMessage = _('Delete all the messages?');
		} else {
			confirmMessage = _('Delete selected message(s)?');
		}
		
		if (!confirm(confirmMessage)) {
			return;
		}
		
		uci.load('sms_manager').then(function() {
			let modemPath = uci.get('sms_manager', '@sms_manager[0]', 'readport');
			
			if (!modemPath) {
				ui.addNotification(null, E('p', _('Please set the modem for communication')), 'info');
				return;
			}
			
			let modemNum = modemPath.split('/').pop();
			let smsIds = [];
			
			checked.forEach(function(checkbox) {
				let smsId = checkbox.getAttribute('data-sms-id');
				if (smsId) {
					smsIds.push(smsId);
				}
			});
			
			if (smsIds.length === 0) {
				ui.addNotification(null, E('p', _('No valid SMS IDs found')), 'error');
				return;
			}
			
			let deletePromises = smsIds.map(function(smsId) {
				return fs.exec('/usr/bin/mmcli', ['-m', modemNum, '--messaging-delete-sms=' + smsId]);
			});
			
			Promise.all(deletePromises).then(function() {
				return fs.exec('/usr/bin/mmcli', ['-m', modemNum, '--messaging-list-sms']).then(function(listResult) {
					let remainingSms = 0;
					if (listResult && listResult.stdout) {
						let matches = listResult.stdout.matchAll(/\/SMS\/(\d+)/g);
						for (let match of matches) {
							remainingSms++;
						}
					}
					
					return uci.load('sms_manager').then(function() {
						try {
							uci.set('sms_manager', '@sms_manager[0]', 'sms_count', String(remainingSms));
							uci.save();
							uci.apply();
						} catch (e) {}
						
						ui.addNotification(null, E('p', _('Message(s) deleted successfully')), 'info');
						window.setTimeout(function() {
							location.reload();
						}, 5000);
					});
				});
			}).catch(function(err) {
				ui.addNotification(null, E('p', _('Error deleting messages: ') + err.message), 'error');
			});
		});
	},

	handleForward: function(ev) {
		let checked = document.querySelectorAll('input[name="smsn"]:checked');
		
		if (checked.length === 0) {
			ui.addNotification(null, E('p', _('Please select the message(s) to be forwarded')), 'info');
			return;
		}
		
		let self = this;
		
		uci.load('sms_manager').then(function() {
			let fwdEnabled = uci.get('sms_manager', '@sms_manager[0]', 'forward_sms_enabled');
			
			if (fwdEnabled !== '1') {
				ui.addNotification(null, E('p', _('SMS forwarding function is not enabled')), 'info');
				return;
			}
			
			let emailSubject = '';
			let emailBody = '';

			if (checked.length === 1) {
				let row = checked[0].closest('tr');
				let cells = row.cells;
				
				let sender = cells[1].textContent.trim();
				let timestamp = cells[2].textContent.trim();
				let message = cells[3].textContent.trim();
				
				emailSubject = 'SMS ' + timestamp + ' - ' + sender;
				emailBody = message;
				
				self.showEmailModal(emailSubject, emailBody);
			} 
			else {
				uci.load('system').then(function() {
					let hostname = uci.get('system', '@system[0]', 'hostname') || _('My Router');
					
					let messages = [];
					checked.forEach(function(checkbox) {
						let row = checkbox.closest('tr');
						let cells = row.cells;
						
						let timestamp = cells[2].textContent.trim();
						let sender = cells[1].textContent.trim();
						let message = cells[3].textContent.trim();
						
						messages.push(timestamp + ' - ' + sender + '\n' + message);
					});
					
					let emailBody = messages.join('\n\n');
					
					self.showEmailModal(hostname, emailBody);
				});
			}
		});
	},

	showEmailModal: function(defaultSubject, defaultBody) {
		let self = this;
		
		ui.showModal(_('Forward SMS to E-mail'), [
			E('p', _('Subject:')),
			E('input', {
				'type': 'text',
				'id': 'email-subject',
				'class': 'cbi-input-text',
				'style': 'width: 100% !important; margin-bottom: 15px;',
				'value': defaultSubject
			}),
			E('p', _('Message text:')),
			E('textarea', {
				'id': 'email-body',
				'class': 'cbi-input-textarea',
				'style': 'width: 100% !important; height: 30vh; min-height: 250px;',
				'wrap': 'off',
				'spellcheck': 'false'
			}, defaultBody),
			E('div', { 'class': 'right' }, [
				E('button', {
					'class': 'btn',
					'click': ui.hideModal
				}, _('Cancel')), ' ',
				E('button', {
					'class': 'cbi-button cbi-button-action important',
					'click': ui.createHandlerFn(this, 'sendEmailFromModal')
				}, _('Send'))
			])
		], 'cbi-modal');
	},

	sendEmailFromModal: function() {
		let subject = document.getElementById('email-subject').value;
		let body = document.getElementById('email-body').value;
		
		if (!subject || !body) {
			ui.addNotification(null, E('p', _('Subject and body cannot be empty')), 'error');
			return;
		}
		
		let self = this;
		
		ui.hideModal();
		
		let contentArea = document.getElementById('forward-status');
		contentArea.style.display = 'block';
		contentArea.innerHTML = '';
		contentArea.appendChild(E('div', {'class': 'alert alert-info'}, 
			E('span', {'class': 'spinning'}, _('Sending e-mail...'))
		));
		
		callForwardSMS(subject, body).then(function(response) {
			contentArea.innerHTML = '';
			if (response.success) {
			    popTimeout(null, E('p', _('Message forwarded successfully')), 5000, 'info');
				setTimeout(function() {
					if (contentArea) {
						contentArea.innerHTML = '';
						contentArea.style.display = 'none';
					}
				}, 5000);
			} else {
				contentArea.innerHTML = '';
				contentArea.style.display = 'none';
				ui.addNotification(null, E('p', _('Failed to forward message: %s').format(response.error || 'Unknown error')), 'error');
			}
		}).catch(function(err) {
			contentArea.innerHTML = '';
			contentArea.style.display = 'none';
			ui.addNotification(null, E('p', _('Error sending e-mail: %s').format(err.message || 'Unknown error')), 'error');
		});
	},

	handleSelect: function(ev) {
		let checked = ev.target.checked;
		let checkboxes = document.querySelectorAll('input[name="smsn"]');
		checkboxes.forEach(function(checkbox) {
			checkbox.checked = checked;
		});
	},

	handleRefresh: function(ev) {
		window.location.reload();
	},

	render: function() {
		let self = this;
		
		return uci.load('sms_manager').then(function() {
			let modemPath = uci.get('sms_manager', '@sms_manager[0]', 'readport');
			let hideNumber = uci.get('sms_manager', '@sms_manager[0]', 'bnumber') || '';
			let ledNotify = uci.get('sms_manager', '@sms_manager[0]', 'lednotify') || '0';
			let ledType = uci.get('sms_manager', '@sms_manager[0]', 'ledtype') || 'S';
			let smsLed = uci.get('sms_manager', '@sms_manager[0]', 'smsled') || '';
			
			if (ledNotify === '1') {
				if (ledType === 'S') {
					fs.exec_direct('/etc/init.d/led', ['restart']);
				} else if (ledType === 'D' && smsLed) {
					fs.write('/sys/class/leds/' + smsLed + '/brightness', '0');
				}
			}

			let smsTable = E('table', { 
				'class': 'table',
				'id': 'smsTable'
			}, [
				E('tr', { 'class': 'tr table-titles' }, [
					E('th', { 'class': 'th checker' }, 
						E('input', {
							'id': 'ch-all',
							'type': 'checkbox',
							'name': 'checkall',
							'click': ui.createHandlerFn(self, 'handleSelect')
						})
					),
					E('th', { 'class': 'th from' }, _('Sender')),
					E('th', { 'class': 'th received' }, _('Received')),
					E('th', { 'class': 'th message' }, _('Message'))
				])
			]);
			
			if (!modemPath) {
				ui.addNotification(null, E('p', _('Please configure the SMS reading modem first')), 'info');
			} else {
				let modemNum = modemPath.split('/').pop();
				
				L.resolveDefault(fs.exec_direct('/usr/bin/mmcli', ['-m', modemNum, '--messaging-list-sms']), null)
					.then(function(listRes) {
						
						if (!listRes) {
							let emptyRow = E('tr', { 'class': 'tr placeholder' }, [
								E('td', { 'class': 'td', 'colspan': '4', 'style': 'text-align:center; padding:20px;' }, 
									_('No SMS messages found')
								)
							]);
							smsTable.appendChild(emptyRow);
							return;
						}
						
						let smsListOutput = listRes;

						let smsIds = [];
						let matches = smsListOutput.matchAll(/\/SMS\/(\d+)/g);
						for (let match of matches) {
							smsIds.push(match[1]);
						}
						
						
						if (smsIds.length === 0) {
							let emptyRow = E('tr', { 'class': 'tr placeholder' }, [
								E('td', { 'class': 'td', 'colspan': '4', 'style': 'text-align:center; padding:20px;' }, 
									_('No SMS messages found')
								)
							]);
							smsTable.appendChild(emptyRow);
							return;
						}
						
						let smsPromises = smsIds.map(function(smsId) {
							return fs.exec_direct('/usr/bin/mmcli', ['-s', smsId, '-J'], 'json');
						});
						
						Promise.all(smsPromises).then(function(smsResults) {
							
							let smsList = [];
							let simCount = 0;
							let memoryCount = 0;
							
							smsResults.forEach(function(smsRes, index) {
								const item = sms.parse(smsRes, smsIds[index]);
								if (item.storage === 'sm') simCount++;
								else if (item.storage === 'me') memoryCount++;
								smsList.push(item);
							});

							let sortedSmsList = smsList.sort(function(a, b) {
								return b.date - a.date;
							});
							
							let storageInfo = document.getElementById('storage-info');
							if (storageInfo) {
								storageInfo.innerHTML = 'SIM: ' + simCount + ' / Memory: ' + memoryCount;
							}
							
							return uci.load('sms_manager').then(function() {
								try {
									uci.set('sms_manager', '@sms_manager[0]', 'sms_count', String(sortedSmsList.length));
									uci.save();
									uci.apply();
								} catch (e) {}
							}).then(function() {
								let Lres = L.resource('icons/sms_manager_delsms.png');
								let iconz = String.format('<img style="width: 24px; height: 24px; "src="%s"/>', Lres);
								
								sortedSmsList.forEach(function(sms, i) {
									let displayNumber = sms.sender;
									
									if (hideNumber && sms.sender.includes(hideNumber)) {
										let removeLast5 = sms.sender.slice(0, -5);
										displayNumber = removeLast5 + '#####';
									}
									
									let displayTime = sms.timestamp;
									try {
										let date = sms.date;
										if (date && !isNaN(date.getTime())) {
											displayTime = date.getFullYear() + '-' + 
												String(date.getMonth() + 1).padStart(2, '0') + '-' + 
												String(date.getDate()).padStart(2, '0') + ' ' +
												String(date.getHours()).padStart(2, '0') + ':' + 
												String(date.getMinutes()).padStart(2, '0');
										}
									} catch(e) {
										// timestamp
									}
									
									let row = E('tr', { 'class': 'tr cbi-rowstyle-%d'.format(i % 2 ? 2 : 1) }, [
										E('td', { 'class': 'td checker' }, [
											E('input', {
												'type': 'checkbox',
												'name': 'smsn',
												'data-sms-id': sms.index
											}),
											E('span', { 'style': 'margin-left: 5px;' }, E('raw', iconz))
										]),
										E('td', { 'class': 'td from' }, displayNumber),
										E('td', { 'class': 'td received' }, displayTime),
										E('td', { 'class': 'td message' }, sms.content)
									]);
									
									smsTable.appendChild(row);
								});
							});
						}).catch(function(err) {
							ui.addNotification(null, E('p', _('Error loading SMS details: ') + err.message), 'error');
						});
					})
					.catch(function(err) {
						ui.addNotification(null, E('p', _('Error listing SMS: ') + err.message), 'error');
					});
			}
			
			let v = E([], [
				E('h2', _('SMS Messages')),
				E('div', { 'class': 'cbi-map-descr' }, _('User interface for reading messages using ModemManager.')),

				E('h3', _('Received Messages')),
				E('table', { 
					'class': 'table',
					'style': 'width:100%; table-layout:fixed; border-collapse:collapse;'
				}, [
					E('tr', { 'class': 'tr' }, [
						E('td', { 
							'class': 'td left',
							'style': 'width:33%; padding:10px; text-align:left!important; vertical-align:top!important; border-top:1px solid var(--border-color-medium)!important;'
						}, [ _('Message storage area') ]),
						E('td', { 
							'class': 'td left',
							'id': 'storage-area',
							'style': 'padding:10px; text-align:left!important; vertical-align:top!important; border-top:1px solid var(--border-color-medium)!important;'
						}, [ _('SM / ME (Automatic selection)') ])
					]),
					E('tr', { 'class': 'tr' }, [
						E('td', { 
							'class': 'td left',
							'style': 'width:33%; padding:10px; text-align:left!important; vertical-align:top!important; border-top:1px solid var(--border-color-medium)!important;'
						}, [ _('Storage SM / ME used') ]),
						E('td', { 
							'class': 'td left',
							'id': 'storage-info',
							'style': 'padding:10px; text-align:left!important; vertical-align:top!important; border-top:1px solid var(--border-color-medium)!important;'
						}, [ 
							E('span', {'class': 'spinning'}, _('Loading...'))
						])
					])
				]),

				E('div', {'id': 'forward-status', 'style': 'margin: 10px 0; display: none;'}),

				E('div', { 'class': 'right' }, [
					E('button', {
						'class': 'cbi-button cbi-button-apply',
						'id': 'forward',
						'click': ui.createHandlerFn(self, 'handleForward')
					}, [ _('Forward SMS') ]),
					'\xa0\xa0\xa0',
					E('button', {
						'class': 'cbi-button cbi-button-remove',
						'id': 'execute',
						'click': ui.createHandlerFn(self, 'handleDelete')
					}, [ _('Delete') ]),
					'\xa0\xa0\xa0',
					E('button', {
						'class': 'cbi-button cbi-button-add',
						'id': 'clr',
						'click': ui.createHandlerFn(self, 'handleRefresh')
					}, [ _('Refresh') ]),
				]),
				E('p'),
				smsTable
			]);
			return v;
		});
	},

	handleSaveApply: null,
	handleSave: null,
	handleReset: null
});
