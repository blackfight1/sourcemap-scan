#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="/root/sourcemap-scan"
BIN_PATH="$ROOT_DIR/bin/sourcemap-scan"
SITE_ONE_DIR="/root/testsite-batch-1"
SITE_TWO_DIR="/root/testsite-batch-2"
SITE_ONE_PORT="18080"
SITE_TWO_PORT="18081"
TARGETS_FILE="$ROOT_DIR/targets.txt"
FINDINGS_PATH="$ROOT_DIR/findings-batch.jsonl"

mkdir -p "$ROOT_DIR/bin"
cd "$ROOT_DIR"
go build -o "$BIN_PATH" ./cmd/sourcemap-scan

mkdir -p "$SITE_ONE_DIR" "$SITE_TWO_DIR"

cat > "$SITE_ONE_DIR/index.html" <<'EOF'
<!doctype html>
<html>
<head><meta charset="utf-8"><title>batch-one</title></head>
<body><script src="/app.js"></script></body>
</html>
EOF

cat > "$SITE_ONE_DIR/app.js" <<'EOF'
console.log("batch target one");
//# sourceMappingURL=app.js.map
EOF

cat > "$SITE_ONE_DIR/app.js.map" <<'EOF'
{"version":3,"file":"app.js","sources":["src/one.ts"],"sourcesContent":["console.log(\"one\");\n"],"names":[],"mappings":"AAAA"}
EOF

cat > "$SITE_TWO_DIR/index.html" <<'EOF'
<!doctype html>
<html>
<head><meta charset="utf-8"><title>batch-two</title></head>
<body><script src="/bundle.js"></script></body>
</html>
EOF

cat > "$SITE_TWO_DIR/bundle.js" <<'EOF'
console.log("batch target two");
EOF

cat > "$SITE_TWO_DIR/bundle.js.map" <<'EOF'
{"version":3,"file":"bundle.js","sources":["src/two.ts"],"sourcesContent":["console.log(\"two\");\n"],"names":[],"mappings":"AAAA"}
EOF

pkill -f "python3 -m http.server $SITE_ONE_PORT" >/dev/null 2>&1 || true
pkill -f "python3 -m http.server $SITE_TWO_PORT" >/dev/null 2>&1 || true
nohup python3 -m http.server "$SITE_ONE_PORT" --directory "$SITE_ONE_DIR" >/tmp/testsite-batch-1.log 2>&1 &
nohup python3 -m http.server "$SITE_TWO_PORT" --directory "$SITE_TWO_DIR" >/tmp/testsite-batch-2.log 2>&1 &
sleep 2

curl -fsS "http://127.0.0.1:$SITE_ONE_PORT/" >/dev/null
curl -fsS "http://127.0.0.1:$SITE_TWO_PORT/" >/dev/null

cat > "$TARGETS_FILE" <<EOF
# duplicate and comment lines should be ignored
http://127.0.0.1:$SITE_ONE_PORT
http://127.0.0.1:$SITE_TWO_PORT
http://127.0.0.1:$SITE_ONE_PORT
EOF

"$BIN_PATH" \
  -l "$TARGETS_FILE" \
  -target-workers 2 \
  -katana-bin /usr/local/bin/katana \
  -o "$FINDINGS_PATH"

printf '\n=== FINDINGS ===\n'
cat "$FINDINGS_PATH"
printf '\n=== COUNT ===\n'
wc -l "$FINDINGS_PATH"
