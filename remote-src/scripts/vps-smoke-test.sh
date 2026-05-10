#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="/root/sourcemap-scan"
BIN_PATH="$ROOT_DIR/bin/sourcemap-scan"
SITE_DIR="/root/testsite"
SITE_PORT="18080"
FINDINGS_PATH="$ROOT_DIR/findings.jsonl"

mkdir -p "$ROOT_DIR/bin"
cd "$ROOT_DIR"
go build -o "$BIN_PATH" ./cmd/sourcemap-scan

mkdir -p "$SITE_DIR"

cat > "$SITE_DIR/index.html" <<'EOF'
<!doctype html>
<html>
<head><meta charset="utf-8"><title>test</title></head>
<body><script src="/app.js"></script></body>
</html>
EOF

cat > "$SITE_DIR/app.js" <<'EOF'
console.log("hello from test site");
//# sourceMappingURL=app.js.map
EOF

cat > "$SITE_DIR/app.js.map" <<'EOF'
{"version":3,"file":"app.js","sources":["src/app.ts"],"sourcesContent":["console.log(\"hello from source\");\n"],"names":[],"mappings":"AAAA"}
EOF

pkill -f "python3 -m http.server $SITE_PORT" >/dev/null 2>&1 || true
nohup python3 -m http.server "$SITE_PORT" --directory "$SITE_DIR" >/tmp/testsite-http.log 2>&1 &
sleep 2

curl -fsS "http://127.0.0.1:$SITE_PORT/" >/tmp/testsite-index.html

"$BIN_PATH" \
  -u "http://127.0.0.1:$SITE_PORT" \
  -katana-bin /usr/local/bin/katana \
  -o "$FINDINGS_PATH"

printf '\n=== FINDINGS ===\n'
cat "$FINDINGS_PATH"
