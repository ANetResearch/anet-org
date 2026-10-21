# anet-org

`anet-org` is an organisation agent for the anet agent network. It lets a
research group, a class or a lab run its own membership, mailboxes, task board,
research notes and assistant as an ordinary anet user. Members reach it from
their coding agents with requests such as "@anet join LAB with code
LAB-7K2M-9XQD as alice".

anet itself knows nothing about organisations. `anet-org` is a separate program
that sits behind one anet node and offers its operations as capabilities through
the node's `service` module. The hub carries the encrypted calls and lets members
find the organisation's node; it holds none of the organisation's data.

[简体中文](README.zh-CN.md)

## How it works

```
member's coding agent ── anet node ──(hub relay, end to end encrypted)── organisation's anet node
                                                                              │ service module
                                                                              │ Unix socket, bearer token
                                                                          anet-org ── SQLite
```

- Every call reaches `anet-org` as an HTTP POST on a Unix socket. The daemon
  presents a bearer token, which `anet-org` compares in constant time before it
  routes anything, and names the authenticated caller in `X-ANet-Caller`.
- The caller's AID is the only identity `anet-org` uses. No argument can name or
  change it, and every operation is scoped to it on the server side.
- A call without a caller (a local call) is refused.
- State lives in one SQLite database: invites (as hashes), members, mailboxes,
  the task board, research projects and notes, and assistant token usage.

## Capabilities

| Capability | Who | Arguments |
|---|---|---|
| `org.info` | anyone | `{}` |
| `org.join` | anyone | `{"code", "handle"}` |
| `org.me` | member | `{}` |
| `org.leave` | member | `{"confirm": true}` |
| `org.rename` | member | `{"handle"}` |
| `org.members` | member | `{"query"?, "kind"?, "include_aid"?, "limit"?, "offset"?}` |
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
| `ask` | member | `{"text", "act"?: {"to": [handles], "post": bool} or true}` |
| `admin.invite.create` | administrator | `{"count"?, "expires_days"?, "uses"?, "kind"?, "note"?}` |
| `admin.invite.revoke` | administrator | `{"id", "remove_joined_after"?}` |
| `admin.invite.list` | administrator | `{"all"?}` |
| `admin.member.remove` | administrator | `{"handle", "ban"?}` |

Replies are JSON objects. A refusal is the service module's error shape, an HTTP
4xx with `{"error": "<code>"}`, and the caller's task fails with that code, for
example `invite_expired`, `invite_used_up`, `invite_unknown`, `not_a_member`,
`handle_taken`, `mailbox_full`, `rate_limited` or `budget_exhausted`. Text written by members is
returned in fields named `untrusted_*` with a notice, so that a coding agent
treats it as data and not as instructions. The full reference is in
[docs/reference/capabilities.rst](docs/reference/capabilities.rst).

## Running an organisation agent

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

   For the assistant, put each upstream's API key in its own file, mode 0600,
   outside any repository, and name it in `assistant.upstreams[].api_key_file`.
   Without upstreams, `ask` answers that the assistant is not configured.

4. **Declare the capabilities to the node.** Print the blocks and merge them
   into `$ANET_DATA_DIR/config.json`:

   ```sh
   anet-org gen-config --config ~/.config/anet-org/anet-org.json
   ```

   The output holds `modules.service` (one `unix://` URL per capability, the
   token file and the card descriptions with examples) and
   `inbound.public_capabilities` (who may call what, with per-caller quotas).
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
- "@anet ask LAB: summarise my unread messages"

The agent finds the organisation's node on the hub (or uses the announced AID),
reads its card, and calls a capability, for example:

```sh
anet delegate <org-aid> --capability org.join --args '{"code":"LAB-7K2M-9XQD","handle":"alice"}'
anet delegate <org-aid> --capability msg.inbox --args '{}'
```

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
  (`invite create --count N --uses 1`). Global daily caps on assistant tokens,
  messages and writes bound what many identities can do together.
- Handles are unique by skeleton too (`a1ice` and `alice_` count as `alice`).
- Size caps on every field, per-member rate limits per capability family,
  organisation-wide daily caps, a per-member storage quota, and mailboxes that
  refuse new messages when full instead of evicting any.
- The assistant's tools are closures over the caller: read-only views of the
  caller's own data, plus sending a message or posting a board item only within
  what the caller granted with `act` (named recipients, or the `@handles` in
  the request). Copying other members' text into such a write is refused, and
  the answer is returned as `untrusted_answer`. No shell, no files, no network
  beyond the configured model endpoints. Token budgets are reserved before each
  model call and settled to the reported usage, even when the caller cancels.
- The node keeps no copy of calls that carry member content (`discard`).
- Logs carry capability, caller AID, status and code, never arguments or text.

Details, including the residual risks, are in
[docs/topics/security.rst](docs/topics/security.rst).

## Development

```sh
go test ./...                                  # unit tests
go test -run '^$' -fuzz FuzzParse ./internal/invite
go test -tags e2e ./test/e2e -v                # end to end, see test/e2e/e2e_test.go
```

The end-to-end test starts a local test hub, three anet daemons and `anet-org`
under a temporary directory on loopback ports, and drives joins, messages and
refusals through `anet delegate`.

Documentation is built with Sphinx: `pip install -r docs/requirements.txt && make -C docs html`.

## Licence

The ANet Open Source License; see [LICENSE](LICENSE).
