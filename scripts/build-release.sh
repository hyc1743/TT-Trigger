#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
VERSION="${VERSION:-2.1.1}"
STAGE="$ROOT/dist/TT-Trigger-${VERSION}-windows-x64"
ARCHIVE="$ROOT/dist/TT-Trigger-${VERSION}-windows-x64.zip"

command -v go >/dev/null 2>&1 || { echo "Go 1.22+ is required to build the release." >&2; exit 1; }
command -v python3 >/dev/null 2>&1 || { echo "Python 3 is required to package the release." >&2; exit 1; }

python3 - "$STAGE" "$ARCHIVE" "$ARCHIVE.sha256" <<'PY'
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
mkdir -p "$STAGE/extension"

cd "$ROOT"
go test ./...
CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build \
  -trimpath \
  -ldflags "-s -w -X main.version=$VERSION" \
  -o "$STAGE/tt-trigger-server.exe" \
  ./cmd/tt-trigger-server

cp windows/start.bat windows/stop.bat windows/config.example.json "$STAGE/"
cp README.md "$STAGE/README.md"
cp -R extension/. "$STAGE/extension/"

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

write_tree(stage / f"TT-Trigger-Chrome-{version}.zip", stage / "extension", stage)
write_tree(archive, stage, stage.parent)
PY

(
  cd "$ROOT/dist"
  sha256sum "$(basename "$ARCHIVE")" > "$(basename "$ARCHIVE").sha256"
)

echo "Release created: $ARCHIVE"
