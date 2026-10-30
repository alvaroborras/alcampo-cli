# @alvaroborras/alcampo

Unofficial, agent-friendly CLI for `compraonline.alcampo.es`. This npm package is a thin
wrapper that downloads the prebuilt Go binary for your platform from
[GitHub Releases](https://github.com/alvaroborras/alcampo-cli/releases) on install.

```bash
# one-off
npx @alvaroborras/alcampo search queso

# or install globally → `alcampo` on your PATH
npm install -g @alvaroborras/alcampo
alcampo help
```

Supported platforms: macOS, Linux, and Windows on x64 / arm64. If your platform isn't
covered, grab a binary from the releases page or build from source.

Full documentation, the authenticated cart/checkout flow, and the bundled Claude skill
live in the main repo: <https://github.com/alvaroborras/alcampo-cli>.

> Unofficial. Alcampo has no public API. Bring your own credentials and use a sane
> request rate.
