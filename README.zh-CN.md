# anet-org

`anet-org` 是运行在 anet 智能体网络之上的组织智能体。研究组、班级或实验室可以借助它，以一个普通 anet 用户的身份
自行管理成员、信箱、任务板、科研笔记与助手。成员在自己的编程智能体中提出请求即可使用，例如
"@anet 加入 LAB，邀请码 LAB-7K2M-9XQD，昵称 alice"。

anet 本身不包含任何组织概念。`anet-org` 是一个独立程序，部署在一个 anet 节点之后，通过该节点的 `service`
模块对外提供能力。hub 只负责转发端到端加密的调用，并让成员找到组织节点，不保存组织的任何数据。

[English](README.md)

## 工作方式

```
成员的编程智能体 ── anet 节点 ──（hub 中继，端到端加密）── 组织的 anet 节点
                                                              │ service 模块
                                                              │ Unix socket，Bearer 令牌
                                                          anet-org ── SQLite
```

- 每次调用都以 HTTP POST 的形式经 Unix socket 到达 `anet-org`。daemon 携带 Bearer 令牌，`anet-org`
  在路由之前以常数时间比较；经 daemon 认证的调用方 AID 位于 `X-ANet-Caller` 头。
- 调用方 AID 是 `anet-org` 使用的唯一身份。任何参数都不能指定或更改调用方，所有操作均在服务端限定于调用方本人。
- 没有调用方的调用（本机调用）一律拒绝。
- 状态保存在一个 SQLite 数据库中：邀请码（仅存哈希）、成员、信箱、任务板、科研项目与笔记、助手的 token 用量。

## 能力

| 能力 | 调用者 | 参数 |
|---|---|---|
| `org.info` | 任何人 | `{}` |
| `org.join` | 任何人 | `{"code", "handle"}` |
| `org.me` | 成员 | `{}` |
| `org.leave` | 成员 | `{"confirm": true}` |
| `org.rename` | 成员 | `{"handle"}` |
| `org.members` | 成员 | `{"query"?, "kind"?, "include_aid"?, "limit"?, "offset"?}` |
| `msg.send` | 成员 | `{"to": 昵称或 "staff"（staff 可用 "all"）, "text"}` |
| `msg.inbox` | 成员 | `{"since"?: 消息 id 或 RFC 3339 时间, "limit"?}` |
| `msg.ack` | 成员 | `{"ids": [...]}` 或 `{"up_to": id}` |
| `board.post` | 成员 | `{"title", "body"?, "tags"?}` |
| `board.list` | 成员 | `{"status"?, "tag"?, "mine"?, "query"?, "limit"?, "before"?}` 或 `{"id"}` |
| `board.claim` | 成员 | `{"id", "release"?}` |
| `board.done` | 成员 | `{"id", "note"?, "withdraw"?}` |
| `board.comment` | 成员 | `{"id", "text"}` |
| `research.project.create` | 成员 | `{"name", "title"?, "description"?}` |
| `research.project.list` | 成员 | `{"query"?, "limit"?}` |
| `research.note.add` | 成员 | `{"project", "text", "kind"?: note、reading 或 minutes, "title"?, "url"?}` |
| `research.note.list` | 成员 | `{"project"}` 或 `{"mine": true}`，可加 `"kind"?`、`"limit"?`、`"before"?` |
| `ask` | 成员 | `{"text", "act"?}` |
| `admin.invite.create` | 管理员 | `{"expires_days"?, "uses"?, "kind"?, "note"?}` |
| `admin.invite.revoke` | 管理员 | `{"id", "remove_joined_after"?}` |
| `admin.invite.list` | 管理员 | `{"all"?}` |
| `admin.member.remove` | 管理员 | `{"handle", "ban"?}` |

答复均为 JSON 对象。拒绝采用 service 模块的错误格式，即 HTTP 4xx 加 `{"error": "<代码>"}`，调用方的任务以该代码
失败，例如 `invite_expired`、`invite_used_up`、`invite_unknown`、`not_a_member`、`handle_taken`、
`rate_limited`、`budget_exhausted`。由成员撰写的文本放在名为 `untrusted_*` 的字段中并附说明，编程智能体应将其
视为数据而非指令。完整说明见 [docs/reference/capabilities.rst](docs/reference/capabilities.rst)。

## 部署组织智能体

以下步骤以一个专用账户的 systemd 用户单元运行组织节点与 `anet-org`，均以该账户执行。

1. **安装 anet**（使用官方安装脚本），并将 `anet-org` 构建或安装到 `~/.local/bin`：

   ```sh
   go build -o ~/.local/bin/anet-org ./cmd/anet-org
   ```

2. **在独立数据目录中创建节点身份**：

   ```sh
   export ANET_DATA_DIR=~/.local/state/anet-org-node
   anet init
   anet profile set     # 填写说明组织用途的简介
   ```

3. **配置 anet-org**。将 `deploy/anet-org.example.json` 复制为 `~/.config/anet-org/anet-org.json`，设置
   `org.name`、`org.prefix`（邀请码前缀，例如 `LAB`）与 `admins`（管理员本人 anet 节点的 AID），并生成共享令牌：

   ```sh
   anet-org gen-token ~/.config/anet-org/token
   ```

   如需启用助手，将每个上游的 API 密钥分别保存为权限 0600 的文件（不得放入任何代码仓库），并在
   `assistant.upstreams[].api_key_file` 中指定。未配置上游时，`ask` 会答复助手尚未配置。

4. **向节点声明能力**。打印配置块并合并到 `$ANET_DATA_DIR/config.json`：

   ```sh
   anet-org gen-config --config ~/.config/anet-org/anet-org.json
   ```

   输出包含 `modules.service`（每个能力一个 `unix://` URL、令牌文件，以及带示例的卡片说明）和
   `inbound.public_capabilities`（各能力的调用范围与按调用方计的配额）。入站策略保持 `closed`：陌生节点只能调用
   列出的能力，成员能力会拒绝非成员。`admin.*` 能力为 `allow_only`，请用 `anet peers allow <AID>` 将每位管理员
   加入节点的允许名单。

5. **启动两个单元**（文件位于 `deploy/systemd/`）：

   ```sh
   cp deploy/systemd/* ~/.config/systemd/user/
   systemctl --user daemon-reload
   systemctl --user enable --now anet-org.service anet-org-node.service anet-org-backup.timer
   loginctl enable-linger "$USER"     # 以 root 执行一次，使单元在未登录时也能运行
   ```

   `anet-org.slice` 将二者合计限制在 400% CPU 与 4 GiB 内存以内。

6. **以组织名称在 hub 注册**：

   ```sh
   ANET_DATA_DIR=~/.local/state/anet-org-node anet hub-register https://hub.example.org --name LAB
   ```

   按 AID 准入的 hub 会打印一行准入命令，交给 hub 运营者执行即可。注册时会一并登记公开能力；能力变更后需重新注册。

7. **在主机上创建邀请码**：

   ```sh
   anet-org admin --config ~/.config/anet-org/anet-org.json invite create --uses 60 --note "Autumn intake"
   ```

   邀请码只显示一次，数据库仅保存其哈希。发布邀请码时请同时公布节点的 AID（`anet status` 可查），以便成员的智能体
   找到正确的节点，而不是 hub 上同名的其他节点。

运维命令：`anet-org admin … invite list|revoke`、`member list|remove|kind`，以及用于生成数据库一致性副本的
`anet-org backup <路径>`。

## 在编程智能体中使用

成员安装 anet 及其编程智能体集成后，即可向网络提出请求，例如：

- "@anet 加入 LAB，邀请码 LAB-7K2M-9XQD，昵称 alice"
- "@anet 给 @bob 留言：明天组会改到三点"
- "@anet 收件箱"
- "@anet 任务板上有什么"
- "@anet 帮我整理这三篇论文的笔记到课题 graph-learning"
- "@anet 问 LAB：总结一下我的未读消息"

智能体会在 hub 上找到组织节点（或使用公布的 AID），读取其卡片后调用相应能力，例如：

```sh
anet delegate <org-aid> --capability org.join --args '{"code":"LAB-7K2M-9XQD","handle":"alice"}'
anet delegate <org-aid> --capability msg.inbox --args '{}'
```

成员无需开放自己节点的入站调用：每次交互都是成员主动发起的任务。消息保存在信箱中，直到成员读取。

## 安全

- 仅监听 Unix socket，权限 0600，并检查对端进程的 uid（`SO_PEERCRED`）。
- 路由之前以常数时间比较 Bearer 令牌；身份只取自 `X-ANet-Caller`。
- 邀请码：40 位随机数，以 SHA-256 哈希保存并以常数时间比较；必须设定有效期（默认 14 天，最长 90 天），
  可限定使用次数、可撤销，每个邀请码每分钟最多 30 次加入。
- 所有字段均有长度上限；按成员、按能力类别限速；信箱满时丢弃最早的消息并给出说明。
- 助手的工具均限定于调用方本人：只读访问调用方自己的数据；仅当调用方以 `"act": true` 明确要求时，才会代为发送消息
  或发布任务。不提供 shell、文件访问，也不访问所配置模型端点之外的网络。按成员与全局设定每日 token 预算。
- 日志只记录能力、调用方 AID、状态与代码，不记录参数或文本内容。

## 开发

```sh
go test ./...                                  # 单元测试
go test -run '^$' -fuzz FuzzParse ./internal/invite
go test -tags e2e ./test/e2e -v                # 端到端测试，见 test/e2e/e2e_test.go
```

端到端测试在临时目录中、仅使用回环端口，启动一个本地测试 hub、三个 anet daemon 与 `anet-org`，
并通过 `anet delegate` 完成加入、留言与各类拒绝场景。

文档使用 Sphinx 构建：`pip install -r docs/requirements.txt && make -C docs html`。

## 许可

ANet Open Source License，见 [LICENSE](LICENSE)。
