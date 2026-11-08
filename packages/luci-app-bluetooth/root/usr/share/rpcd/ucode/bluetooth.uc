#!/usr/bin/env ucode

'use strict';

import { popen } from 'fs';

const shellquote = (s) => {
	return `'${replace(s, "'", "'\\''")}'`;
};

const valid_mac = (mac) => type(mac) == 'string' && match(mac, /^([0-9a-fA-F]{2}:){5}[0-9a-fA-F]{2}$/) != null;

const run_command = (command, seconds) => {
	try {
		const cmd_str = `timeout ${seconds || 8} ${command} 2>&1`;
		const handle = popen(cmd_str, 'r');
		if (!handle) return null;

		let output = handle.read("all");
		if (handle.close() != 0) return null;

		return output;
	}
	catch (e) {
		return null;
	}
};

const run_btctl = (command, seconds) => run_command(`bluetoothctl --timeout 6 ${command}`, seconds);

const methods = {
	get_status: {
		call: function() {
			const nap_enabled = trim(run_command('uci -q get bt-nap.main.enabled') || '') === '1';
			const compatibility = trim(run_command('/usr/sbin/bt-compat status') || '');
			const pairing_compat_available = compatibility === '0' || compatibility === '1';
			const pairing_compat = compatibility === '1';
			const output = run_btctl('show');
			if (!output) {
				return {
					controller: 'Unknown',
					powered: false,
					name: 'Unknown',
					alias: 'Unknown',
					discoverable: false,
					pairable: false,
					discovering: false,
					nap_active: false,
					nap_enabled, pairing_compat_available, pairing_compat
				};
			}

			const parse_value = (regex) => {
				const m = match(output, regex);
				return m ? trim(m[1]) : null;
			};

			const parse_bool = (regex) => {
				const val = parse_value(regex);
				return val === 'yes';
			};

			return {
				controller: parse_value(/Controller\s+([0-9A-F:]+)/) || 'Unknown',
				powered: parse_bool(/Powered:\s*([a-z]+)/),
				name: parse_value(/Name:\s*([^\n]+)/) || 'Unknown',
				alias: parse_value(/Alias:\s*([^\n]+)/) || 'Unknown',
				discoverable: parse_bool(/Discoverable:\s*([a-z]+)/),
				pairable: parse_bool(/Pairable:\s*([a-z]+)/),
				discovering: parse_bool(/Discovering:\s*([a-z]+)/),
				nap_active: parse_bool(/Powered:\s*([a-z]+)/) && match(output, /00001116-0000-1000-8000-00805f9b34fb/) != null,
				nap_enabled, pairing_compat_available, pairing_compat
			};
		}
	},

	get_devices: {
		call: function() {
			try {
				const handle = popen('/usr/libexec/bluetooth/bt_device.sh', 'r');
				if (!handle) {
					return { devices: [] };
				}

				const output = handle.read('all');
				handle.close();

				if (!output) {
					return { devices: [] };
				}

				try {
					const result = json(output);

					if (result && result.devices) {
						return result;
					} else {
						return { devices: [] };
					}
				} catch (e) {
					return { devices: [] };
				}
			}
			catch (e) {
				return { devices: [] };
			}
		}
	},

	set_power: {
		args: { on: 0 },
		call: function(req) {
			const val = req.args.on;
			const is_on = (val === true || val === 1 || val === "1" || val === "true" || val === "on");
			const cmd = is_on ? 'power on' : 'power off';
			return { success: run_btctl(cmd) != null };
		}
	},

	set_discoverable: {
		args: { on: 0 },
		call: function(req) {
			if (req.args.on !== 0 && req.args.on !== 1) return { success: false, error: 'Invalid choice' };
			if (req.args.on === 1 && (run_btctl('discoverable-timeout 180') == null || run_btctl('pairable on') == null))
				return { success: false, error: 'Could not enable pairing' };
			return { success: run_btctl(req.args.on === 1 ? 'discoverable on' : 'discoverable off') != null };
		}
	},

	set_nap: {
		args: { on: 0 },
		call: function(req) {
			if (req.args.on !== 0 && req.args.on !== 1) return { success: false, error: 'Invalid choice' };
			return { success: run_command(req.args.on === 1 ? '/usr/sbin/bt-nap on' : '/usr/sbin/bt-nap off', 35) != null };
		}
	},
	set_pairing_compat: {
		args: { on: 0 },
		call: function(req) {
			if (req.args.on !== 0 && req.args.on !== 1) return { success: false, error: 'Invalid choice' };
			return { success: run_command(req.args.on === 1 ? '/usr/sbin/bt-compat on' : '/usr/sbin/bt-compat off', 60) != null };
		}
	},

	start_scan: {
		args: { on: 0 },
		call: function(req) {
			const val = req.args.on;
			const is_on = (val === true || val === 1 || val === "1" || val === "true" || val === "on");
			const cmd = is_on ? '--timeout 5 scan on' : 'scan off';
			return { success: run_btctl(cmd) != null };
		}
	},

	pair: {
		args: { mac: 'string' },
		call: function(req) {
			if (!valid_mac(req.args.mac)) {
				return { success: false, error: 'MAC address required' };
			}

			return { success: run_btctl(`--agent NoInputNoOutput --timeout 25 pair ${shellquote(req.args.mac)}`, 28) != null };
		}
	},

	connect: {
		args: { mac: 'string' },
		call: function(req) {
			if (!valid_mac(req.args.mac)) return { success: false, error: 'Invalid MAC address' };
			return { success: run_btctl(`connect ${shellquote(req.args.mac)}`) != null };
		}
	},

	disconnect: {
		args: { mac: 'string' },
		call: function(req) {
			if (!valid_mac(req.args.mac)) return { success: false, error: 'Invalid MAC address' };
			return { success: run_btctl(`disconnect ${shellquote(req.args.mac)}`) != null };
		}
	},

	remove_device: {
		args: { mac: 'string' },
		call: function(req) {
			try {
				if (!valid_mac(req.args.mac)) {
					return { success: false, error: 'MAC address required' };
				}

				return { success: run_btctl(`remove ${shellquote(req.args.mac)}`) != null };

			} catch (e) {
				return { success: false, error: 'Exception while removing device.', details: e };
			}
		}
	},

	set_alias: {
		args: { alias: 'string' },
		call: function(req) {
			if (type(req.args.alias) != 'string' || !length(req.args.alias) || match(req.args.alias, /[\x00-\x1f\x7f]/))
				return { success: false, error: 'Invalid alias' };
			return { success: run_btctl(`system-alias ${shellquote(req.args.alias)}`) != null };
		}
	},

	set_codec: {
		args: { data: {} },
		call: function(req) {
			try {
				if (!req.args.data) {
					return { success: false, error: 'Invalid request: "data" object is missing.' };
				}

				const mac = req.args.data.mac;
				const codec = req.args.data.codec;

				if (!valid_mac(mac)) return { success: false, error: 'Invalid MAC address' };
				if (!codec) return { success: false, error: 'Codec required in data object.' };

				const bluealsa_mac = replace(mac, /:/g, '_');
				const bluealsa_path = `/org/bluealsa/hci0/dev_${bluealsa_mac}/a2dpsrc/sink`;
				const cmd = `bluealsactl codec ${shellquote(bluealsa_path)} ${shellquote(codec)} 2>&1`;

				const handle = popen(cmd, 'r');
				if (!handle) {
					return { success: false, error: 'Failed to execute command via popen.' };
				}

				const output = handle.read('all');
				handle.close();

				if (output && trim(output).length > 0) {
					return { success: false, error: 'Command execution failed.', details: trim(output) };
				}

				return { success: true };
			} catch (e) {
				return { success: false, error: 'An unexpected exception occurred.', details: e.message };
			}
		}
	},

	set_volume: {
		args: { data: {} },
		call: function(req) {
			try {
				if (!req.args.data) {
					return { success: false, error: 'Invalid request: "data" object is missing.' };
				}

				const mac = req.args.data.mac;
				const volume = req.args.data.volume;

				if (!valid_mac(mac)) {
					return { success: false, error: 'MAC address required in data object.' };
				}

				if (type(volume) != 'int' || volume < 0 || volume > 127) {
					return { success: false, error: 'Volume must be an integer from 0 to 127.' };
				}

				const bluealsa_mac = replace(mac, /:/g, '_');
				const bluealsa_path = `/org/bluealsa/hci0/dev_${bluealsa_mac}/a2dpsrc/sink`;
				const cmd = `bluealsactl volume ${shellquote(bluealsa_path)} ${volume} 2>&1`;

				const handle = popen(cmd, 'r');
				if (!handle) {
					return { success: false, error: 'Failed to execute command via popen.' };
				}

				const output = handle.read('all');
				handle.close();

				if (output && trim(output).length > 0) {
					return { success: false, error: 'Command execution failed.', details: trim(output) };
				}

				return { success: true };
			} catch (e) {
				return { success: false, error: 'An unexpected exception occurred.', details: e.message };
			}
		}
	}
};

return { 'luci.bluetooth': methods };
