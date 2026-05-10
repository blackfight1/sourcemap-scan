# sourcemap-scan

A Go CLI for finding exposed JavaScript source maps on one or many targets.

## Current Scope

This tool uses an installed `katana` binary to discover JavaScript assets, then validates exposed sourcemaps.

Implemented discovery methods:

- JavaScript tail comment parsing for `sourceMappingURL`
- `SourceMap` and `X-SourceMap` response headers
- Adjacent `.js.map` guessing when no explicit sourcemap hint exists

Validation checks:

- HTTP `200` response
- Reject obvious HTML responses
- JSON parsing
- Required sourcemap fields: `version`, `sources`, `mappings`

## Requirements

- Go 1.22+
- Linux VPS runtime target
- `katana` installed and available in `PATH`, or passed with `-katana-bin`

## Build

```bash
go build -o sourcemap-scan ./cmd/sourcemap-scan
```

## Usage

```bash
./sourcemap-scan -u https://target.tld
./sourcemap-scan -l targets.txt
./sourcemap-scan -l targets.txt -target-workers 5 -o findings.jsonl
./sourcemap-scan -u https://target.tld -katana-bin /usr/local/bin/katana
./sourcemap-scan -u https://target.tld -katana-extra-args "-H 'Cookie: session=...' -proxy http://127.0.0.1:8080"
./sourcemap-scan process -i findings.jsonl -base-dir /opt/sourcemap/data
./sourcemap-scan pipeline -l targets.txt -base-dir /opt/sourcemap/run
```

## Process Subcommand

The `process` subcommand keeps `shuji` and `trufflehog` as external tools, but moves the post-processing orchestration into Go.

Example:

```bash
./sourcemap-scan process \
  -i findings.jsonl \
  -base-dir /opt/sourcemap/data \
  -shuji-bin /usr/bin/shuji \
  -trufflehog-bin /usr/local/bin/trufflehog \
  -feishu-webhook https://open.feishu.cn/open-apis/bot/v2/hook/xxxx
```

The process stage:

1. Reads `findings.jsonl`
2. Skips already processed `map_url` values
3. Downloads each `.map`
4. Restores source via `shuji`
5. Scans restored files via `trufflehog filesystem --json`
6. Keeps all TruffleHog hits in the summary
7. Sends Feishu notifications only for `Verified=true` results
8. Writes `summary.json` and raw TruffleHog output under the base directory

## Pipeline Subcommand

The `pipeline` subcommand is the one-command Go workflow. It does not shell out to wrapper scripts for orchestration.

Example:

```bash
./sourcemap-scan pipeline \
  -l targets.txt \
  -base-dir /opt/sourcemap/run \
  -katana-bin /usr/local/bin/katana \
  -shuji-bin /usr/bin/shuji \
  -trufflehog-bin /usr/local/bin/trufflehog \
  -feishu-webhook https://open.feishu.cn/open-apis/bot/v2/hook/xxxx
```

The pipeline stage:

1. Runs the normal scan stage
2. Writes findings JSONL to `-o`, or auto-generates one under `base-dir/findings/`
3. Runs the Go `process` stage against that findings file
4. Reuses `base-dir/work`, `base-dir/state`, and `base-dir/logs` for restored files, summaries, and dedupe state

## Example Output

Each finding is emitted as one JSON line:

```json
{
  "target": "https://target.tld",
  "asset_url": "https://target.tld/static/app.js",
  "map_url": "https://target.tld/static/app.js.map",
  "requested_map_url": "https://target.tld/static/app.js.map",
  "discovered_by": "js_comment",
  "source_mapping_url_raw": "app.js.map",
  "status_code": 200,
  "content_type": "application/json",
  "sources_count": 42,
  "names_count": 1337,
  "has_sources_content": true,
  "file": "app.js"
}
```

## Notes

- The scanner only targets `.js` and `.mjs` assets for now.
- `data:` sourcemap URLs are intentionally ignored because they are not exposed remote `.map` files.
- The tool uses a tail-range fetch first and falls back to reading the returned body as needed by the server response.
- `-target-workers` controls parallel targets, while `-scan-workers` controls per-target JavaScript scanning.
