# 复现指南 — SWE-bench Verified 79.2%

## 前置条件

| 依赖 | 版本 |
|------|------|
| Go | 1.24+ |
| Git | 2.27+ |
| Docker | 20.10+（评分用） |
| Python | 3.12+（swebench 用） |
| DeepSeek API Key | [api.deepseek.com](https://api.deepseek.com) |

## 源码版本

| 仓库 | Commit |
|------|--------|
| wescode.git | `2eeff2c8`（SWE-bench Patch 采集修复版） |
| wesgine.git | `093ad57e` |
| wesapp.git | 对应 `go.mod replace` 版本 |
| weisyn.git | 对应 `go.mod replace` 版本 |

## Step 1：构建 wescode

```bash
cd wescode.git/backend
go build -o bin/wescode ./cmd/wescode/
```

## Step 2：配置 DeepSeek

创建 `~/.config/wescode/config.yaml`（Linux）或 `~/Library/Application Support/wescode/config.yaml`（macOS）：

```yaml
providers:
  - name: deepseek
    type: openai_compat
    base_url: https://api.deepseek.com
    model: deepseek-chat
    api_key: <your-deepseek-api-key>
    is_default: true
```

## Step 3：生成 Predictions

```bash
# 跑 SWE-bench Verified 全部 500 题
bin/wescode bench \
  --dataset tests/bench/swebench/batches \
  --runs 1 \
  --verbose \
  --output results/swebench/report.json \
  --predictions results/swebench/predictions.jsonl

# 约 5-7 小时，费用约 ¥680 / $96
```

## Step 4：评分

```bash
# 安装 swebench
pip install swebench

# 方法 A：用预构建镜像评分（需要能访问 Docker Hub）
swebench eval verified \
  -p results/swebench/predictions.jsonl \
  --run-id wescode \
  -j 4

# 方法 B：用 task-repo 本地构建镜像（不依赖 Docker Hub）
git clone --depth 1 https://github.com/SWE-bench/swe-bench-tasks.git
swebench eval verified \
  -p results/swebench/predictions.jsonl \
  --run-id wescode \
  --task-repo ./swe-bench-tasks \
  -j 2
```

## Step 5：验证结果

```bash
# 统计 resolved 数
python3 -c "
import json, glob
reports = glob.glob('logs/run_evaluation/wescode/*/report.json')
r = sum(1 for rp in reports for k,v in json.load(open(rp)).items() if isinstance(v,dict) and v.get('resolved'))
print(f'Resolved: {r}/{len(reports)} = {r*100.0/500:.1f}%')
"
```

## 预期结果

| 指标 | 值 |
|------|------|
| Resolved | 396 / 500 = 79.2% |
| 费用 | ~$96（DeepSeek Chat） |
| 时间 | predictions ~6h + 评分 ~4h |

## 注意事项

- DeepSeek Chat 的输出有随机性（temperature > 0），每次运行结果会有 ±2-3% 的波动
- 中国大陆服务器需配置 pip 国内源（清华/阿里）和 HuggingFace 镜像（`HF_ENDPOINT=https://hf-mirror.com`）
- Docker 镜像构建需要充足磁盘空间（~400GB for 500 images）
