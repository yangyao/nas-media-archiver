#!/usr/bin/env python3
"""Exercise an actual archiver service, using disposable files only."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import socket
import subprocess
import tempfile
import time
import urllib.error
import urllib.parse
import urllib.request


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--binary', default='dist/archive')
    args = parser.parse_args()
    binary = str(Path(args.binary).resolve())
    with tempfile.TemporaryDirectory(prefix='archiver-smoke-') as directory:
        root = Path(directory).resolve()
        sources, archive, state = (root / p for p in ('sources', 'archive', 'state'))
        for path in (sources, archive, state):
            path.mkdir()
        batch = sources / 'iPhone batch'
        batch.mkdir()
        fixtures = {
            'IMG_20260701_120000.jpg': b'original photo fixture\n',
            'IMG_20260702_120000.PNG': b'original screenshot fixture\n',
            'IMG_20260703_120000.WEBP': b'original webp fixture\n',
        }
        for name, content in fixtures.items():
            (batch / name).write_bytes(content)
        (batch / 'unsupported.txt').write_text('retain me')
        thumbnail = batch / '.@__thumb'
        thumbnail.mkdir()
        (thumbnail / 'IMG_20260701_120001.jpg').write_bytes(b'not an original')
        outside = root / 'outside'
        outside.mkdir()
        (sources / 'escape').symlink_to(outside, target_is_directory=True)
        existing = archive / 'photos' / '2026' / '07' / ('20260701_120000_%d.jpg' % len(fixtures['IMG_20260701_120000.jpg']))
        existing.parent.mkdir(parents=True)
        existing.write_bytes(fixtures['IMG_20260701_120000.jpg'])
        token = os.urandom(32).hex()
        token_file = state / 'token'
        token_file.write_text(token)
        token_file.chmod(0o600)
        with socket.socket() as listener:
            listener.bind(('127.0.0.1', 0))
            port = listener.getsockname()[1]
        base = f'http://127.0.0.1:{port}'
        opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))

        def request(method, path, body=None, auth=True):
            headers = {'Content-Type': 'application/json'}
            if auth:
                headers['Authorization'] = f'Bearer {token}'
            data = json.dumps(body).encode() if body is not None else (b'' if method == 'POST' else None)
            req = urllib.request.Request(base + path, data=data, headers=headers, method=method)
            with opener.open(req, timeout=15) as response:
                return json.load(response)

        def rejected(method, path, body=None, auth=True):
            try:
                request(method, path, body, auth)
            except urllib.error.HTTPError as error:
                assert 400 <= error.code < 500, (path, error.code)
                payload = json.loads(error.read())
                assert payload.get('error', {}).get('message'), payload
                return
            raise AssertionError('Request was not rejected: ' + path)

        log_path = root / 'service.log'
        with log_path.open('w+') as log:
            process = subprocess.Popen([binary, 'serve', '--listen', f'127.0.0.1:{port}', '--state-dir', str(state), '--archive-base', str(archive), '--roots', f'{sources},{archive}', '--token-file', str(token_file), '--name', 'Smoke NAS'], stdout=log, stderr=log)
            try:
                deadline = time.monotonic() + 30
                while True:
                    try:
                        if request('GET', '/healthz', auth=False)['status'] == 'ok':
                            break
                    except (OSError, ValueError):
                        if process.poll() is not None or time.monotonic() >= deadline:
                            raise RuntimeError('Service did not become healthy')
                        time.sleep(.1)
                rejected('GET', '/api/v1/info', auth=False)
                info = request('GET', '/api/v1/info')
                assert info['name'] == 'Smoke NAS', info
                rejected('GET', '/api/v1/browse?path=' + urllib.parse.quote(str(outside)))
                rejected('GET', '/api/v1/browse?path=' + urllib.parse.quote(str(sources / 'escape')))
                listing = request('GET', '/api/v1/browse?path=' + urllib.parse.quote(str(sources)))
                assert any(entry['path'] == str(batch) for entry in listing['entries']), listing
                rejected('POST', '/api/v1/jobs', {'sourcePaths': [str(archive)], 'archiveBase': str(archive)})
                job = request('POST', '/api/v1/jobs', {'sourcePaths': [str(batch)], 'archiveBase': str(archive), 'cleanupEmptyDirectories': True})
                job_path = '/api/v1/jobs/' + job['id']
                deadline = time.monotonic() + 30
                while job['status'] == 'scanning':
                    assert time.monotonic() < deadline, job
                    time.sleep(.05)
                    job = request('GET', job_path)
                assert job['status'] == 'ready', job
                rejected('POST', '/api/v1/jobs', {'sourcePaths': [str(batch)], 'archiveBase': str(archive)})
                preview = request('GET', job_path + '/files?filter=all&limit=200')['files']
                assert not any('.@__thumb' in item['sourcePath'] for item in preview), preview
                assert all((batch / name).is_file() for name in fixtures), 'Preview moved sources'
                event_request = urllib.request.Request(base + '/api/v1/events', headers={'Authorization': 'Bearer ' + token})
                with opener.open(event_request, timeout=10) as stream:
                    while True:
                        line = stream.readline().decode()
                        if line.startswith('data:'):
                            event = json.loads(line[5:].strip())
                            assert event['id'] == job['id'], event
                            break
                job = request('POST', job_path + '/start')
                deadline = time.monotonic() + 60
                while job['status'] in ('queued', 'running', 'pausing'):
                    assert time.monotonic() < deadline, job
                    time.sleep(.1)
                    job = request('GET', job_path)
                assert job['status'] in ('completed', 'completed_with_errors'), job
                assert job['failedFiles'] == 0, job
                files = request('GET', job_path + '/files?filter=all&limit=200')['files']
                completed = [item for item in files if item['status'] == 'completed']
                assert len(completed) == len(fixtures), files
                for item in completed:
                    content = fixtures[Path(item['sourcePath']).name]
                    assert not Path(item['sourcePath']).exists(), item
                    assert Path(item['targetPath']).read_bytes() == content, item
                    assert item['sha256'] == hashlib.sha256(content).hexdigest(), item
                assert existing.read_bytes() == fixtures['IMG_20260701_120000.jpg']
                assert (batch / 'unsupported.txt').read_text() == 'retain me'
                assert (thumbnail / 'IMG_20260701_120001.jpg').is_file()
                assert job['cleanupStatus'] == 'residual', job
                # A second batch verifies empty-root removal after a clean archive.
                clean = sources / 'clean batch'
                clean.mkdir()
                (clean / 'IMG_20260704_120000.jpg').write_bytes(b'clean fixture')
                job2 = request('POST', '/api/v1/jobs', {'sourcePaths': [str(clean)], 'archiveBase': str(archive), 'cleanupEmptyDirectories': True})
                path2 = '/api/v1/jobs/' + job2['id']
                deadline = time.monotonic() + 30
                while job2['status'] == 'scanning':
                    assert time.monotonic() < deadline
                    time.sleep(.05)
                    job2 = request('GET', path2)
                assert job2['status'] == 'ready', job2
                request('POST', path2 + '/start')
                while True:
                    job2 = request('GET', path2)
                    if job2['status'] in ('completed', 'completed_with_errors', 'failed'):
                        break
                    assert time.monotonic() < deadline
                    time.sleep(.1)
                assert job2['status'] == 'completed' and job2['cleanupStatus'] == 'done', job2
                assert not clean.exists(), 'Empty batch directory was not removed'
                # Confirm duplicate skipping through the public API, retaining
                # the source and avoiding any extra destination file.
                duplicates = sources / 'duplicate batch'
                duplicates.mkdir()
                duplicate = duplicates / 'IMG_20260701_120000.jpg'
                duplicate.write_bytes(fixtures[duplicate.name])
                before = sorted(str(p) for p in archive.rglob('*') if p.is_file())
                job3 = request('POST', '/api/v1/jobs', {'sourcePaths': [str(duplicates)], 'archiveBase': str(archive), 'cleanupEmptyDirectories': True, 'skipDuplicates': True})
                path3 = '/api/v1/jobs/' + job3['id']
                deadline = time.monotonic() + 30
                while job3['status'] == 'scanning':
                    assert time.monotonic() < deadline
                    time.sleep(.05)
                    job3 = request('GET', path3)
                assert job3['skipDuplicates']
                rejected('POST', path3 + '/skip-duplicates', {})
                assert not request('POST', path3 + '/skip-duplicates', {'enabled': False})['skipDuplicates']
                assert request('POST', path3 + '/skip-duplicates', {'enabled': True})['skipDuplicates']
                request('POST', path3 + '/start')
                while True:
                    job3 = request('GET', path3)
                    if job3['status'] in ('completed', 'completed_with_errors', 'failed'):
                        break
                    assert time.monotonic() < deadline
                    time.sleep(.05)
                assert job3['status'] == 'completed' and job3['skippedFiles'] == 1 and job3['completedFiles'] == 0, job3
                assert duplicate.read_bytes() == fixtures[duplicate.name]
                assert sorted(str(p) for p in archive.rglob('*') if p.is_file()) == before
                assert job3['cleanupStatus'] == 'residual'
                print('PASS: auth, path containment, preview, SSE, archive, SHA-256, collisions, duplicate skipping with source retention, residuals and cleanup')
            except Exception:
                log.flush()
                log.seek(0)
                print(log.read()[-8000:])
                raise
            finally:
                process.terminate()
                try:
                    process.wait(timeout=20)
                except subprocess.TimeoutExpired:
                    process.kill()
                    process.wait()


if __name__ == '__main__':
    main()
