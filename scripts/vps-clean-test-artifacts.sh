#!/usr/bin/env bash
set -euo pipefail

rm -rf /root/testsite /root/testsite-batch-1 /root/testsite-batch-2
rm -f /root/sourcemap-scan/findings.jsonl /root/sourcemap-scan/findings-batch.jsonl /root/sourcemap-scan/targets.txt
pkill -f "python3 -m http.server 18080" >/dev/null 2>&1 || true
pkill -f "python3 -m http.server 18081" >/dev/null 2>&1 || true
