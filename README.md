# sourcemap-scan

A Go CLI for finding exposed JavaScript sourcemaps and running a full post-processing pipeline with `shuji` and `trufflehog`.

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
./sourcemap-scan process -i findings.jsonl -base-dir /opt/sourcemap/data
./sourcemap-scan pipeline -u https://target.tld -base-dir /opt/sourcemap/run
./sourcemap-scan pipeline -l targets.txt -base-dir /opt/sourcemap/batch -target-workers 10 -process-workers 4
./sourcemap-scan pipeline -l targets.txt -base-dir /opt/sourcemap/run-all
```

Main commands:

- `sourcemap-scan`
  Scan only. Outputs findings JSONL.
- `sourcemap-scan process`
  Process existing findings JSONL with `shuji` and `trufflehog`.
- `sourcemap-scan pipeline`
  One command from target list to verified secret notification.

## Process Subcommand

The `process` subcommand keeps `shuji` and `trufflehog` as external tools, but moves the post-processing orchestration into Go.

Example:

```bash
./sourcemap-scan process \
  -i findings.jsonl \
  -base-dir /opt/sourcemap/data \
  -shuji-bin /usr/bin/shuji \
  -trufflehog-bin /usr/local/bin/trufflehog \
  -process-workers 2
```

The process stage:

1. Reads `findings.jsonl`
2. Skips already processed `map_url` values
3. Downloads each `.map`
4. Restores source via `shuji`
5. Scans restored files via `trufflehog filesystem --json`
6. Keeps all TruffleHog hits in the summary JSONL
7. Sends Feishu notifications only for `Verified=true` results
8. Appends one compact line per processed map into `results/summaries.jsonl`
9. Keeps per-map artifacts only when `-keep-artifacts` is enabled

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
  -target-workers 8 \
  -process-workers 3
```

The pipeline stage:

1. Starts the scan stage
2. Writes findings JSONL to `-o`, or auto-generates one under `base-dir/findings/`
3. Streams each finding directly into the Go `process` stage as soon as it is discovered
4. Processes sourcemaps concurrently with `-process-workers`
5. Reuses `base-dir/findings`, `base-dir/results`, and `base-dir/state` as the default compact output layout
6. Writes `base-dir/work` only when `-keep-artifacts` is enabled
7. Automatically splits large target files into internal batches of `10000` targets by default

Batch concurrency model:

- `-target-workers`
  Number of targets scanned in parallel
- `-scan-workers`
  Number of JS assets checked in parallel inside each target
- `-process-workers`
  Number of sourcemaps restored and scanned in parallel

Internal batch model:

- `-batch-size`
  Number of targets per internal batch inside one `pipeline` run
- Default is `10000`
- When the target count exceeds one batch, outputs are automatically written under `base-dir/batches/batch-xxxxx/`
- A single final `results/pipeline-summary.json` is written under the root `base-dir`

Failure handling:

- If one target fails during the scan stage, the pipeline skips that target and continues with the rest
- If one sourcemap fails during processing, the rest of the queue continues
- The final exit code is non-zero when any target or finding fails

Default notifications:

- Feishu webhook is built in by default
- Only `Verified=true` TruffleHog results are sent
- Notification payload is an interactive card with target, detector, secret redacted value, asset URL, map URL, bundle file, and source file
- The `pipeline` command also sends one final completion notification after the whole run finishes

Default output layout:

```text
base-dir/
  findings/
    findings-*.jsonl
  results/
    summaries.jsonl
    pipeline-summary.json
  state/
    processed-maps.txt

When internal batching is used:

```text
base-dir/
  batches/
    batch-00000/
      findings/
      results/
        summaries.jsonl
      state/
        processed-maps.txt
    batch-00001/
      ...
```
```

Artifacts kept only with `-keep-artifacts`:

```text
base-dir/
  work/
    <host>/<hash>/
      map/
      restored/
      summary.json
      trufflehog.raw.jsonl
```

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

Each processed sourcemap appends one line to `results/summaries.jsonl`:

```json
{
  "target": "https://target.tld",
  "target_host": "target.tld",
  "asset_url": "https://target.tld/static/app.js",
  "map_url": "https://target.tld/static/app.js.map",
  "file": "app.js",
  "discovered_by": "js_comment",
  "sources_count": 42,
  "names_count": 1337,
  "has_sources_content": true,
  "processed_at": "2026-05-11T08:00:00Z",
  "restore_success": true,
  "trufflehog_success": true,
  "hits_total": 3,
  "verified_hits": 1,
  "notified": 1,
  "hits": [
    {
      "detector": "PayPal",
      "verified": true,
      "file_path": "src/config/payments.ts",
      "redacted": "A***Z",
      "notified": true
    }
  ]
}
```

## Notes

- The scanner only targets `.js` and `.mjs` assets for now.
- `data:` sourcemap URLs are intentionally ignored because they are not exposed remote `.map` files.
- The tool uses a tail-range fetch first and falls back to reading the returned body as needed by the server response.
- `-target-workers` controls parallel targets, while `-scan-workers` controls per-target JavaScript scanning.
- On Linux VPS, use `tmux` or `screen` if you want the pipeline to keep running after disconnecting.
