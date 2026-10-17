"""Measure real HTTP against a disposable official-data test instance only.

Credentials must be generated for this test and remain in its private directory.
The output contains timings and public transaction IDs, never authentication data.
"""
import argparse
import base64
import hashlib
import hmac
import http.cookiejar
import json
import math
from pathlib import Path
import struct
import time
import urllib.parse
import urllib.request


def otp(secret):
    key = base64.b32decode(secret + '=' * ((8 - len(secret) % 8) % 8))
    digest = hmac.new(key, struct.pack('>Q', int(time.time() // 30)), hashlib.sha1).digest()
    offset = digest[-1] & 15
    return str((struct.unpack('>I', digest[offset:offset + 4])[0] & 0x7fffffff) % 1000000).zfill(6)


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('--url', default='http://localhost:8080')
    parser.add_argument('--test-auth', type=Path, required=True)
    parser.add_argument('--output', type=Path, required=True)
    parser.add_argument('--phase', required=True)
    parser.add_argument('--include-new', action='store_true')
    args = parser.parse_args()
    parsed = urllib.parse.urlparse(args.url)
    if parsed.hostname not in ('localhost', '127.0.0.1') or parsed.scheme != 'http':
        raise ValueError('Only a local isolated test instance is supported')
    if 'transaction-20261017-browser' not in args.test_auth.resolve().parts:
        raise ValueError('Refusing to read personal credentials; use the named disposable test')
    credentials = json.loads(args.test_auth.read_text())
    jar = http.cookiejar.CookieJar()
    client = urllib.request.build_opener(urllib.request.HTTPCookieProcessor(jar))
    csrf = ''

    def request(path, body=None):
        nonlocal csrf
        data = None if body is None else json.dumps(body).encode()
        headers = {'Origin': args.url, 'Content-Type': 'application/json', 'X-CSRF-Token': csrf}
        with client.open(urllib.request.Request(args.url + '/api' + path, data=data, headers=headers), timeout=30) as response:
            value = json.load(response)
        if isinstance(value, dict) and 'session' in value:
            csrf = value['session']['csrf']
        return value

    request('/auth/login', {'id': 'finance', 'password': 'UI-test-only_R7m2x9q4Z1s8'})
    request('/auth/totp/login', {'code': otp(credentials['secret'])})
    runs = request('/runs')
    run_id = next(run['run_id'] for run in runs if run['scope'] == 'all')
    scenarios = [
        ('source', {}), ('customer', {'customer': '13085'}), ('invoice', {'invoice': '489434'}),
        ('stock', {'stock': '85048'}),
        ('year_date', {'date_from': '2010-01-01', 'date_to': '2011-01-01'}),
        ('combination', {'customer': '13085', 'stock': '85048', 'date_from': '2009-01-01', 'date_to': '2012-01-01'}),
        ('deep_page', {'page': 10000}),
    ]
    if args.include_new:
        scenarios += [('country', {'country': 'United Kingdom'}), ('date_desc', {'sort': 'date', 'direction': 'desc'})]
    results = []
    for name, condition in scenarios:
        for repetition in range(3):
            query = {'run_id': run_id, 'page': 1, 'page_size': 50, **condition}
            started = time.perf_counter()
            page = request('/transactions?' + urllib.parse.urlencode(query))
            elapsed = (time.perf_counter() - started) * 1000
            results.append({'scenario': name, 'repetition': repetition + 1, 'milliseconds': elapsed,
                            'total': page['total'], 'page': page['page'], 'ids': [row['id'] for row in page['items']]})
    request('/auth/logout', {})
    args.output.parent.mkdir(parents=True, exist_ok=True)
    args.output.write_text(json.dumps({'phase': args.phase, 'samples_per_scenario': 3,
                                     'boundary': 'HTTP request through JSON decode; same machine, OS file cache not cleared; first sample retained separately',
                                     'results': results}, ensure_ascii=False, indent=2), encoding='utf-8')
    print(json.dumps({name: [round(r['milliseconds'], 2) for r in results if r['scenario'] == name] for name, _ in scenarios}))


if __name__ == '__main__':
    main()
