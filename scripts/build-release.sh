#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
VERSION="${VERSION:-4.0.2}"
DIST="$ROOT/dist"
BUILD_ROOT="$(mktemp -d)"
WINDOWS_STAGE="$BUILD_ROOT/TT-Trigger-Windows-Local-${VERSION}"

WINDOWS_ARCHIVE="$DIST/TT-Trigger-Windows-Local-${VERSION}.zip"
CHROME_ARCHIVE="$DIST/TT-Trigger-Chrome-${VERSION}.zip"
CLOUD_ARCHIVE="$DIST/TT-Trigger-Cloudflare-${VERSION}.zip"
PYTHON_CLIENT="$DIST/invoke-trigger.py"

trap 'python3 - "$BUILD_ROOT" <<'"'"'PY'"'"'
import pathlib, shutil, sys
path = pathlib.Path(sys.argv[1])
if path.exists(): shutil.rmtree(path)
PY' EXIT

command -v go >/dev/null 2>&1 || { echo "Go 1.22+ is required to build the release." >&2; exit 1; }
command -v python3 >/dev/null 2>&1 || { echo "Python 3 is required to package the release." >&2; exit 1; }
command -v npm >/dev/null 2>&1 || { echo "npm is required to validate the release." >&2; exit 1; }

# dist is a release-output directory. Rebuilding intentionally removes every
# historical layout so only the four documented downloads remain.
python3 - "$DIST" <<'PY'
import pathlib, shutil, sys
path = pathlib.Path(sys.argv[1])
if path.exists(): shutil.rmtree(path)
path.mkdir(parents=True)
PY

cd "$ROOT"
go test ./...
python3 -m unittest discover -s tests -p '*_test.py'
npm run check:extension
npm run test:extension
(cd cloudflare && npm ci && npm test && npm run typecheck)

mkdir -p "$WINDOWS_STAGE"
CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build \
  -trimpath \
  -ldflags "-s -w -X main.version=$VERSION" \
  -o "$WINDOWS_STAGE/tt-trigger-server.exe" \
  ./cmd/tt-trigger-server

if [[ -n "${WINDOWS_SIGN_PFX:-}" ]]; then
  command -v osslsigncode >/dev/null 2>&1 || { echo "osslsigncode is required when WINDOWS_SIGN_PFX is set." >&2; exit 1; }
  signed_exe="$WINDOWS_STAGE/tt-trigger-server.signed.exe"
  osslsigncode sign -pkcs12 "$WINDOWS_SIGN_PFX" -pass "${WINDOWS_SIGN_PASSWORD:-}" \
    -n "TT-Trigger" -in "$WINDOWS_STAGE/tt-trigger-server.exe" -out "$signed_exe"
  mv "$signed_exe" "$WINDOWS_STAGE/tt-trigger-server.exe"
fi

cp windows/start.bat windows/stop.bat windows/configure.bat windows/status.bat \
  windows/manage-keys.bat windows/tt-trigger.ps1 windows/invoke-trigger.ps1 \
  windows/config.example.json "$WINDOWS_STAGE/"
cp windows/README-local.md "$WINDOWS_STAGE/README.md"
cp windows/invoke-trigger.py "$PYTHON_CLIENT"
chmod 0755 "$PYTHON_CLIENT"

python3 - "$ROOT" "$WINDOWS_STAGE" "$WINDOWS_ARCHIVE" "$CHROME_ARCHIVE" "$CLOUD_ARCHIVE" <<'PY'
import pathlib
import sys
import zipfile

root = pathlib.Path(sys.argv[1])
windows_stage = pathlib.Path(sys.argv[2])
windows_archive = pathlib.Path(sys.argv[3])
chrome_archive = pathlib.Path(sys.argv[4])
cloud_archive = pathlib.Path(sys.argv[5])

def write_tree(destination, source, relative_to):
    with zipfile.ZipFile(destination, "w", compression=zipfile.ZIP_DEFLATED, compresslevel=9) as output:
        for path in sorted(source.rglob("*")):
            if path.is_file():
                output.write(path, path.relative_to(relative_to))

write_tree(windows_archive, windows_stage, windows_stage.parent)
write_tree(chrome_archive, root / "extension", root / "extension")

cloud_files = [
    "package.json", "package-lock.json", "tsconfig.json", "wrangler.toml",
    "vitest.config.ts", "README.md", "deploy.ps1"
]
with zipfile.ZipFile(cloud_archive, "w", compression=zipfile.ZIP_DEFLATED, compresslevel=9) as output:
    cloud_root = root / "cloudflare"
    for name in cloud_files:
        output.write(cloud_root / name, pathlib.Path("cloudflare") / name)
    for directory in ["src", "scripts", "test"]:
        for path in sorted((cloud_root / directory).rglob("*")):
            if path.is_file():
                output.write(path, pathlib.Path("cloudflare") / path.relative_to(cloud_root))
PY

(
  cd "$DIST"
  for file in \
    "$(basename "$PYTHON_CLIENT")" \
    "$(basename "$CHROME_ARCHIVE")" \
    "$(basename "$WINDOWS_ARCHIVE")" \
    "$(basename "$CLOUD_ARCHIVE")"; do
    sha256sum "$file" > "$file.sha256"
  done
)

if [[ -n "${COSIGN_KEY:-}" ]]; then
  command -v cosign >/dev/null 2>&1 || { echo "cosign is required when COSIGN_KEY is set." >&2; exit 1; }
  for file in "$PYTHON_CLIENT" "$CHROME_ARCHIVE" "$WINDOWS_ARCHIVE" "$CLOUD_ARCHIVE"; do
    cosign sign-blob --yes --key "$COSIGN_KEY" \
      --output-signature "$file.sig" --output-certificate "$file.pem" "$file"
  done
fi

echo "Release created with four downloads:"
echo "  $PYTHON_CLIENT"
echo "  $CHROME_ARCHIVE"
echo "  $WINDOWS_ARCHIVE"
echo "  $CLOUD_ARCHIVE"
