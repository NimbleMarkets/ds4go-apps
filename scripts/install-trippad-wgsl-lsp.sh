#!/bin/sh
# Install our patched release verified by internal/trippad/wgslls tests.
set -eu
if [ "$(uname -s)" != Darwin ]; then
    echo 'This installer targets macOS; build NimbleMarkets/wgsl-analyzer tag v0.9.11-trippad.1 for your platform and use --wgsl-lsp PATH.' >&2
    exit 1
fi
case "$(uname -m)" in
    arm64)
        asset=wgsl-analyzer-darwin-arm64
        checksum=0c36ab292059c3cfc7f67013c1384ad9a60d743ebf1e61a5526ca6a70dc80b62
        ;;
    x86_64)
        asset=wgsl-analyzer-darwin-x64
        checksum=2fd5f7bc130b2b3f19636dcb5a2d021484a00a00ab38f534ada548052e22a935
        ;;
    *)
        echo 'Unsupported macOS architecture; build the pinned fork and use --wgsl-lsp PATH.' >&2
        exit 1
        ;;
esac
repo=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
mkdir -p "$repo/bin"
download=$(mktemp "$repo/bin/.wgsl-analyzer.XXXXXX")
trap 'rm -f "$download"' EXIT HUP INT TERM
curl --fail --location --silent --show-error \
    "https://github.com/NimbleMarkets/wgsl-analyzer/releases/download/v0.9.11-trippad.1/$asset" \
    -o "$download"
# SHA-256 of the fork artifact exercised by our compatibility tests.
echo "$checksum  $download" | shasum -a 256 -c -
chmod 755 "$download"
mv "$download" "$repo/bin/wgsl-analyzer"
"$repo/bin/wgsl-analyzer" --version
