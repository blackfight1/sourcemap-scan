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

## Notes

- The scanner only targets `.js` and `.mjs` assets for now.
- `data:` sourcemap URLs are intentionally ignored because they are not exposed remote `.map` files.
- The tool uses a tail-range fetch first and falls back to reading the returned body as needed by the server response.
- `-target-workers` controls parallel targets, while `-scan-workers` controls per-target JavaScript scanning.
