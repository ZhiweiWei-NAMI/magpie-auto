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

安装并配置 [dsh-auto](https://github.com/ZhiweiWei-NAMI/dsh-auto#启用)。dsh 的独立 profile 用现有 Magpie 网关连接，主代理及 `subagent`、`subagent_fork` 都指定 `dsh-workbuddy/deepseek-v4.1-flash`。关闭插件并重新启用原生 `compaction-basic` 即恢复普通 dsh；无需改变模型服务连接。

## 上下文与缓存

原始记录保留完整正文，修复了历史回答只保留前 1200 字节的问题。正常轮次保持系统提示、工具定义和历史顺序，新内容追加。调查子代理的完整结果经 dsh 原生会话记录追加给主代理。

达到上下文压力阈值后，先用较小的 Jev 状态判断是否需要筛选。保留决定维持已有前缀；筛选决定才分批判断历史。较大的单条来源会完整分片判断，任一片段有用就保留整条来源。原始任务要求、最近的工具配对和原始日志保留。替换通过 dsh 原生压缩事务执行。

网关把 dsh 原生会话标识传给现有 WorkBuddy 服务，以维持执行会话的账号绑定。Jev 决策与执行会话分别维护。输入变短不等于费用降低：实验分别记录实际缓存命中、未命中、输出和延迟。缺失字段标为不可观测。

## 实验

[固定题单、运行配置及逐题记录](experiments/jev-cache-20261012/) 使用 SWE-bench Verified 与官方 Docker 评测，三组使用同一批题目、同一个执行模型和同一模型服务链路：A 开启 Auto/Jev；B 单主代理、关闭 Auto；C 原生可选子代理、关闭 Auto。环境下载、模型求解和官方评测分别记录。

实际结果与局限见 [实验说明](experiments/jev-cache-20261012/README.md)。开发试跑的失败和中断单独保留，未当作正式比较结果。没有可靠的执行服务计费依据时不计算美元费用。Jev 官方目前标为限时免费，接口未提供价格或计费字段。

接口与职责细节见 [Auto 与 dsh](docs/subsystems/auto-dsh.md)。原版功能与配置参考 [Magpie 官方文档](https://usemagpie.ai/docs/start) 和 [本仓库 reference](docs/reference.md)。上游提案另存为草稿，发送前由用户审阅。

[MIT](LICENSE)。
