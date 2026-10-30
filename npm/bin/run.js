#!/usr/bin/env node
'use strict';

// Thin launcher: ensure the platform binary is present (downloading it on first run,
// cached thereafter), then exec it with the caller's args, forwarding the exit code.

const { spawnSync } = require('child_process');
const { ensureBinary } = require('../lib/download.js');

(async () => {
  let bin;
  try {
    bin = await ensureBinary();
  } catch (e) {
    process.stderr.write(`alcampo: ${e.message}\n`);
    process.exit(1);
  }
  const res = spawnSync(bin, process.argv.slice(2), { stdio: 'inherit' });
  if (res.error) {
    process.stderr.write(`alcampo: ${res.error.message}\n`);
    process.exit(1);
  }
  process.exit(res.status === null ? 1 : res.status);
})();
