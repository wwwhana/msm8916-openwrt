#!/usr/bin/env python3
"""Build offline Internet APN lists, keyed by the SIM's home MCC+MNC."""
import hashlib
import json
from pathlib import Path
import re
import sys
import xml.etree.ElementTree as ET


def compile_database(xml):
    db = {}
    for country in ET.fromstring(xml).findall('country'):
        for provider in country.findall('provider'):
            name = provider.findtext('name') or country.get('code', '')
            for gsm in provider.findall('gsm'):
                codes = [n.get('mcc', '') + n.get('mnc', '') for n in gsm.findall('network-id')]
                for apn in gsm.findall('apn'):
                    usage = {n.get('type') for n in apn.findall('usage')}
                    if usage and 'internet' not in usage:
                        continue
                    value = apn.get('value', '')
                    if not re.fullmatch(r'[A-Za-z0-9][A-Za-z0-9.-]{0,99}', value):
                        continue
                    label = apn.findtext('name') or name
                    auth = apn.find('authentication')
                    row = dict(provider=name, name=label, country=country.get('code', ''), apn=value,
                               username=apn.findtext('username') or '', password=apn.findtext('password') or '',
                               iptype='ipv4v6')
                    row['auth'] = auth.get('method', 'none') if auth is not None else (
                        'both' if row['username'] or row['password'] else 'none')
                    row['id'] = hashlib.sha256(json.dumps(row, sort_keys=True).encode()).hexdigest()[:16]
                    for code in codes:
                        if re.fullmatch(r'\d{5,6}', code):
                            rows = db.setdefault(code, [])
                            if not any(r['id'] == row['id'] for r in rows):
                                rows.append(row)
    for rows in db.values():
        # Prefer LTE/5G entries over a carrier's older generic Internet APN.
        rows.sort(key=lambda r: (not bool(re.search(r'\b(lte|4g|5g)\b', r['name'], re.I)), r['provider']))
    return db


if __name__ == '__main__':
    data = compile_database(Path(sys.argv[1]).read_bytes())
    dest = Path(sys.argv[2]); dest.mkdir(parents=True, exist_ok=True)
    for code, profiles in data.items():
        (dest / (code + '.json')).write_text(json.dumps(profiles, ensure_ascii=False, separators=(',', ':')) + '\n')
    print(f'{len(data)} home networks, {sum(map(len, data.values()))} Internet APN entries')
