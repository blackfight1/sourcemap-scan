# sourcemap-scan

一个用 Go 编写的 CLI 工具，用于批量发现网站暴露的 JavaScript sourcemap，并对还原后的源码做 secret 检测。

核心流程：

`katana` 抓 JS -> 识别 `.map` -> `shuji` 还原源码 -> `trufflehog` 扫描 -> 飞书通知 `Verified=true`

## 功能

- 支持单目标和批量目标
- 支持 3 种 sourcemap 识别方式
  - JS 尾部 `sourceMappingURL`
  - `SourceMap` / `X-SourceMap` 响应头
  - `.js.map` 邻接猜测
- 支持批量自动分批
  - 默认 `10000` 目标一批
- 支持 `shuji` 还原源码
- 支持 `trufflehog filesystem --json` 扫描
- 只对 `Verified=true` 的 secret 发飞书通知
- 全部扫描结束后会再发一条任务完成通知

## 依赖

运行环境建议为 Linux VPS。

需要提前安装：

- `katana`
- `shuji`
- `trufflehog`

Go 构建版本：

- `Go 1.22+`

## 编译

```bash
go build -o sourcemap-scan ./cmd/sourcemap-scan
```

## 推荐命令

### 1. 单目标完整扫描

```bash
sourcemap-scan pipeline -u https://target.tld -base-dir /tmp/smap-run
```

### 2. 批量扫描

```bash
sourcemap-scan pipeline -l targets.txt -base-dir /root/smap-run
```

### 3. 适合 4H4G VPS 的推荐命令

```bash
sourcemap-scan pipeline \
  -l /root/bugbounty-program/bugcrowd/bc-alive.txt \
  -base-dir /root/bugbounty-program/run-bugcrowd \
  -target-workers 4 \
  -process-workers 2 \
  -scan-workers 12 \
  -katana-concurrency 15 \
  -katana-parallelism 4 \
  -katana-rate-limit 40
```

如果机器压力太大，再降回：

```bash
-target-workers 2 -process-workers 1 -scan-workers 8
```

### 4. 只扫描，不做还原和 secret 检测

```bash
sourcemap-scan -l targets.txt -o findings.jsonl
```

### 5. 对已有 findings 二次处理

```bash
sourcemap-scan process -i findings.jsonl -base-dir /root/smap-process
```

## 常用参数

### `pipeline`

- `-u`
  单目标 URL
- `-l`
  目标文件，一行一个 URL
- `-base-dir`
  输出目录
- `-batch-size`
  每批目标数量，默认 `10000`
- `-target-workers`
  目标级并发
- `-scan-workers`
  单目标内 JS 扫描并发
- `-process-workers`
  sourcemap 处理并发
- `-katana-bin`
  `katana` 路径
- `-shuji-bin`
  `shuji` 路径
- `-trufflehog-bin`
  `trufflehog` 路径
- `-feishu-webhook`
  飞书 webhook
- `-verbose`
  输出更详细日志

### `process`

- `-i`
  findings JSONL 文件
- `-base-dir`
  输出目录
- `-process-workers`
  处理并发
- `-keep-artifacts`
  保留 map、summary、trufflehog 原始输出
- `-keep-restored`
  保留还原后的源码目录

## 输出目录

默认输出结构：

```text
base-dir/
  findings/
    findings-*.jsonl
  results/
    summaries.jsonl
    pipeline-summary.json
  state/
    processed-maps.txt
```

批量自动分批时：

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
  results/
    pipeline-summary.json
```

说明：

- `findings-*.jsonl`
  扫描阶段找到的有效 sourcemap
- `summaries.jsonl`
  每个 sourcemap 处理后的结果
- `pipeline-summary.json`
  整个任务的总汇总
- `processed-maps.txt`
  已处理过的 map 状态

## 关于反编译文件保存位置

默认情况下：

- sourcemap 会先下载到临时目录
- `shuji` 还原源码后立即交给 `trufflehog` 扫描
- 扫描完成后临时目录会删除

也就是说：

**默认不会长期保留还原后的源码。**

如果要保留，请使用：

```bash
-keep-artifacts -keep-restored
```

保留后目录在：

```text
base-dir/work/<host>/<hash>/
```

## 通知逻辑

飞书通知分两类：

1. `Verified=true` 的 secret 实时通知
2. 全部扫描结束后的任务完成通知

如果不想通知，可以把 webhook 置空：

```bash
-feishu-webhook ''
```

## 建议

- 长时间批量任务建议用 `tmux`
- 大规模跑时优先用 `pipeline`
- 先看 `results/pipeline-summary.json`
- 如果想保留还原源码，再开 `-keep-artifacts -keep-restored`

## 例子

查看帮助：

```bash
sourcemap-scan -h
sourcemap-scan pipeline -h
sourcemap-scan process -h
```
