import assert from 'node:assert/strict';
import { spawnSync } from 'node:child_process';
import { createHash } from 'node:crypto';
import { copyFileSync, existsSync, mkdirSync, mkdtempSync, readFileSync, readdirSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import test from 'node:test';
import { fileURLToPath } from 'node:url';

const version = 'v0.2.1';
const releaseScript = new URL('./release.sh', import.meta.url);
const publishScript = fileURLToPath(new URL('./publish-release.sh', import.meta.url));

function fixture(t) {
    const root = mkdtempSync(join(tmpdir(), 'workbench-release-'));
    t.after(() => rmSync(root, { recursive: true, force: true }));
    return root;
}

function checksums(directory) {
    const entries = readdirSync(directory).filter(name => name.endsWith('.tar.gz'));
    writeFileSync(join(directory, 'SHA256SUMS'), entries.map(name =>
        `${createHash('sha256').update(readFileSync(join(directory, name))).digest('hex')}  ${name}\n`).join(''));
}

function buildArtifacts(root) {
    for (const name of ['scripts', 'doc', 'deploy']) mkdirSync(join(root, name));
    copyFileSync(releaseScript, join(root, 'scripts/release.sh'));
    for (const name of ['README.md', '.env.example', 'compose.image.yaml']) writeFileSync(join(root, name), 'fixture\n');
    for (const arch of ['amd64', 'arm64']) {
        const directory = join(root, 'builds', arch);
        mkdirSync(directory, { recursive: true });
        writeFileSync(join(directory, `workbench_${version}_linux_${arch}.tar.gz`), `verified binary: ${arch}\n`);
        checksums(directory);
    }
    return { ...process.env, VERSION: version, COMMIT: 'fixture', BUILD_ARTIFACTS_DIR: join(root, 'builds') };
}

test('release packages reuse the verified binary archives without a source build', t => {
    const root = fixture(t), env = buildArtifacts(root);
    const result = spawnSync('sh', [join(root, 'scripts/release.sh')], { env, encoding: 'utf8' });
    assert.equal(result.status, 0, result.stderr);
    const output = join(root, 'dist/release', version);
    for (const arch of ['amd64', 'arm64']) {
        const name = `workbench_${version}_linux_${arch}.tar.gz`;
        assert.deepEqual(readFileSync(join(output, name)), readFileSync(join(root, 'builds', arch, name)));
    }
    assert.equal(readdirSync(output).filter(name => name.endsWith('.tar.gz')).length, 3);
    const checked = spawnSync('sha256sum', ['--check', 'SHA256SUMS'], { cwd: output, encoding: 'utf8' });
    assert.equal(checked.status, 0, checked.stderr);
});

test('release packaging rejects a modified CI artifact', t => {
    const root = fixture(t), env = buildArtifacts(root);
    writeFileSync(join(root, 'builds/arm64', `workbench_${version}_linux_arm64.tar.gz`), 'changed');
    const result = spawnSync('sh', [join(root, 'scripts/release.sh')], { env, encoding: 'utf8' });
    assert.notEqual(result.status, 0);
    assert.match(result.stdout + result.stderr, /FAILED/);
});

function publish(t, current, published, failAPI = false) {
    const root = fixture(t), bin = join(root, 'bin'), packages = join(root, 'packages');
    mkdirSync(bin);
    mkdirSync(packages);
    writeFileSync(join(packages, 'fixture.tar.gz'), 'verified package');
    checksums(packages);
    const trace = join(root, 'calls.jsonl');
    for (const command of ['gh', 'docker']) writeFileSync(join(bin, command), `#!/usr/bin/env node
const fs = require('node:fs');
const args = process.argv.slice(2);
fs.appendFileSync(process.env.CALL_TRACE, JSON.stringify({ command: '${command}', args }) + '\\n');
if ('${command}' === 'gh' && args[0] === 'api') {
    if (process.env.FAIL_API === 'true') process.exit(1);
    process.stdout.write(process.env.PUBLISHED_VERSIONS);
}
`, { mode: 0o755 });
    const result = spawnSync('bash', [publishScript, packages], {
        encoding: 'utf8', env: {
            ...process.env, PATH: `${bin}:${process.env.PATH}`, CALL_TRACE: trace,
            VERSION: current, GH_REPO: 'example/workbench', GITHUB_SHA: 'fixture',
            IMAGE: 'ghcr.io/example/workbench', DIGEST: 'sha256:verified', RUNNER_TEMP: root,
            PUBLISHED_VERSIONS: published.join('\n'), FAIL_API: String(failAPI),
        },
    });
    const calls = existsSync(trace) ? readFileSync(trace, 'utf8').trim().split('\n').map(line => JSON.parse(line)) : [];
    return { result, calls };
}

for (const [current, published, promote] of [
    ['v0.2.1', ['v0.2.0'], true],
    ['v0.2.1', ['v0.3.0'], false],
    ['v0.10.0', ['v0.9.10'], true],
    ['v0.3.0-rc.1', ['v0.2.1'], false],
    ['v0.1.0', [], true],
]) test(`publishing ${current} after ${published.join(', ') || 'no releases'} ${promote ? 'updates' : 'preserves'} latest`, t => {
    const { result, calls } = publish(t, current, published);
    assert.equal(result.status, 0, result.stderr);
    const release = calls.find(call => call.command === 'gh' && call.args[0] === 'release');
    assert(release);
    assert(release.args.includes(promote ? '--latest' : '--latest=false'));
    assert.equal(release.args.includes('--prerelease'), current.includes('-'));
    const docker = calls.filter(call => call.command === 'docker');
    assert.equal(docker.length, promote ? 1 : 0);
    if (promote) assert.deepEqual(docker[0].args, [
        'buildx', 'imagetools', 'create', '--tag', 'ghcr.io/example/workbench:latest', 'ghcr.io/example/workbench@sha256:verified',
    ]);
});

test('a release listing failure stops publication and latest updates', t => {
    const { result, calls } = publish(t, version, [], true);
    assert.notEqual(result.status, 0);
    assert.equal(calls.length, 1);
    assert.equal(calls[0].args[0], 'api');
});
