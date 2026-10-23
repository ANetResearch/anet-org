# anet-org

`anet-org` 是 anet 智能体网络上的组织程序。研究组、班级或实验室可以借助它，以一个普通 anet 用户的身份
自行管理成员、信箱、任务板与科研笔记。成员在自己的编程智能体中提出请求即可使用，例如
"@anet 加入 LAB，邀请码 LAB-7K2M-9XQD，昵称 alice"。自然语言请求（例如"@anet 请 LAB 用两块 GPU 测试这个模型"）
作为文本任务发送，由组织智能体答复：它是组织节点在组织主机上运行的智能体程序，以提出请求的成员的身份使用组织功能。

anet 本身不包含任何组织概念。`anet-org` 是一个独立程序，部署在一个 anet 节点之后，通过该节点的 `service`
模块对外提供能力。hub 只负责转发端到端加密的调用，并让成员找到组织节点，不保存组织的任何数据。

[English](README.md)

## 工作方式

```
成员的编程智能体 ── anet 节点 ──（hub 中继，端到端加密）── 组织的 anet 节点
                                                            │                │
                                          能力调用          │                │  成员的文本任务
                                          （service 模块，  │                │  （经 org.admit 准入）
                                          Unix socket，令牌）▼                ▼
                                                   anet-org ── SQLite    anet runner（另一账户）
                                                            ▲                │
                                                            │                ▼
                                                            └─ 智能体工具 ── 组织智能体
                                                               以成员身份
```

- 每次调用都以 HTTP POST 的形式经 Unix socket 到达 `anet-org`。daemon 携带 Bearer 令牌，`anet-org`
  在路由之前以常数时间比较；经 daemon 认证的调用方 AID 位于 `X-ANet-Caller` 头。
- 调用方 AID 是 `anet-org` 使用的唯一身份。任何参数都不能指定或更改调用方，所有操作均在服务端限定于调用方本人。
- 没有调用方的调用（本机调用）一律拒绝。
- 状态保存在一个 SQLite 数据库中：邀请码（仅存哈希）、成员、信箱、任务板、科研项目与笔记。
- 仅当 `org.admit` 对发送方答复 `{"admit": true}`（即发送方为在册成员）时，文本任务才会被接受。组织智能体随后以该成员
  为 `X-ANet-Caller`、并带 `X-ANet-Via: agent` 调用智能体工具；经此途径的每次写入均记录 `via=agent`，并向读者显示
  "(via the organisation agent)"。

## 能力

| 能力 | 调用者 | 参数 |
|---|---|---|
| `org.info` | 任何人 | `{}` |
| `org.join` | 任何人 | `{"code", "handle"}` |
| `org.admit` | 节点（内部） | `{}`（忽略） |
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
| `board.delete` | 成员 | `{"id"}` |
| `research.project.create` | 成员 | `{"name", "title"?, "description"?}` |
| `research.project.list` | 成员 | `{"query"?, "limit"?}` |
| `research.note.add` | 成员 | `{"project", "text", "kind"?: note、reading 或 minutes, "title"?, "url"?}` |
| `research.note.list` | 成员 | `{"project"}` 或 `{"mine": true}`，可加 `"kind"?`、`"limit"?`、`"before"?` |
| `research.note.delete` | 成员 | `{"id"}` |
| `ask` | 成员 | 已停用：答复 `ask_moved`（请改为发送文本任务） |
| `admin.invite.create` | 管理员 | `{"count"?, "expires_days"?, "uses"?, "kind"?, "note"?}` |
| `admin.invite.revoke` | 管理员 | `{"id", "remove_joined_after"?}` |
| `admin.invite.list` | 管理员 | `{"all"?}` |
| `admin.member.remove` | 管理员 | `{"handle", "ban"?}` |

答复均为 JSON 对象。拒绝采用 service 模块的错误格式，即 HTTP 4xx 加 `{"error": "<代码>"}`，调用方的任务以该代码
失败，例如 `invite_expired`、`invite_used_up`、`invite_unknown`、`not_a_member`、`handle_taken`、
`rate_limited`、`agent_not_permitted`。由成员撰写的文本放在名为 `untrusted_*` 的字段中并附说明，编程智能体应将其
视为数据而非指令。除 `org.leave` 与 `org.rename` 外，所有成员能力也是智能体工具。任务被认领、评论或关闭时，另一方会收到
来自 `board` 的消息。完整说明见 [docs/reference/capabilities.rst](docs/reference/capabilities.rst)。

## 部署组织

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

   如需组织智能体，将每个模型密钥分别保存为权限 0600 的文件（不得放入任何代码仓库；默认为
   `~/.config/anet-org/ark-plan.key` 与 `~/.config/anet-org/ark-api.key`），仅由节点读取。

4. **向节点声明能力**。打印配置块并合并到 `$ANET_DATA_DIR/config.json`：

   ```sh
   anet-org gen-config --config ~/.config/anet-org/anet-org.json
   ```

   输出包含 `modules.service`（每个能力一个 `unix://` URL、令牌文件、带示例的卡片说明，以及智能体工具的
   `agent_tool` 与 `input_schema`）、`inbound.public_capabilities`（各能力的调用范围与按调用方计的配额）、
   `inbound.admit` 与 `auto_reply`（组织智能体：runner、模型代理与系统提示词）。不使用智能体时加 `--no-agent`。
   组织智能体及其 `anet runner` 账户的部署见 [docs/intro/agent-deployment.rst](docs/intro/agent-deployment.rst)。入站策略保持 `closed`：陌生节点只能调用
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

7. **在主机上创建邀请码**，每人一个一次性邀请码：

   ```sh
   anet-org admin --config ~/.config/anet-org/anet-org.json invite create --count 60 --uses 1 --note "Autumn intake"
   ```

   每个邀请码只显示一次，数据库仅保存其哈希。发放邀请码时请同时告知节点的 AID（`anet status` 可查），以便成员的
   智能体找到正确的节点，而不是 hub 上同名的其他节点。

运维命令：`anet-org admin … invite list|revoke`、`member list|remove|kind`，以及用于生成数据库一致性副本的
`anet-org backup <路径>`。

## 在编程智能体中使用

成员安装 anet 及其编程智能体集成后，即可向网络提出请求，例如：

- "@anet 加入 LAB，邀请码 LAB-7K2M-9XQD，昵称 alice"
- "@anet 给 @bob 留言：明天组会改到三点"
- "@anet 收件箱"
- "@anet 任务板上有什么"
- "@anet 帮我整理这三篇论文的笔记到课题 graph-learning"
- "@anet 请 LAB 总结一下我的未读消息"（文本任务）
- "@anet 请 LAB 帮我找一位能审阅第三章统计方法的成员"

智能体会在 hub 上找到组织节点（或使用公布的 AID），读取其卡片后调用相应能力，例如：

```sh
anet delegate <org-aid> --capability org.join --args '{"code":"LAB-7K2M-9XQD","handle":"alice"}'
anet delegate <org-aid> --capability msg.inbox --args '{}'
```

"@anet 请 LAB ……"这类文本任务以消息形式发送到组织节点，由组织智能体完成工作并答复。需要他人协助时，它会在任务板上
发布求助，以所需技能作为标签，并告知任务编号；认领与评论会送达成员的信箱。

成员无需开放自己节点的入站调用：每次交互都是成员主动发起的任务。消息保存在信箱中，直到成员读取。

## 安全

- 仅监听 Unix socket，权限 0600，并检查对端进程的 uid（`SO_PEERCRED`）。
- 路由之前以常数时间比较 Bearer 令牌；身份只取自 `X-ANet-Caller`。
- 邀请码：40 位随机数，以 SHA-256 哈希保存并以常数时间比较；必须设定有效期（默认 14 天，最长 90 天），
  可限定使用次数、可撤销；每个邀请码每分钟最多 30 次成功加入，失败的加入尝试按调用方与全局分别限量。
  系统不核验身份，多次使用的邀请码可让同等数量的身份加入，因此建议为每人发放一次性邀请码
  （`invite create --count N --uses 1`）。消息与写入设有全组织每日上限，节点对已准入成员另设运行预算，
  约束大量身份的合计行为。
- 昵称还按"骨架"判重：`a1ice`、`alice_` 与 `alice` 视为相同。
- 所有字段均有长度上限；按成员、按能力类别限速；设有全组织每日上限与每位成员的存储配额；信箱满时拒收新消息，
  不会删除已有消息。
- 组织智能体不在沙箱中运行，而是在不持有任何密钥的另一账户（`anet runner`）下运行。它通过节点的带密钥代理访问模型，
  仅持有本次运行有效的密钥；它只能通过绑定到所服务成员的工具使用组织功能。加入、退出、改名与管理操作不能经由它执行
  （`agent_not_permitted`）。已接受的风险（包括成员可读取彼此在 runner 账户下的工作目录，以及在其他成员的运行期间
  以该成员身份行事直至该运行结束）列于安全模型中；智能体所做的写入均有标记，使此类情况可见。
- 节点不保留携带成员内容的调用（`discard`）。
- 日志只记录能力、调用方 AID、via、状态与代码，不记录参数或文本内容。

详细说明及剩余风险见 [docs/topics/security.rst](docs/topics/security.rst)。

## 开发

```sh
go test ./...                                  # 单元测试
go test -run '^$' -fuzz FuzzParse ./internal/invite
go test -tags e2e ./test/e2e -v                # 端到端测试，见 test/e2e/e2e_test.go
```

端到端测试在临时目录中、仅使用回环端口，启动一个本地测试 hub、三个 anet daemon 与 `anet-org`，
并通过 `anet delegate` 完成加入、留言、任务板通知与各类拒绝场景。

文档使用 Sphinx 构建：`pip install -r docs/requirements.txt && make -C docs html`。

## 许可

ANet Open Source License，见 [LICENSE](LICENSE)。
