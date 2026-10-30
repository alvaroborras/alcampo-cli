'use strict';

// Resolves and downloads the prebuilt alcampo binary that matches this package
// version + the host platform, from the GitHub Release. Used by bin/run.js, which
// fetches the binary lazily on first run and caches it in bin/ (no install-time
// scripts, so it works cleanly under npm's/pnpm's default --ignore-scripts).

const fs = require('fs');
const path = require('path');
const https = require('https');
const { execFileSync } = require('child_process');

const REPO = 'alvaroborras/alcampo-cli';
const pkg = require('../package.json');
const BIN_DIR = path.join(__dirname, '..', 'bin');

// getTarget maps the Node platform/arch to the GoReleaser asset naming.
function getTarget() {
  const platform = { darwin: 'darwin', linux: 'linux', win32: 'windows' }[process.platform];
  const arch = { x64: 'amd64', arm64: 'arm64' }[process.arch];
  if (!platform || !arch) {
    throw new Error(
      `unsupported platform ${process.platform}/${process.arch}; supported: ` +
        `darwin|linux|windows on amd64|arm64. Download manually: https://github.com/${REPO}/releases`
    );
  }
  return {
    platform,
    arch,
    ext: platform === 'windows' ? 'zip' : 'tar.gz',
    binname: platform === 'windows' ? 'alcampo.exe' : 'alcampo',
  };
}

function binaryPath() {
  return path.join(BIN_DIR, getTarget().binname);
}

// download follows redirects (GitHub release assets 302 to a CDN) and resolves
// once the file is fully written.
function download(url, dest, redirects = 0) {
  return new Promise((resolve, reject) => {
    if (redirects > 10) return reject(new Error('too many redirects'));
    const req = https.get(url, { headers: { 'User-Agent': '@alvaroborras/alcampo installer' } }, (res) => {
      if (res.statusCode >= 300 && res.statusCode < 400 && res.headers.location) {
        res.resume();
        return resolve(download(res.headers.location, dest, redirects + 1));
      }
      if (res.statusCode !== 200) {
        res.resume();
        return reject(new Error(`HTTP ${res.statusCode} for ${url}`));
      }
      const file = fs.createWriteStream(dest);
      res.pipe(file);
      file.on('finish', () => file.close(() => resolve()));
      file.on('error', (e) => {
        try { fs.unlinkSync(dest); } catch (_) {}
        reject(e);
      });
    });
    req.on('error', reject);
  });
}

// ensureBinary returns the path to the platform binary, downloading + extracting
// it from the matching GitHub Release on first use. Idempotent.
async function ensureBinary() {
  const target = getTarget();
  const out = binaryPath();
  if (fs.existsSync(out) && fs.statSync(out).size > 0) return out;

  const version = pkg.version;
  const tag = `v${version}`;
  const asset = `alcampo_${version}_${target.platform}_${target.arch}.${target.ext}`;
  const url = `https://github.com/${REPO}/releases/download/${tag}/${asset}`;

  fs.mkdirSync(BIN_DIR, { recursive: true });
  const archive = path.join(BIN_DIR, asset);
  process.stderr.write(`alcampo: downloading ${asset}…\n`);
  await download(url, archive);

  // System `tar` handles .tar.gz everywhere and .zip on Windows 10+ (bsdtar).
  try {
    execFileSync('tar', ['-xf', archive, '-C', BIN_DIR], { stdio: 'ignore' });
  } catch (e) {
    throw new Error(`failed to extract ${asset} (is 'tar' on PATH?): ${e.message}`);
  } finally {
    try { fs.unlinkSync(archive); } catch (_) {}
  }

  if (!fs.existsSync(out)) throw new Error(`binary ${target.binname} missing after extracting ${asset}`);
  if (process.platform !== 'win32') fs.chmodSync(out, 0o755);
  return out;
}

module.exports = { ensureBinary, binaryPath, getTarget, REPO };
