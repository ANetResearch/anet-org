# anet-org

`anet-org` is the organisation program for the anet agent network. It lets a
research group, a class or a lab run its own membership, mailboxes, task board
and research notes as an ordinary anet user. Members reach it from their coding
agents with requests such as "@anet join LAB with code LAB-7K2M-9XQD as alice".
Requests in natural language, such as "@anet ask LAB to benchmark this model on
two GPUs", are text tasks that the organisation agent answers: an agent program
that the node runs on the organisation's host and that uses the organisation as
the member who asked.

anet itself knows nothing about organisations. `anet-org` is a separate program
that sits behind one anet node and offers its operations as capabilities through
the node's `service` module. The hub carries the encrypted calls and lets members
find the organisation's node; it holds none of the organisation's data.

[简体中文](README.zh-CN.md)

## How it works

```
member's coding agent ── anet node ──(hub relay, end to end encrypted)── organisation's anet node
                                                                           │                 │
                                                    capability calls       │                 │  text tasks of members
                                                    (service module,       │                 │  (admitted by org.admit)
                                                    Unix socket, token)    ▼                 ▼
                                                                  anet-org ── SQLite    anet runner (second account)
                                                                           ▲                 │
                                                                           │                 ▼
                                                                           └─ agent tools ── organisation agent
                                                                              as the member
```

- Every call reaches `anet-org` as an HTTP POST on a Unix socket. The daemon
  presents a bearer token, which `anet-org` compares in constant time before it
  routes anything, and names the authenticated caller in `X-ANet-Caller`.
- The caller's AID is the only identity `anet-org` uses. No argument can name or
  change it, and every operation is scoped to it on the server side.
- A call without a caller (a local call) is refused.
- State lives in one SQLite database: invites (as hashes), members, mailboxes,
  the task board, research projects and notes.
- A text task is admitted only when `org.admit` answers `{"admit": true}`
  for the sender, that is, for an active member. The organisation agent then
  calls the agent tools with the member as `X-ANet-Caller` and
  `X-ANet-Via: agent`; every write made so is stored with `via=agent` and
  shown as "(via the organisation agent)".

## Capabilities

| Capability | Who | Arguments |
|---|---|---|
| `org.info` | anyone | `{}` |
| `org.join` | anyone | `{"code", "handle"}` |
| `org.admit` | the node (internal) | `{}` (ignored) |
| `org.me` | member | `{}` |
| `org.leave` | member | `{"confirm": true}` |
| `org.rename` | member | `{"handle"}` |
| `org.members` | member | `{"query"?, "kind"?, "limit"?, "offset"?}` (handles only; AIDs are for administrators) |
| `msg.send` | member | `{"to": handle or "staff" (or "all" for staff), "text"}` |
| `msg.inbox` | member | `{"since"?: id or RFC 3339 time, "limit"?}` |
| `msg.ack` | member | `{"ids": [...]}` or `{"up_to": id}` |
| `board.post` | member | `{"title", "body"?, "tags"?}` |
| `board.list` | member | `{"status"?, "tag"?, "mine"?, "query"?, "limit"?, "before"?}` or `{"id"}` |
| `board.claim` | member | `{"id", "release"?}` |
| `board.done` | member | `{"id", "note"?, "withdraw"?}` |
| `board.comment` | member | `{"id", "text"}` |
| `board.delete` | member | `{"id"}` |
| `research.project.create` | member | `{"name", "title"?, "description"?}` |
| `research.project.list` | member | `{"query"?, "limit"?}` |
| `research.note.add` | member | `{"project", "text", "kind"?: note, reading or minutes, "title"?, "url"?}` |
| `research.note.list` | member | `{"project"}` or `{"mine": true}`, with `"kind"?`, `"limit"?`, `"before"?` |
| `research.note.delete` | member | `{"id"}` |
| `ask` | member | retired: answers `ask_moved` (send a text task instead) |
| `admin.invite.create` | administrator | `{"count"?, "expires_days"?, "uses"?, "kind"?, "note"?}` |
| `admin.invite.revoke` | administrator | `{"id", "remove_joined_after"?}` |
| `admin.invite.list` | administrator | `{"all"?}` |
| `admin.member.remove` | administrator | `{"handle", "ban"?}` |
| `admin.member.list` | administrator | `{"query"?}` (with AIDs) |
| `admin.settings.get` | administrator | `{}` |
| `admin.settings.set` | administrator | `{"brief"?, "model"?: plan or api}` |

Replies are JSON objects. A refusal is the service module's error shape, an HTTP
4xx with `{"error": "<code>"}`, and the caller's task fails with that code, for
example `invite_expired`, `invite_used_up`, `invite_unknown`, `not_a_member`,
`handle_taken`, `mailbox_full`, `rate_limited` or `agent_not_permitted`. Text written by members is
returned in fields named `untrusted_*` with a notice, so that a coding agent
treats it as data and not as instructions. Every member capability except
`org.leave` and `org.rename` is also an agent tool. Claims, comments and the
closing of a board item reach the other party as a message from `board`. The
full reference is in [docs/reference/capabilities.rst](docs/reference/capabilities.rst).

## Deploying an organisation

The steps below run the organisation's node and `anet-org` as systemd user
units of one dedicated account. Run them as that account.

1. **Install anet** with the official installer, and build or install
   `anet-org` into `~/.local/bin`:

   ```sh
   go build -o ~/.local/bin/anet-org ./cmd/anet-org
   ```

2. **Create the node's identity** in its own data directory:

   ```sh
   export ANET_DATA_DIR=~/.local/state/anet-org-node
   anet init
   anet profile set     # a summary that says what the organisation is
   ```

3. **Configure anet-org.** Copy `deploy/anet-org.example.json` to
   `~/.config/anet-org/anet-org.json`, set `org.name`, `org.prefix` (the invite
   prefix, for example `LAB`) and `admins` (the AID of the administrator's own
   anet node). Create the shared token:

   ```sh
   anet-org gen-token ~/.config/anet-org/token
   ```

   For the organisation agent, put each model key in its own file, mode 0600,
   outside any repository (by default `~/.config/anet-org/ark-plan.key` and
   `~/.config/anet-org/ark-api.key`). Only the node reads them.

4. **Declare the capabilities to the node.** Print the blocks and merge them
   into `$ANET_DATA_DIR/config.json`:

   ```sh
   anet-org gen-config --config ~/.config/anet-org/anet-org.json
   ```

   The output holds `modules.service` (one `unix://` URL per capability, the
   token file, the card descriptions with examples, and `agent_tool` with an
   `input_schema` for the agent tools), `inbound.public_capabilities` (who may
   call what, with per-caller quotas), `inbound.admit` and `auto_reply` (the
   organisation agent: runner, model proxy and system prompt). Without an
   agent, add `--no-agent`. Setting up the agent and its `anet runner` account
   is described in [docs/intro/agent-deployment.rst](docs/intro/agent-deployment.rst).
   The inbound policy stays `closed`: strangers can call only the listed
   capabilities, and the member capabilities refuse non-members. The `admin.*`
   capabilities are `allow_only`; put each administrator's AID on the node's
   allow list with `anet peers allow <AID>`.

5. **Start both units** (from `deploy/systemd/`):

   ```sh
   cp deploy/systemd/* ~/.config/systemd/user/
   systemctl --user daemon-reload
   systemctl --user enable --now anet-org.service anet-org-node.service anet-org-backup.timer
   loginctl enable-linger "$USER"     # as root, once, so the units run without a login
   ```

   `anet-org.slice` caps both together at 400 % CPU and 4 GiB of memory.

6. **Register on a hub** with the organisation's name:

   ```sh
   ANET_DATA_DIR=~/.local/state/anet-org-node anet hub-register https://hub.example.org --name LAB
   ```

   A hub that admits by AID prints an admit line for its operator. Registration
   folds in the public capabilities; register again after changing them.

7. **Create invites** on the host, one single-use code per person:

   ```sh
   anet-org admin --config ~/.config/anet-org/anet-org.json invite create --count 60 --uses 1 --note "Autumn intake"
   ```

   Each code is printed once; only its hash is stored. Give the codes out
   together with the node's AID (from `anet status`), so that members' agents
   address the right node and not a look-alike name on the hub.

Operator commands: `anet-org admin … invite list|revoke`, `member list|remove|kind`,
and `anet-org backup <path>` for a consistent copy of the database.

## Using it from a coding agent

Members install anet with its coding-agent integration, then address requests to
the network. Typical requests:

- "@anet join LAB with invite code LAB-7K2M-9XQD, handle alice"
- "@anet tell @bob in LAB: the group meeting moves to 3 pm"
- "@anet check my LAB inbox"
- "@anet what is on the LAB task board?"
- "@anet add my notes on these three papers to project graph-learning"
- "@anet ask LAB to summarise my unread messages" (a text task)
- "@anet ask LAB to find someone who can review the statistics in chapter 3"

The agent finds the organisation's node on the hub (or uses the announced AID),
reads its card, and calls a capability, for example:

```sh
anet delegate <org-aid> --capability org.join --args '{"code":"LAB-7K2M-9XQD","handle":"alice"}'
anet delegate <org-aid> --capability msg.inbox --args '{}'
```

A text task such as "@anet ask LAB to ..." is sent to the organisation's node as
a message; the organisation agent does the work and replies. To find people, it
posts a request on the task board with the skills needed as tags and reports
the item id; claims and comments reach the member's mailbox.

Members never open their own node to inbound calls: every exchange is a task the
member starts. Messages wait in the mailbox until the member reads them.

## Security

- Unix socket only, mode 0600, with the peer's uid checked (`SO_PEERCRED`).
- Bearer token compared in constant time before routing; identity only from
  `X-ANet-Caller`.
- Invite codes: 40 random bits, stored as SHA-256 hashes, compared in constant
  time; expiry (default 14 days, at most 90), use limits, revocation, at most
  30 successful joins per minute per code, and failed joins limited per caller
  and globally. Identities are not verified, so a multi-use code admits as many
  identities as it has uses: give each person a single-use code
  (`invite create --count N --uses 1`). Global daily caps on messages and
  writes, and the node's run budget for admitted members, bound what many
  identities can do together.
- Handles are unique by skeleton too (`a1ice` and `alice_` count as `alice`).
- Size caps on every field, per-member rate limits per capability family,
  organisation-wide daily caps, a per-member storage quota, and mailboxes that
  refuse new messages when full instead of evicting any.
- The organisation agent is not sandboxed; it runs under a second account
  (`anet runner`) that holds no keys. It reaches the model through the node's
  keyed proxy with a per-run key and the organisation only through tools bound
  to the member it serves. Joining, leaving, renaming and administration are
  refused through it (`agent_not_permitted`). The accepted risks, among them
  that members can read each other's runner work directories and, during
  another member's run, act as that member for the rest of the run, are listed
  in the security model; agent-made writes are marked so this is visible.
- The node keeps no copy of calls that carry member content (`discard`).
- Logs carry capability, caller AID, via, status and code, never arguments or text.

Details, including the residual risks, are in
[docs/topics/security.rst](docs/topics/security.rst).

## Development

```sh
go test ./...                                  # unit tests
go test -run '^$' -fuzz FuzzParse ./internal/invite
go test -tags e2e ./test/e2e -v                # end to end, see test/e2e/e2e_test.go
```

The end-to-end test starts a local test hub, three anet daemons and `anet-org`
under a temporary directory on loopback ports, and drives joins, messages, a
board notification and refusals through `anet delegate`.

Documentation is built with Sphinx: `pip install -r docs/requirements.txt && make -C docs html`.

## Licence

The ANet Open Source License; see [LICENSE](LICENSE).
