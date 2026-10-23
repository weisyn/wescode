# SWE-bench Live Verified 500 — 未通过题能力分析

> **用途**：这次跑分不只是为了交榜，是看 wescode 真正卡在哪。本文只分析**已经评完、没有 resolved** 的题。
> **冻结分数**：官方 `results.json` **355 / 500 = 71.0%**（success / submitted）。
> **分析快照**：2026-10-22 14:16，fill 完成前：344 success / 98 failure。fill 把 12 道 incomplete 评完（+11 success、+1 failure → 冻结 355 / 99）。分类表仍以本快照的 98 道 failure 为准。
> **机器账本**：同目录 [`failure-catalog.json`](failure-catalog.json)（98 道 failure 的 FTP/PTP/patch 字段）。
> **对照**：Princeton SWE-bench Verified 那次是 [`../../swebench-verified/20261004-deepseek-chat/FAILURE-ANALYSIS.md`](../../swebench-verified/20261004-deepseek-chat/FAILURE-ANALYSIS.md)——形状完全不同，不要把两套结论混用。
> **Lite 300**：另一套 split，必须新 rollout。邻域门已落地，但拦住 ≠ resolved。**禁止**把下面 84 / 44 / 8 抄进 Lite 预测分。

---

## 范围

```
500 行（499 唯一 instance；conan-18153 官方重复一行）
 ├── 344 success（68.8%）——本文不展开
 ├──  98 failure  —— 有 patch，官方 evaluation.py resolved=false
 ├──   8 empty    —— preds 里 model_patch 长度为 0
 └──  50 incomplete —— 镜像没评上；不是失败，本文不记成能力缺陷
```

「没过」= **98 + 8 = 106 题**。队列里那 ~40 张还在拉的图，评完再补进本文件，不要提前当失败。

和 Princeton 内部 79.2% 那次的差别（同一套 wescode，不同数据集）：

| | Princeton Verified 500 | Live Verified 500（本档） |
|---|---|---|
| 补丁格式错误 | 46（尾换行被 TrimSpace 剥掉） | **0**（98 道全是合法 `diff --git`，都有尾 `\n`） |
| 目标测试全没过 | 57 / 57 unresolved | **9 / 98**（miss 6 + miss+reg 3） |
| 目标测试过了、旧测试挂了 | **0** | **84 / 98** |
| 空 patch | 1 | 8，且 8/8 都是 300s 墙掐掉 |

一句话：**Live 这 100 道不是「模型不会修」，是「修对了目标测试，但弄坏了旁边的测试」+「5 分钟到点还没写出 diff」。**

---

## 98 道 failure 的分类

判据只看官方 `report.json` 的 `FAIL_TO_PASS` / `PASS_TO_PASS`，不看轨迹里 agent 自报过没过。

| 类 | 题数 | 含义 | 迭代抓手 |
|---|---:|---|---|
| **regression** | **84** | FTP 全过、PTP 有失败 | 结束前跑旧测试；L2.5 / QualityGate |
| miss | 6 | FTP 全失败、PTP 干净 | 定位/理解错了，模型或检索 |
| miss+reg | 3 | FTP 全失败 **且** 引入回归 | 改错地方还带伤 |
| partial | 1 | FTP 过一部分、无回归 | 差一点 |
| partial+reg | 2 | FTP 过一部分 **且** 回归 | 含 YAML 泄漏的 `cfn-lint-3981`（92 个文件） |
| eval/harness 假阴 | 2 | 测试日志与 `resolved=false` 对不上 | 见 §五，先别当成模型笨 |

84/98 = **85.7%** 是回归。53/84 只改了 1 个文件——不是乱改仓库，是「改对了那一行，没发现邻居断了」。

回归的 PTP 失败条数：1 条 20 题、2–8 条 60 题、>8 条 4 题。多数是小回归，不是炸测试套件。

---

## 一、回归 84 题 —— 主战场

### 1.1 全灭或接近全灭的仓库

下面的失败率分母是 **success + failure**（不含 empty / incomplete）。

| 仓库 | fail | succ | 失败率 | 回归形态 |
|---|---:|---:|---:|---|
| `bridgecrewio`（checkov） | 12 | 0 | **100%** | 12/12 regression。SARIF schema / terraform graph builder |
| `privacyidea` | 6 | 0 | **100%** | 12/12 里的 6 道全是 push token API 回归（每题 PTP fail=8） |
| `ipython` | 5 | 0 | **100%** | 4 regression + 1 miss+reg。**5/5 都挂 `test_system_interrupt`** |
| `kedro-org` | 4 | 0 | **100%** | CLI starters `tools` flag，PTP 8–50 |
| `geopandas` | 4 | 0 | **100%** | **4/4 都挂同一组 `test_read_file_url`（访问 raw.githubusercontent / geonode）** |
| `pypsa` | 3 | 0 | **100%** | 回归，PTP fail=1 |
| `huggingface` | 3 | 0 | **100%** | `datasets` 两道 PTP 炸 143 / 17；`smolagents` 3 |
| `aws-cloudformation`（cfn-lint） | 32 | 20 | **61.5%** | 31 regression + 1 partial+reg。几乎每题 PTP 都是 `test_success_run` + QuickStart 模板 4 或 7 条 |

cfn-lint 和 checkov 加起来 **44 题**，占 98 道 failure 的 45%。两个都是「加一条规则 → 旧规则/旧模板被误杀」。这是 wescode 现在最大的产品形状，不是 matplotlib 渲染、也不是 sympy 数学。

### 1.2 可能是评测环境、不是能力

先把这两簇从「模型不会」里摘出来，否则迭代会改错东西：

1. **geopandas 4/4**：失败测试全是读 HTTPS URL。Live 镜像若无出网，gold 也会挂——应用 gold 验证才能定性。在定性前 **不要** 当作 wescode 不会修 geopandas。
2. **ipython 5/5 `test_system_interrupt`**：子进程 SIGINT。Docker 里没进程组 / 没有 TTY 时这类测试会假红。同样要 gold 才能定性。

其余 checkov / cfn-lint / privacyidea / kedro 的 PTP 名字是业务测试，更像真回归。

### 1.3 和引擎已有纪律的关系

wescode 有 L2.5（测试绿了但行为变了）和 Skill「看到 `1 failed` 就不要 end_turn」。本档 84 道回归说明：

- Agent **在 rollout 里多半没跑 PTP**（Live 协议不把 `PASS_TO_PASS` 列表喂给模型，它只能自己猜该跑什么）。
- 它跑了自己想到的那条新测试（所以 FTP 过了），没跑仓库里相邻的旧测试。
- 结束条件是「我认为修完了」，不是「官方会跑的那批旧测试也绿」。

迭代方向是 **让模型在 end_turn 前跑更宽的测试**（至少同文件 / 同目录），不是再给它看 `FAIL_TO_PASS` 名单——那会破坏提交协议。

---

## 二、空 patch 8 题 —— 同一堵 300 秒墙

8 道 `model_patch` 长度都是 0。轨迹里 **8/8 `status=interrupted`，`elapsed_ms` 全在 300184–303556**。这就是本档跑分时 harness 的默认超时（当时 300s）。

**产品已改（Lite 开跑前）**：`Case.DefaultTimeout` 回落到 `DefaultCaseTimeoutSec=1800`。salvage `git diff` 原先就有——空 patch 是因为超时前 0 次 edit，salvage 救不回没写过的 diff。墙加长之后 empty 2（Lite 交集）才有机会变成非空补丁。

Live YAML 仍不写 `timeout_sec`。LHTB 是 90 分钟；SWE-bench Live 官方 runner 远不止 5 分钟。本档 8 题的轨迹仍是 300s 那一轮，不要用它们证明 1800s 已经生效。

| instance | turns | elapsed_ms | 写文件了吗 | 停在干什么 |
|---|---:|---:|---|---|
| `matplotlib-28584` | 15 | 302535 | 0 edit/write | 还在 `git show` 找测试；`exec` 报 `fork/exec /usr/bin/sh: no such file or directory` |
| `matplotlib-29721` | 3 | 303556 | 0 | `context_cancelled`，几乎没开始 |
| `shapely-2094` | 10 | 300824 | 0 | `find /` 找 `geos_c.h` 超时 |
| `instructlab-2004` | 17 | 300184 | 0 | 在 `git grep` 历史 |
| `xarray-10001` | 26 | 300361 | write 1（未留下 diff） | 用宿主机 `/opt/swebench-eval/bin/python` 跑测试 |
| `llama_deploy-457` | 28 | 302936 | 0 | 探索到点 |
| `llama_deploy-458` | 22 | 302938 | 0 | 同上 |
| `llama-stack-1105` | 21 | 302524 | 0 | 同上 |

这 8 题 **不能** 写成「模型交白卷」。墙先倒了。`xarray-10001` 还摸到了宿主机 eval 解释器——和那 6 个 `exec` 读 YAML 是同一类边界，提交 README 要并案。

**迭代（已落地，分数要新 rollout）**：`DefaultTimeout` → 1800s；`find /` 命令位置拒绝；POSIX `sh` 缺失回退 `bash`（matplotlib-28584）。exec 工作区外绝对读见 §10.3 / README L0f。

---

## 三、真正「修错了」的 9 题（miss / miss+reg）

FTP 一条都没过。这才是定位/理解失败。

| instance | 类 | FTP | PTP fail | 文件 | 备注 |
|---|---|---|---:|---:|---|
| `pylint-9771` | miss | 0/1 | 0 | 1 | 389–394B 小补丁，改了但不是 `invalid_name` 要的 |
| `pylint-10097` | miss | 0/1 | 0 | 1 | 同上，`potential_index_error` |
| `fonttools-3726` | miss | 0/2 | 0 | 2 | feaLib contextual inline format 4 |
| `fonttools-3749` | miss | 0/2 | 0 | 1 | feaLib duplicate language stmt |
| `fast-f1-699` | miss | 0/1 | 0 | 1 | 105 条消息仍没打中 `test_session_results_drivers` |
| `jupyter-ai-1022` | miss | 0/3 | 0 | 9 | 改了 9 个文件仍没关上 closing/pending 消息 |
| `ipython-14693` | miss+reg | 0/1 | 1 | 1 | 目标没中，还挂了 `test_system_interrupt` |
| `keras-20880` | miss+reg | 0/59 | 7 | 5 | 大面积没打中 |
| `llama-factory-7505` | miss+reg | 0/1 | 45 | 1 | 一处改动炸 45 条旧测试 |

pylint 两道是 **最小错误补丁**（不到 400B）——方向近、断言没对上。fonttools 两道是领域（字体 feature file）。这 9 题才需要「更强模型 / 更好 CKG」，不要和那 84 道回归混成同一张改进单。

---

## 四、部分修好

| instance | FTP | PTP fail | 文件 | 备注 |
|---|---|---:|---:|---|
| `llama_deploy-397` | 1/2 | 0 | 1 | 唯一一道「差一点、还没回归」 |
| `python-telegram-bot-4673` | 3/4 | 64 | 3 | 修了 3 条目标，炸 64 条旧测试 |
| `cfn-lint-3981` | 3/4 | 6 | **92** | 协议残留：`exec` 读到宿主机 YAML。提交必须点名 |

`cfn-lint-3981` 是 98 道里改文件最多的（92）。不要拿它代表「wescode 喜欢大改」——它看到了不该看的 gold。其余回归 53/84 只动 1 个文件。

---

## 五、两条不能算能力的假阴

### `keras-20122`

`report.json` 里 FTP/PTP 全是 0。`post_patch_log.txt`：pytest 收到 11892 items，跑到 43% 被 `Killed`（OOM）。宿主机 14Gi。这是 **评测进程被杀**，不是 resolved=false 的常规含义。

### `python-poetry-10324`

数据集 `FAIL_TO_PASS` 有 **2** 条；report 只记到 **1 success / 0 failure**。PTP 2537 全过。pytest 总结 `2609 passed, 9 skipped, 27 deselected`。缺的那条是参数化名字：

`tests/utils/test_threading.py::test_threading_atomic_cached_property_different_instances[value_functools_cache-264]`

日志对不上官方名单 → harness 不把它算进 success 也不算 failure → `resolved` 仍是 false。这是 **测试名匹配**，重评或对一下 log 里的实际 node id 就能定性。

---

## 六、其它扎眼的单题

- `Aider-AI__aider-3806`：regression，FTP 1/0，但 patch **3.3MB / 4 文件**。极可能把不该进 diff 的生成物打进去了。值得单独打开 patch 看。
- `briefcase-2125`：FTP **98** 条全过，PTP 只挂 1 条。几乎是满分回归。
- `huggingface__datasets-7353`：FTP 10 条全过，PTP 挂 143。改 1 个文件。

---

## 七、逐题表（98 failure）

列：`FTP ok/fail` = FAIL_TO_PASS 通过/失败条数；`PTP fail` = PASS_TO_PASS 失败条数。完整 JSON 见 [`failure-catalog.json`](failure-catalog.json)。

| instance | class | FTP ok/fail | PTP fail | files | bytes | msgs |
|---|---|---:|---:|---:|---:|---:|
| `Aider-AI__aider-3806` | regression | 1/0 | 4 | 4 | 3380505 | 63 |
| `aiogram__aiogram-1585` | regression | 2/0 | 1 | 1 | 1112 | 42 |
| `aws-cloudformation__cfn-lint-3759` | regression | 5/0 | 4 | 3 | 2070 | 56 |
| `aws-cloudformation__cfn-lint-3764` | regression | 2/0 | 4 | 1 | 1919 | 47 |
| `aws-cloudformation__cfn-lint-3767` | regression | 1/0 | 4 | 1 | 448 | 57 |
| `aws-cloudformation__cfn-lint-3768` | regression | 8/0 | 4 | 1 | 3952 | 54 |
| `aws-cloudformation__cfn-lint-3770` | regression | 1/0 | 4 | 1 | 1073 | 70 |
| `aws-cloudformation__cfn-lint-3789` | regression | 3/0 | 4 | 22 | 24213 | 52 |
| `aws-cloudformation__cfn-lint-3817` | regression | 1/0 | 4 | 1 | 1546 | 49 |
| `aws-cloudformation__cfn-lint-3821` | regression | 1/0 | 4 | 1 | 1137 | 42 |
| `aws-cloudformation__cfn-lint-3854` | regression | 1/0 | 4 | 1 | 2125 | 80 |
| `aws-cloudformation__cfn-lint-3855` | regression | 1/0 | 4 | 1 | 1068 | 33 |
| `aws-cloudformation__cfn-lint-3856` | regression | 1/0 | 4 | 3 | 3581 | 67 |
| `aws-cloudformation__cfn-lint-3861` | regression | 1/0 | 4 | 1 | 3188 | 62 |
| `aws-cloudformation__cfn-lint-3866` | regression | 1/0 | 4 | 1 | 984 | 49 |
| `aws-cloudformation__cfn-lint-3875` | regression | 2/0 | 4 | 1 | 1283 | 62 |
| `aws-cloudformation__cfn-lint-3890` | regression | 1/0 | 4 | 1 | 1594 | 35 |
| `aws-cloudformation__cfn-lint-3900` | regression | 1/0 | 4 | 1 | 1235 | 24 |
| `aws-cloudformation__cfn-lint-3908` | regression | 1/0 | 4 | 2 | 1272 | 53 |
| `aws-cloudformation__cfn-lint-3920` | regression | 1/0 | 4 | 1 | 717 | 70 |
| `aws-cloudformation__cfn-lint-3931` | regression | 1/0 | 4 | 1 | 722 | 29 |
| `aws-cloudformation__cfn-lint-3940` | regression | 3/0 | 4 | 2 | 3992 | 41 |
| `aws-cloudformation__cfn-lint-3947` | regression | 2/0 | 4 | 6 | 5264 | 65 |
| `aws-cloudformation__cfn-lint-3972` | regression | 1/0 | 4 | 1 | 2231 | 63 |
| `aws-cloudformation__cfn-lint-3982` | regression | 1/0 | 7 | 2 | 822 | 59 |
| `aws-cloudformation__cfn-lint-4002` | regression | 2/0 | 7 | 1 | 2296 | 47 |
| `aws-cloudformation__cfn-lint-4009` | regression | 1/0 | 7 | 4 | 3145 | 51 |
| `aws-cloudformation__cfn-lint-4016` | regression | 1/0 | 7 | 3 | 1776 | 50 |
| `aws-cloudformation__cfn-lint-4023` | regression | 1/0 | 7 | 1 | 552 | 55 |
| `aws-cloudformation__cfn-lint-4032` | regression | 1/0 | 7 | 1 | 1614 | 55 |
| `aws-cloudformation__cfn-lint-4045` | regression | 1/0 | 7 | 1 | 7615 | 67 |
| `aws-cloudformation__cfn-lint-4051` | regression | 2/0 | 7 | 2 | 3302 | 57 |
| `aws-cloudformation__cfn-lint-4116` | regression | 1/0 | 7 | 2 | 2236 | 64 |
| `beeware__briefcase-2088` | regression | 7/0 | 1 | 2 | 2063 | 32 |
| `beeware__briefcase-2125` | regression | 98/0 | 1 | 9 | 16039 | 47 |
| `bridgecrewio__checkov-6612` | regression | 2/0 | 3 | 2 | 1613 | 42 |
| `bridgecrewio__checkov-6630` | regression | 1/0 | 1 | 1 | 613 | 79 |
| `bridgecrewio__checkov-6657` | regression | 1/0 | 4 | 3 | 7331 | 73 |
| `bridgecrewio__checkov-6687` | regression | 1/0 | 4 | 1 | 622 | 88 |
| `bridgecrewio__checkov-6893` | regression | 1/0 | 4 | 1 | 2388 | 70 |
| `bridgecrewio__checkov-6895` | regression | 1/0 | 5 | 1 | 1581 | 39 |
| `bridgecrewio__checkov-6922` | regression | 1/0 | 5 | 1 | 577 | 30 |
| `bridgecrewio__checkov-6933` | regression | 1/0 | 4 | 2 | 2198 | 42 |
| `bridgecrewio__checkov-7002` | regression | 1/0 | 4 | 2 | 3189 | 50 |
| `bridgecrewio__checkov-7022` | regression | 1/0 | 4 | 1 | 831 | 20 |
| `bridgecrewio__checkov-7119` | regression | 1/0 | 5 | 1 | 2167 | 33 |
| `bridgecrewio__checkov-7124` | regression | 1/0 | 4 | 1 | 2591 | 35 |
| `conan-io__conan-16776` | regression | 1/0 | 5 | 1 | 1583 | 56 |
| `conan-io__conan-16789` | regression | 1/0 | 5 | 1 | 2071 | 65 |
| `cyclotruc__gitingest-134` | regression | 2/0 | 1 | 1 | 316 | 38 |
| `deepset-ai__haystack-8941` | regression | 1/0 | 1 | 1 | 3900 | 32 |
| `geopandas__geopandas-3419` | regression | 1/0 | 3 | 1 | 1880 | 60 |
| `geopandas__geopandas-3424` | regression | 1/0 | 3 | 1 | 423 | 53 |
| `geopandas__geopandas-3471` | regression | 7/0 | 3 | 1 | 491 | 38 |
| `geopandas__geopandas-3502` | regression | 1/0 | 3 | 1 | 1372 | 35 |
| `huggingface__datasets-7353` | regression | 10/0 | 143 | 1 | 3347 | 64 |
| `huggingface__datasets-7522` | regression | 11/0 | 17 | 1 | 2067 | 52 |
| `huggingface__smolagents-285` | regression | 1/0 | 3 | 1 | 4448 | 60 |
| `instructlab__instructlab-1762` | regression | 1/0 | 1 | 2 | 1369 | 81 |
| `instructlab__instructlab-3045` | regression | 1/0 | 1 | 16 | 9282 | 28 |
| `instructlab__instructlab-3060` | regression | 1/0 | 1 | 1 | 931 | 36 |
| `instructlab__instructlab-3184` | regression | 1/0 | 1 | 1 | 3414 | 39 |
| `ipython__ipython-14503` | regression | 1/0 | 1 | 1 | 2335 | 56 |
| `ipython__ipython-14555` | regression | 1/0 | 1 | 1 | 731 | 39 |
| `ipython__ipython-14822` | regression | 1/0 | 1 | 1 | 1112 | 41 |
| `ipython__ipython-14838` | regression | 36/0 | 1 | 1 | 7340 | 63 |
| `kedro-org__kedro-4075` | regression | 1/0 | 8 | 1 | 1309 | 68 |
| `kedro-org__kedro-4092` | regression | 1/0 | 8 | 2 | 4350 | 68 |
| `kedro-org__kedro-4540` | regression | 1/0 | 25 | 2 | 2677 | 58 |
| `kedro-org__kedro-4580` | regression | 8/0 | 50 | 1 | 4852 | 56 |
| `keras-team__keras-20765` | regression | 1/0 | 7 | 1 | 1646 | 41 |
| `keras-team__keras-20824` | regression | 1/0 | 7 | 1 | 1098 | 60 |
| `olofk__fusesoc-720` | regression | 1/0 | 1 | 2 | 2347 | 71 |
| `privacyidea__privacyidea-4206` | regression | 1/0 | 8 | 8 | 10460 | 61 |
| `privacyidea__privacyidea-4223` | regression | 1/0 | 8 | 1 | 7040 | 73 |
| `privacyidea__privacyidea-4226` | regression | 1/0 | 8 | 2 | 5344 | 59 |
| `privacyidea__privacyidea-4233` | regression | 7/0 | 8 | 2 | 1253 | 67 |
| `privacyidea__privacyidea-4247` | regression | 3/0 | 8 | 1 | 24917 | 78 |
| `privacyidea__privacyidea-4251` | regression | 1/0 | 8 | 2 | 5384 | 57 |
| `pypa__pipenv-6380` | regression | 1/0 | 1 | 2 | 1701 | 46 |
| `pypsa__pypsa-1012` | regression | 1/0 | 1 | 4 | 5812 | 60 |
| `pypsa__pypsa-1091` | regression | 1/0 | 1 | 8 | 12314 | 71 |
| `pypsa__pypsa-1172` | regression | 1/0 | 1 | 1 | 856 | 94 |
| `urllib3__urllib3-3441` | regression | 1/0 | 1 | 2 | 868 | 71 |
| `aws-cloudformation__cfn-lint-3981` | partial+reg | 3/1 | 6 | 92 | 64746 | 108 |
| `python-telegram-bot__python-telegram-bot-4673` | partial+reg | 3/1 | 64 | 3 | 21501 | 50 |
| `run-llama__llama_deploy-397` | partial | 1/1 | 0 | 1 | 6590 | 45 |
| `hiyouga__llama-factory-7505` | miss+reg | 0/1 | 45 | 1 | 1655 | 74 |
| `ipython__ipython-14693` | miss+reg | 0/1 | 1 | 1 | 2400 | 45 |
| `keras-team__keras-20880` | miss+reg | 0/59 | 7 | 5 | 5937 | 80 |
| `fonttools__fonttools-3726` | miss | 0/2 | 0 | 2 | 3120 | 54 |
| `fonttools__fonttools-3749` | miss | 0/2 | 0 | 1 | 996 | 46 |
| `jupyterlab__jupyter-ai-1022` | miss | 0/3 | 0 | 9 | 17674 | 104 |
| `pylint-dev__pylint-10097` | miss | 0/1 | 0 | 1 | 389 | 56 |
| `pylint-dev__pylint-9771` | miss | 0/1 | 0 | 1 | 394 | 49 |
| `theoehrly__fast-f1-699` | miss | 0/1 | 0 | 1 | 3021 | 105 |
| `keras-team__keras-20122` | eval-OOM | 0/0 | 0 | 2 | 1095 | 31 |
| `python-poetry__poetry-10324` | harness-name | 1/0 | 0 | 2 | 4355 | 64 |

空 patch 8 题见 §二，不重复列入。

---

## 八、wescode 迭代优先级（按本档证据）

只列能被这 106 题证伪的项。换模型、pass@N 不在本表——本档是 pass@1。

| 优先级 | 改什么 | 对应题 | 验证 |
|---|---|---|---|
| **P0** | Live 默认超时 300s → ≥1800s；超时要 salvage `git diff` 而不是交空 patch | empty 8 | **已改** `DefaultCaseTimeoutSec=1800`。本档 8 题是旧墙。Lite 抽 3 道轨迹 `elapsed_ms` 须能超过 300s |
| **P0** | QualityGate 预留最后一段墙（agent 预算与门预算拆开；Evaluate 用 run ctx） | 交集 21 道 cfn-lint + 3 道 checkov | **已改**。1800s → 预留 5min。否则 agent 吃光墙再 `end_turn`，邻域立刻被取消 |
| **P0** | end_turn 前跑影响邻域（L1 同位 / L2 调用方 / L3 夹具），不要只跑自己新写的那条 | regression 84，尤其 cfn-lint 32 + checkov 12 | **门已落地**（INV-VF-NEIGHBOR-01，见 §十）。本档 84 道**未**用新门重跑。Lite 验证：抽 10 道回归形态题看 PTP 是否下降，且轨迹里 QualityGate 真跑了邻域 |
| **P1** | Python L1 `tests/test_<underscored_path>.py` | privacyidea 5（PTP=8，push token） | **已改** `flattenedPythonTestCandidates`。拦住 ≠ 过 |
| **P1** | `exec` 不得摸到宿主机数据集 YAML（绝对路径且不在 AllowPaths；系统前缀除外） | 协议 + 6 题泄漏 | **已改**。交榜必须做，**不要指望加分**——5 题当前是 success |
| **P1** | 禁止无界 `find /`；POSIX `sh` 缺失回退 `bash` | shapely-2094、matplotlib-28584 | **已改**。对 Lite 174 几乎零贡献 |
| **P2** | keras 评测 OOM：workers=1 已有，单题 pytest 要限收集范围或加内存 | keras-20122 | 该题 report 不再 0/0 |
| **P2** | 参数化 FTP 名字对不上时不要静默 resolved=false | poetry-10324 | 2 条 FTP 都进 success 或明确记 missing |
| **P3** | gold 抽 geopandas / ipython 定性是不是环境假红 | 4+5 题 | gold 也挂 → 从能力账本划走 |
| **P3** | 打开 `aider-3806` 的 3.3MB diff，堵住把生成物打进 patch | 1 题 | patch 回到正常量级 |
| **不做** | 把 `FAIL_TO_PASS` / `test_patch` 喂给模型 | — | 破坏 Live 协议 |
| **不做** | 为这 84 道回归去换 Claude | — | 目标测试已经过了 |

粗算（不能当预测分数对外说）：若 P0 超时救回 empty 里的一半（4），P0 回归在 cfn-lint+checkov 44 题上救回三分之一（15），假阴 2 题翻过来，上限大约 **344+4+15+2 ≈ 365/500**。没做实验之前这只是方向，不是承诺。**不得**把 365/500 或「邻域已落地」写成 Lite 300 的预期分。Lite 是另一套 split，见 §十。

---

## 九、和提交的关系

本文不挡交榜。交榜用完整 500 分母、点名 6 题 YAML/`exec`、conan 重复行、incomplete 能补多少算多少。

本文的超时 / exec 边界仍是 **下一轮 rollout** 才生效，不会改这一份 `preds.json`。邻域门已进产品二进制（§十），但本档 98+8 是门落地**之前**的 preds。

---

## 十、邻域门落地后：拦住 ≠ 过（Lite 评分纪律）

> **用途**：把 2026-10-22 产品改动和本档 106 道账对上，供 [Lite 300](https://swe-bench-live.github.io/#leaderboard-lite) 下一轮 rollout 分类失败，而不是把 Verified 的数字抄过去当 Lite 预测。
> **纪律**：Lite 必须新 rollout，不要从本档 preds 过滤。

分两层。**门的假绿和旁路，可以彻底解决，而且已经用负向用例锁住了。本档 106 道没过，不能靠邻域门彻底变成 resolved。**

上一轮 Live Verified 跑的时候 BenchMode 还在跳 L2（VF-08）。所以 84 道回归是「门根本没问旧测试」，不是「问了但修不好」。INV-VF-NEIGHBOR-01 补的是前一件事。

### 10.1 已经彻底合上的（产品假绿）

这些是构造上的 fail-open：不修就会再出现「看起来过了」。修完之后，同样输入不会再静默放行。单元测试已证明这一层闭合。Lite 开跑前确认二进制含这些测试，不要用 VF-08 时代的 `wescode bench`。

| 洞 | 不补会怎样 | 锁 |
|---|---|---|
| BenchMode 跳 L2 | 回归静默交卷 | `TestEvaluate_BenchModeStillRunsL2`；门户/ADR 不再教 VF-08 |
| pytest 当产物预言机 | 配了源码再跑测试 → 配置一起放行 | `isTestCommand` / `hasRunTests` 已删；`TestWriteVerificationNudge_TestCommandDoesNotExemptArtifacts` |
| 相对路径 + 父目录 go.mod | 空邻域 Pass | `TestEvaluate_RelativeModifiedFiles_ParentMarkerCannotHijackWorkDir` |
| Truncated / 跨仓 | 截断后当全绿 | fail-closed，不得 Pass |
| 源码扩展名两边不一致 | `.vue` 等双跳过 | langs 与 Write-Verify 对齐 |
| Java/PHP/C#/Ruby 测试后缀 | L2 窗口丢掉同包 `FooTest` | SQL 预筛 + `TestTestFilesCalling_LangTestSuffixWithoutTestDir` |

### 10.2 本档失败类：门能拦住 ≠ 题能过

邻域在 end_turn 自己跑同位 / 调用方 / 夹具测试。拦下来之后模型最多再修 3 轮（`MaxTotalRevisions=3`）。拦住和官方 `resolved=true` 中间还有模型和剩余时间。

下表的题数只属于 **Live Verified 500 本档**。Lite 300 与 Verified 交集 174、Lite 独有 126。全量开跑后必须按同一套 FTP/PTP 分类**另建** `FAILURE-ANALYSIS.md`，禁止把 84 / 44 / 8 写进 Lite 报告当预测。

| 类 | Verified 题数 | 邻域能不能「彻底解决」 | Lite 评分时怎么记账 |
|---|---:|---|---|
| **regression**（FTP 全过、PTP 挂） | 84 | **能拦住一大部分，不能保证过** | 正是门的设计目标。53/84 只改 1 个文件，L1 同位经常够用。轨迹须有 QualityGate 邻域；有邻域仍 unresolved → 记「拦住未修好」，不要记「门没跑」 |
| 其中 cfn-lint + checkov | 44 | **选择器对得上，本档未重跑** | L3 夹具（`test_success_run` + QuickStart）。`TestNeighborhood_CfnLintShapeKeepsPTP` 只证明会跑到 PTP。Lite 若撞上同形仓库，抽 10 道看 PTP 是否下降 |
| `datasets-7353` 这类 | 1+ | **不一定拦得到** | 改 1 文件、PTP 挂很多。路径不像 rule/check 则 L3 不升；图未就绪记 `partial/skipped`（不是假绿）。Lite 同形题不要指望 L1 救 100+ 条 PTP |
| miss / miss+reg | 9 | **不能** | 目标测试就没过。邻域不负责「理解对 issue」。换模型 / CKG，不改门 |
| empty patch | 8 | **不能** | 本档 8/8 都是默认 **300 秒墙**。邻域在 end_turn 才跑，跑不到。Lite 若 `elapsed_ms` 贴着超时且 `model_patch` 空 → 记超时，不记能力 |
| eval 假阴 | 2 | **不能** | harness / OOM / 参数化测试名。Lite 同样先对 log 与 `FAIL_TO_PASS` 名单 |
| geopandas 出网、ipython SIGINT | 4+5 | **不要当能力账** | gold 没定性前邻域也会跟着红 |
| incomplete | 50 | **不能** | 镜像没评上。Lite 头部是 `incomplete: 0`，缺图就预拉或跳过后再补，不要交部分 split |

### 10.3 Lite 开跑前的硬墙（代码已改 vs 分数仍要新跑）

1. **超时墙 + 门的预留时间（已改）。** `DefaultCaseTimeoutSec=1800`；Evaluate 用 run ctx，agent 预算按 remaining/6 封顶 5min 拆开。本档 8 道 empty 仍是 300s 那一轮。不加预留，1800s 也会被探索吃掉，21 道 cfn-lint 进不了「拦住之后能不能过」。
2. **修订上限 3（不改）。** PTP 挂 4～7 条的规则题有机会；挂几十上百条的，门会红，题仍可能 unresolved。加到 10 是烧时间，不是闭合洞。
3. **本档 preds 是旧门 + 旧墙。** 负向单元测试证明「不会再假绿 / 门外读会被拒 / find / 会被拒」；不证明官方 `report.json` 会变 resolved。Lite 必须 **新 rollout**，不要拿 Verified 的 `preds.json` 当 Lite 提交，也不要把 104 个 patch 滤成 Lite。
4. **协议边界（已改，不要指望加分）。** `exec` 绝对路径且 `DomainOutside` 拒绝（`/usr/bin` 等系统前缀除外）；`find /` always-on；`sh` 缺失回退 `bash`。堵住 YAML 泄漏可能让那 5 道靠 gold 的 success 掉下来。Lite 提交 README 同样要能点名。

### 10.4 Lite 评分时禁止的说法

| 禁止 | 正确 |
|---|---|
| 「邻域已落地，Lite 应接近 71% / 应过 84 道回归」 | 71% 是 TianxiCode 的 Lite 分；84 是 Verified 旧门的回归数。Lite 只报本次 300 的 FTP/PTP 分类 |
| 把 Verified `success/(500−incomplete)` 拿去和 [Lite 榜](https://swe-bench-live.github.io/#leaderboard-lite) 比 | Lite 头部完整分母、`incomplete: 0`。分母 300 |
| 模型 exec 过 pytest → 当作 QualityGate 已自证 | 证据只认门自己跑的邻域（INV-VF-NEIGHBOR-01） |
| 空 patch 写成「模型交白卷」而 `elapsed_ms≈timeout` | 先记超时墙 |
| 把 `FAIL_TO_PASS` / `test_patch` 喂给模型来「更好评分」 | 破坏 Live 协议；CI 抽检 + trajectory 会抓 |
