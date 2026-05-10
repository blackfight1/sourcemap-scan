#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="/root/sourcemap-scan"
BIN_PATH="$ROOT_DIR/bin/sourcemap-scan"
SITE_DIR="/root/testsite-failure"
SITE_PORT="18080"
TARGETS_FILE="$ROOT_DIR/targets-failure.txt"
FINDINGS_PATH="$ROOT_DIR/findings-failure.jsonl"

mkdir -p "$ROOT_DIR/bin"
cd "$ROOT_DIR"
go build -o "$BIN_PATH" ./cmd/sourcemap-scan

mkdir -p "$SITE_DIR"

cat > "$SITE_DIR/index.html" <<'EOF'
<!doctype html>
<html>
<head><meta charset="utf-8"><title>failure-test</title></head>
<body><script src="/app.js"></script></body>
</html>
EOF

cat > "$SITE_DIR/app.js" <<'EOF'
console.log("target survives other target failure");
//# sourceMappingURL=app.js.map
EOF

cat > "$SITE_DIR/app.js.map" <<'EOF'
{"version":3,"file":"app.js","sources":["src/app.ts"],"sourcesContent":["console.log(\"ok\");\n"],"names":[],"mappings":"AAAA"}
EOF

pkill -f "python3 -m http.server $SITE_PORT" >/dev/null 2>&1 || true
nohup python3 -m http.server "$SITE_PORT" --directory "$SITE_DIR" >/tmp/testsite-failure.log 2>&1 &
sleep 2

curl -fsS "http://127.0.0.1:$SITE_PORT/" >/dev/null

cat > "$TARGETS_FILE" <<EOF
http://127.0.0.1:65535
http://127.0.0.1:$SITE_PORT
EOF

"$BIN_PATH" \
  -l "$TARGETS_FILE" \
  -target-workers 2 \
  -katana-bin /usr/local/bin/katana \
  -o "$FINDINGS_PATH"

printf '\n=== FINDINGS ===\n'
cat "$FINDINGS_PATH"
