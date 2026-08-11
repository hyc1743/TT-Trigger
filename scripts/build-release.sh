#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
VERSION="${VERSION:-4.0.0}"
STAGE="$ROOT/dist/TT-Trigger-${VERSION}-windows-x64"
ARCHIVE="$ROOT/dist/TT-Trigger-${VERSION}-windows-x64.zip"
CLOUD_ARCHIVE="$ROOT/dist/TT-Trigger-${VERSION}-cloudflare.zip"
CHROME_ARCHIVE="$ROOT/dist/TT-Trigger-Chrome-${VERSION}.zip"

command -v go >/dev/null 2>&1 || { echo "Go 1.22+ is required to build the release." >&2; exit 1; }
command -v python3 >/dev/null 2>&1 || { echo "Python 3 is required to package the release." >&2; exit 1; }
command -v npm >/dev/null 2>&1 || { echo "npm is required to validate the release." >&2; exit 1; }

python3 - "$STAGE" "$ARCHIVE" "$ARCHIVE.sha256" "$CLOUD_ARCHIVE" "$CLOUD_ARCHIVE.sha256" "$CHROME_ARCHIVE" "$CHROME_ARCHIVE.sha256" <<'PY'
import pathlib
import shutil
import sys

for raw_path in sys.argv[1:]:
    path = pathlib.Path(raw_path)
    if path.is_dir():
        shutil.rmtree(path)
    elif path.exists():
        path.unlink()
PY
mkdir -p "$STAGE/extension" "$STAGE/cloudflare"

cd "$ROOT"
go test ./...
python3 -m unittest discover -s tests -p '*_test.py'
npm run check:extension
npm run test:extension
(cd cloudflare && npm ci && npm test && npm run typecheck)
CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build \
  -trimpath \
  -ldflags "-s -w -X main.version=$VERSION" \
  -o "$STAGE/tt-trigger-server.exe" \
  ./cmd/tt-trigger-server

cp windows/start.bat windows/stop.bat windows/configure.bat windows/status.bat \
  windows/manage-keys.bat windows/tt-trigger.ps1 windows/invoke-trigger.ps1 \
  windows/invoke-trigger.py \
  windows/install-cloud-client.bat \
  windows/config.example.json "$STAGE/"
cp README.md "$STAGE/README.md"
cp -R extension/. "$STAGE/extension/"
cp cloudflare/package.json cloudflare/package-lock.json cloudflare/tsconfig.json \
  cloudflare/wrangler.toml cloudflare/vitest.config.ts cloudflare/README.md \
  cloudflare/deploy.ps1 "$STAGE/cloudflare/"
cp -R cloudflare/src cloudflare/scripts cloudflare/test "$STAGE/cloudflare/"

python3 - "$STAGE" "$ARCHIVE" "$VERSION" <<'PY'
import pathlib
import sys
import zipfile

stage = pathlib.Path(sys.argv[1])
archive = pathlib.Path(sys.argv[2])
version = sys.argv[3]

def write_tree(destination, root, relative_to):
    with zipfile.ZipFile(destination, "w", compression=zipfile.ZIP_DEFLATED, compresslevel=9) as output:
        for path in sorted(root.rglob("*")):
            if path.is_file():
                output.write(path, path.relative_to(relative_to))

write_tree(stage / f"TT-Trigger-Chrome-{version}.zip", stage / "extension", stage / "extension")
write_tree(archive, stage, stage.parent)
PY

cp "$STAGE/TT-Trigger-Chrome-${VERSION}.zip" "$CHROME_ARCHIVE"

python3 - "$ROOT/cloudflare" "$CLOUD_ARCHIVE" <<'PY'
import pathlib
import sys
import zipfile

root = pathlib.Path(sys.argv[1])
archive = pathlib.Path(sys.argv[2])
included = ["package.json", "package-lock.json", "tsconfig.json", "wrangler.toml", "vitest.config.ts", "README.md", "deploy.ps1"]
with zipfile.ZipFile(archive, "w", compression=zipfile.ZIP_DEFLATED, compresslevel=9) as output:
    for name in included:
        output.write(root / name, pathlib.Path("cloudflare") / name)
    for directory in ["src", "scripts", "test"]:
        for path in sorted((root / directory).rglob("*")):
            if path.is_file(): output.write(path, pathlib.Path("cloudflare") / path.relative_to(root))
PY

(
  cd "$ROOT/dist"
  sha256sum "$(basename "$ARCHIVE")" > "$(basename "$ARCHIVE").sha256"
  sha256sum "$(basename "$CLOUD_ARCHIVE")" > "$(basename "$CLOUD_ARCHIVE").sha256"
  sha256sum "$(basename "$CHROME_ARCHIVE")" > "$(basename "$CHROME_ARCHIVE").sha256"
)

echo "Release created: $ARCHIVE"
echo "Cloudflare package created: $CLOUD_ARCHIVE"
echo "Chrome extension package created: $CHROME_ARCHIVE"
