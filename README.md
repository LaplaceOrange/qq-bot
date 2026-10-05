# QQ Bot

基于 QQ 官方机器人接口的 Go 服务。代码从 `new-api-bot` 精简而来，只保留 `/help`、`/whoami`、`/rss` 和 `/llm` 四个一级命令。

机器人通过 WebSocket Gateway 接收消息，不需要配置公网回调地址。服务使用 bbolt 保存订阅、消息队列和会话。LLM 配置、任务内容和历史记录使用 `BOT_DATA_KEY` 加密。

本项目不包含账户绑定、签到、额度管理、红包、群管理和厂商监控。运行时不需要 New API 管理员接口、SMTP 或 Python。

## 快速启动

### 准备配置

需要 QQ 机器人 AppID、AppSecret 和管理员 OpenID。LLM 对话还需要兼容 Chat Completions 的接口、密钥和模型名称。

1. 创建本地配置文件。

   ```powershell
   Copy-Item .env.example .env
   ```

2. 生成数据加密密钥。

   ```powershell
   $bytes = [byte[]]::new(32)
   [System.Security.Cryptography.RandomNumberGenerator]::Fill($bytes)
   [Convert]::ToBase64String($bytes)
   ```

3. 将生成的值填入 `.env` 的 `BOT_DATA_KEY`。

4. 填写 `QQ_APP_ID`、`QQ_APP_SECRET` 和 `QQ_ADMIN_OPENIDS`。

管理员标识支持以下格式，多个标识用英文逗号分隔：

```dotenv
QQ_ADMIN_OPENIDS=user:<user_openid>,union:<union_openid>,member:<group_openid>:<member_openid>
QQ_READONLY_ADMIN_OPENIDS=
```

OpenID 来自 QQ 消息事件中的用户标识，不是 QQ 号码。群管理员可使用 `member:<group_openid>:<member_openid>`。标识必须与机器人收到的消息事件匹配。

`QQ_READONLY_ADMIN_OPENIDS` 用于配置只读管理员。只读管理员可以查询配置和检查 RSS 源，不能修改订阅或配置。同一身份同时匹配完整管理员和只读管理员时，完整管理员权限优先。

### 本地运行

安装 Go 1.23 或更高版本，在项目目录执行：

```powershell
go run ./cmd/bot
```

构建 Windows 可执行文件：

```powershell
go build -o ./bin/qq-bot.exe ./cmd/bot
```

本地默认数据库路径为 `./data/bot.db`，健康检查端口为 `8080`。

### Docker 运行

安装 Docker 和 Docker Compose，完成 `.env` 配置后执行：

```sh
docker compose up -d --build
docker compose logs -f bot
```

Compose 将 `./data` 挂载到容器的 `/data`，并将数据库路径设为 `/data/bot.db`。健康检查默认仅监听宿主机的 `127.0.0.1:18080`。

停止服务：

```sh
docker compose down
```

## 命令

只有以下四个一级命令可用。旧命令和普通文本消息不会触发处理。群内使用时，可先 @机器人，再输入命令。

### `/help`

```text
/help
/whoami
/rss help
/rss add help
/llm help
/llm config help
```

`/help` 显示四个一级命令。详细帮助按当前身份的权限显示子命令。

### `/whoami`

```text
/whoami
```

私聊时显示当前用户的 `user_openid` 和可用的 `union_openid`。群聊时显示事件提供的 `user_openid`、当前群成员 `member_openid`、`group_openid` 和可用的 `union_openid`。群聊事件未提供 `user_openid` 时，仍会显示成员 OpenID 和群 OpenID。

### `/rss`

RSS/Atom 订阅按群隔离，只能在群聊中管理。

| 命令 | 权限 | 作用 |
| --- | --- | --- |
| `/rss list` | 所有人 | 列出当前群订阅 |
| `/rss status` | 所有人 | 查看最近检查、错误和待发送数量 |
| `/rss add <URL>` | 完整管理员 | 添加订阅 |
| `/rss remove <编号>` | 完整管理员 | 删除订阅及待发送文章 |
| `/rss pause <编号>` | 完整管理员 | 暂停订阅并清除待发送文章 |
| `/rss resume <编号>` | 完整管理员 | 恢复订阅并重新建立基线 |
| `/rss interval <时长>` | 完整管理员 | 设置本群检查间隔 |
| `/rss check <编号>` | 管理员，包括只读管理员 | 检查源，不改变推送进度 |

示例：

```text
/rss add https://example.com/feed.xml
/rss list
/rss interval 10m
/rss pause 1
/rss resume 1
```

添加订阅时，机器人将当前文章记录为基线，只推送后续新文章。恢复订阅时，机器人重新建立基线，不补发暂停期间的文章。检查间隔范围为 `1m` 至 `24h`。

采集结果和待发送文章会持久化。QQ 推送失败后，机器人在下一轮重试。如果 QQ 已收到消息，但机器人没有收到成功响应，重试可能产生重复消息。

`RSS_PROXY_URL` 可用于配置 HTTP(S)、SOCKS5 或 SOCKS5H 代理。列表和状态回复不显示订阅 URL，以免泄露 URL 中的凭据。

### `/llm`

LLM 对话无需账户绑定。单聊使用个人历史，群聊使用本群共享历史。群聊默认关闭，需要完整管理员执行 `/llm on`。

| 命令 | 权限 | 作用 |
| --- | --- | --- |
| `/llm <内容>` | 所有人 | 提交对话 |
| `/llm status` | 所有人 | 查看启用状态、模型和剩余次数 |
| `/llm reset` | 单聊本人；群聊完整管理员 | 清空当前会话历史 |
| `/llm on` | 完整管理员 | 开启当前群对话 |
| `/llm off` | 完整管理员 | 关闭当前群对话并取消相关任务 |
| `/llm config show [key]` | 管理员，包括只读管理员 | 查看配置，密钥不回显 |
| `/llm config set <key> <value>` | 完整管理员 | 保存配置覆盖，新任务使用新配置 |
| `/llm config reset <key\|all>` | 完整管理员 | 恢复 `.env` 或默认配置 |

先在 `.env` 配置接口：

```dotenv
LLM_ENABLED=true
LLM_BASE_URL=https://example.com/v1
LLM_API_KEY=your-api-key
LLM_MODEL=your-model
LLM_SYSTEM_PROMPT=请用中文回答。
```

`LLM_BASE_URL` 是接口基础地址。客户端会在其后追加 `/chat/completions`，不要填写完整的请求路径。

群聊示例：

```text
/llm on
/llm 解释这段 Go 代码
/llm status
/llm reset
```

管理员可修改模型和提示词：

```text
/llm config set model your-model
/llm config set system_prompt "请用中文回答，并给出简短示例。"
/llm config show model
/llm config reset system_prompt
```

含空格的值必须用引号包裹。空提示词使用 `""`。完整配置键列表可通过 `/llm config help` 查看。

`api_key`、`tavily_key`、`bing_key` 和 `search_model_key` 只能由完整管理员在单聊中设置或重置。配置覆盖加密保存到数据库，重启后仍然有效。`/llm config reset all` 清除配置覆盖，但保留群开关和会话历史。

`on`、`off`、`status`、`reset`、`help` 和 `config` 是 `/llm` 的保留子命令。问题正文保持原样，包括尖括号、换行和正文末尾的 `help`。

### 联网搜索

LLM 只提供 `web_search` 工具，不提供账户查询或旧项目的业务工具。模型按需调用搜索，因此所选模型必须支持工具调用。

| `LLM_SEARCH_BACKEND` | 所需配置 |
| --- | --- |
| `off` | 关闭搜索，默认值 |
| `tavily` | `LLM_TAVILY_KEY`，可修改 `LLM_TAVILY_URL` |
| `searxng` | `LLM_SEARXNG_URL`，实例需启用 JSON 搜索结果 |
| `bing_serpapi` | `LLM_BING_KEY`，可修改 `LLM_BING_URL` |
| `model_native` | `LLM_SEARCH_MODEL_URL`、`LLM_SEARCH_MODEL_KEY`、`LLM_SEARCH_MODEL` |

`model_native` 使用支持 `web_search_options` 的 Chat Completions 接口。搜索模型必须返回来源引用；仅返回正文不视为可验证的搜索结果。

机器人会在回复中附加搜索返回的来源。搜索失败时，回复会说明错误。主模型会收到当前会话历史；搜索服务只收到模型生成的搜索词，搜索词仍可能包含模型从对话中提取的内容。

## 主要配置

完整示例见 `.env.example`。进程环境变量优先于 `.env`。

| 配置 | 默认值 | 说明 |
| --- | --- | --- |
| `QQ_APP_ID` / `QQ_APP_SECRET` | 无 | 必填，QQ 机器人凭据 |
| `QQ_ADMIN_OPENIDS` | 无 | 必填，至少一个完整管理员标识 |
| `BOT_DATA_KEY` | 无 | 必填，Base64 编码的 32 字节随机密钥 |
| `DATA_PATH` | `./data/bot.db` | 本地数据库路径 |
| `LISTEN_ADDR` | `:8080` | 健康检查监听地址 |
| `LOG_LEVEL` | `info` | `debug`、`info`、`warn` 或 `error` |
| `QQ_API_TIMEOUT` | `10s` | QQ API 请求超时 |
| `RSS_ENABLED` | `true` | RSS 后台采集和推送开关 |
| `RSS_POLL_INTERVAL` | `5m` | 未配置群间隔时的默认间隔 |
| `RSS_HTTP_TIMEOUT` | `20s` | RSS 请求超时，最大 `2m` |
| `LLM_ENABLED` | `false` | LLM 全局开关 |
| `LLM_MINUTE_LIMIT` | `6` | 每个 QQ 身份的一分钟对话限额 |
| `LLM_DAILY_LIMIT` | `100` | 每个 QQ 身份的每日对话限额 |
| `LLM_CONCURRENCY` | `2` | 不同会话的最大并发数 |
| `LLM_QUEUE_SIZE` | `32` | 等待和执行中的任务容量 |
| `LLM_TIMEOUT_SECONDS` | `120` | 模型任务超时 |
| `LLM_HISTORY_TURNS` | `20` | 最多保留的历史轮数 |
| `LLM_HISTORY_TTL_SECONDS` | `86400` | 会话历史有效期 |

对话限额按北京时间划分日期。群成员身份包含群 OpenID，因此同一个人在不同群或单聊中的限额分别计算。

同一会话按顺序执行。LLM 任务会持久化，服务重启后可以恢复尚未执行的任务和已生成的待发送回复。已开始执行或已进入发送阶段的任务，重启后不自动重试，以免重复调用和重复回复。任务还受 QQ 被动回复时效限制。

## 健康检查

```sh
curl http://127.0.0.1:18080/healthz
curl http://127.0.0.1:18080/readyz
```

上述端口适用于默认 Compose 配置。本地运行使用 `8080`。

`/healthz` 检查数据库。`/readyz` 检查数据库、QQ Gateway 连接和 QQ access token。健康检查不探测 LLM 或 RSS 外部服务；其失败状态通过命令回复和日志查看。

## 数据与验证

备份前先停止机器人，再备份 `data` 目录和 `BOT_DATA_KEY`。不要在已有数据上更换密钥，否则加密内容无法解密。一个数据库文件只供一个机器人进程使用。

运行测试和静态检查：

```sh
go test ./...
go vet ./...
```

项目结构：

```text
cmd/bot/          启动入口
internal/bot/     三个命令、消息调度和后台任务
internal/config/  环境变量和 .env 配置
internal/health/  HTTP 健康检查
internal/llm/     模型接口和搜索客户端
internal/model/   QQ 身份、审计和消息记录
internal/qq/      QQ API 和 WebSocket Gateway
internal/rss/     RSS/Atom 采集与解析
internal/secure/  加密和消息认证
internal/store/   bbolt 持久化
```
