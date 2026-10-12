# 独立 Jev Free 调用

两个小请求均成功，实际模型为 `jev-1.13-free`。请求 payload 见 cases.json，原始脱敏返回见 latest.json。直接使用 Bearer Console key 与 System One 的 state/questions，没有经过 chat/completions。

| 场景 | agent choice | choice probabilities | 历史 noul（h1/h2/h3） | h2 score | 输入/输出 token | 秒 |
| --- | --- | --- | --- | --- | --- | --- |
| API 核验 | api_engineer | api_engineer 1，research_writer 0 | 0.87 / 0.92 / 0.10 | 1.99 | 587 / 110 | 0.9471 |
| 研究摘要 | research_writer | api_engineer 0，research_writer 1 | 0.64 / 0.71 / 0.08 | 1.79 | 577 / 108 | 1.0495 |

表中均为接口实际字段。noul 与 score 保留原值，未补造保留布尔值。缓存、用量单价与计费字段未提供；官方页面当前将此标识列为限时免费。官方格式与免费状态依据：[OpenCode Console Jev](https://opencode.ai/v2/docs/console/models#jev)。

服务器已保留调用脚本及结果：`~/.local/share/jev-api-check/jev_smoke.py`。运行 `python3 ~/.local/share/jev-api-check/jev_smoke.py`。密钥从 `~/.config/jev-api-check/console.key` 读取，权限为 0600；不传入命令参数。需要更换密钥时，该脚本的 `--setup-key` 使用交互隐藏输入。
