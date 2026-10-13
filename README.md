# magpie-auto

这个 fork 来自 [yetone/magpie](https://github.com/yetone/magpie)，保留 Magpie 的模型网关、连接配置和控制台。Auto 用于试验上下文保留与协作调度，目标是在完成质量相近时减少重复工作和实际消耗。它是可选功能。

Magpie 调用真正的 `jev-1.13-free`，返回结构化决策。dsh 负责工具执行、会话、原生子代理、消息传递和结果回收；客户端扩展位于独立仓库 [dsh-auto](https://github.com/ZhiweiWei-NAMI/dsh-auto)。原先放在 Magpie 内的文件执行器已移除。主代理和执行子代理都使用 `deepseek-v4.1-flash`。

## 服务器入口

| 项目 | 位置 |
| --- | --- |
| 部署 | `/mnt/data2/weizhiwei/magpie-auto` |
| 当前源码 | `/mnt/data2/weizhiwei/magpie-auto-repo`，`feature/auto-workspace` |
| 控制台 | `http://127.0.0.1:3436` |
| 模型网关 | `http://127.0.0.1:3435/v1` |
| 现有执行模型连接 | `dsh-workbuddy` → `http://127.0.0.1:8788/v1` |
| Auto 配置 | 部署目录的 `bin/data/auto/state.json` |
| Console 密钥文件 | `~/.config/jev-api-check/console.key`，权限 `0600` |

服务器原有有效修改已先保存到 Git，再实施本次改动。`source/` 是接手时的部署快照；上表的 Git 工作区是当前源码。其他项目的 WorkBuddy 连接、账号凭据和全局 dsh 默认配置不由这个实验修改。

构建服务器版本：

```bash
go build -tags nogui -o magpie-auto .
```

独立安装时，在二进制旁放置空的 `.portable` 文件，然后运行：

```bash
MAGPIE_ADDR=127.0.0.1:3435 ./magpie-auto web --addr 127.0.0.1:3436 --no-open
```

服务器现有运行实例可用部署目录中的 `restart-private.py` 重启；它保留该实例的私有环境，且只处理该部署的 PID。控制台保持在回环地址。

## 启用 Auto

在控制台 Auto 配置中启用决策服务，设置 `decider: jev-1.13-free` 与 Console 密钥文件绝对路径。密钥内容只由服务器读取。Jev 使用 [官方 System One 接口](https://opencode.ai/v2/docs/console/models#jev) 的 `state` 与 typed `questions`，不用 chat/completions，不切换 GLM 或付费 Jev。

策略输入框支持一句话设置偏好，例如“自动决策，优先正确性和缓存，偏好实现代理，只在独立子任务有明确收益时协作”。保存时 Jev 编译模式及初始权重；相同指令和路由重存时复用结果。dsh 新会话固定这份快照，已有会话不被全局编辑改写。角色模型和 effort 仍须来自配置中实际可用的候选；当前服务器统一使用 DeepSeek Flash High。

安装并配置 [dsh-auto](https://github.com/ZhiweiWei-NAMI/dsh-auto#启用)。dsh 的独立 profile 用现有 Magpie 网关连接，主代理及 `subagent`、`subagent_fork` 都指定 `dsh-workbuddy/deepseek-v4.1-flash`。关闭插件并重新启用原生 `compaction-basic` 即恢复普通 dsh；无需改变模型服务连接。

服务器已配置独立入口，进入你的任务目录后运行：

```bash
/mnt/data2/weizhiwei/magpie-auto/run-auto '你的编程任务'
```

此入口使用部署目录的 `dsh-auto-home/settings.yaml` 和 `dsh-auto-home/cordis.patch.yml`，全局 dsh 配置保持原样。最小独立 Jev 验证及实际结构化返回见 [两个小请求](experiments/jev-cache-20261012/jev-isolated/)。

## 上下文与缓存

原始记录保留完整正文，修复了历史回答只保留前 1200 字节的问题。正常轮次保持系统提示、工具定义和历史顺序，新内容追加。执行模型提出具体子任务，Jev 独立判断价值、依赖、配置匹配和已有会话的延续性，再由 dsh 原生工具落实。子代理使用 continuable 会话；默认后台执行，主代理可以同时工作。

达到上下文压力阈值后，Jev 对旧区域完整逻辑块的重要性和归属分别评分；超大块完整分片判断，任一片段有用则保留整块。只有预计缩减足够时才用 dsh 原生压缩事务替换，否则保持历史并提高下次触发阈值。原始任务要求、最近的工具配对和原始日志保留。压力用最近实际 API 输入加新增内容估算，避免原生累计估算过高导致每轮决策。

网关把 dsh 原生会话标识传给现有 WorkBuddy 服务。当前服务器的 8788 运行版本采用 `least_inflight`，实测仍逐次轮换账号，这个字段尚未产生会话绑定。Jev 决策与执行会话分别维护。输入变短不等于费用降低：实验分别记录实际缓存命中、未命中、输出和延迟。缺失字段标为不可观测。

## 实验

[固定题单、运行配置及逐题记录](experiments/jev-cache-20261012/) 使用 SWE-bench Verified 与官方 Docker 评测，三组使用同一批题目、同一个执行模型和同一模型服务链路：A 开启 Auto/Jev；B 单主代理、关闭 Auto；C 原生可选子代理、关闭 Auto。环境下载、模型求解和官方评测分别记录。

固定十二题的 36 次求解均已官方判分：A 解决 6/12，B、C 各解决 8/12；求解及导出累计分别为 71.96、65.82、72.47 分钟。A 启动三个原生调查子代理，一位完成，另两位在同一题失败；C 自行选择了全部单代理执行。71 次 Jev 调用均成功，API 耗时合计 56.09 秒。

三组全部解决的五题中，A 比 B 少 12.7% 未命中 token，但输出多 27.9%、耗时多 6.7%。这轮 v0.2.2 实验没有证明 Auto 降低整体消耗或提高解决率；压力决策全部选择保留，没有实际替换。逐题补丁、真实用量、首次/后续请求、局限和复现命令见 [实验说明](experiments/jev-cache-20261012/README.md)。

v0.3.0 [修订与诊断](experiments/jev-routing-revision/) 已验证 Jev 选择复用同一原生子代理、两次激活与结果回收，以及真实上下文替换。指标固定编号：M1 完成质量、M2 时间、M3 总消耗、M4 缓存、M5 决策开销、M6 稳定性、M7 协作。两题三组官方结果均为 1/2；三轮短任务每组均 3/3，均未调用任务级 Jev。没有证明完成率提升。实际缓存命中与账号再次接收同一会话的历史明显对应，8788 的账号轮换仍限制复用；该共享服务未改动。新记录不替代旧十二题结果。

开发试跑的失败和中断单独保留；曾访问后续发行版源码的开放网络试跑不进入正式比较。取消请求缺失的 usage 未填零。没有可靠的执行服务计费依据时不计算美元费用。Jev [官方目前标为限时免费](https://opencode.ai/v2/docs/console/models#jev)，接口未提供价格或计费字段。

接口与职责细节见 [Auto 与 dsh](docs/subsystems/auto-dsh.md)。原版功能与配置参考 [Magpie 官方文档](https://usemagpie.ai/docs/start) 和 [本仓库 reference](docs/reference.md)。上游提案另存为草稿，发送前由用户审阅。

[MIT](LICENSE)。
