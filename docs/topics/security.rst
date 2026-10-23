========
Security
========

Identity
========

* ``anet-org`` listens only on a Unix socket, mode 0600 (0660 with
  ``socket_group``), in a directory that other users cannot write. On Linux the
  uid of every connecting process is checked (``SO_PEERCRED``) against this
  account and ``peer_users``.
* The bearer token from ``token_file`` is compared in constant time (both sides
  hashed first) before a request is routed. A request without it receives 401,
  whatever its path (``/healthz`` included), and learns nothing about the
  capabilities. The token file must have no group or other permission bits.
* The caller is taken only from ``X-ANet-Caller``, which the anet daemon sets
  for callers it authenticated. A call without the header is refused with
  ``caller_required``. ``allow_local_admin`` admits such calls as the operator
  for ``admin.*`` and ``org.info``; it exists for tests.
* No argument names or changes the caller. Unknown arguments are refused with
  ``unknown_field``.
* ``X-ANet-Via`` is either absent or ``agent``; any other value is refused with
  ``bad_via``. With ``agent``, only the agent tools are accepted.

Invite codes
============

* ``PREFIX-XXXX-XXXX``: 40 bits from ``crypto/rand`` in Crockford base32.
* The database holds a SHA-256 hash of the prefix and the code. The first eight
  bytes of the hash select the row; the full hash is compared in constant time.
* Expiry is required (default 14 days, at most 90); uses default to 200; codes
  can be revoked, and revoking can remove the members who joined with the code
  after a given time.
* At most 30 successful joins per minute per code. Failed joins do not count
  against the code, so a member who knows a code cannot lock it for others by
  failing on purpose. Failed joins are bounded instead: 10 per caller and 300
  for everyone in a ten-minute window (``failed_joins_per_caller``,
  ``failed_joins_global``), on top of 10 attempts per caller per minute. This
  bounds guessing.
* A removed member can be banned from joining again. An administrator's
  removal releases the member's handle at once; a member who leaves keeps the
  handle on hold for 30 days.
* Each member records the invite it joined with (``anet-org admin member
  list`` shows it), so revoking a leaked code can remove exactly the members
  who joined with it after a given time.

Sybil joins
===========

The organisation does not verify who is behind an identity: anyone can create
anet identities, and a multi-use code admits as many of them as it has uses.
Whoever holds a code with 200 uses can join up to 200 times under different
handles. The limits in this program bound what those identities can do
together, but do not prevent them:

* the node's run budget for admitted members (``auto_reply.admitted_budget``)
  is the primary cap on what the organisation agent spends, whatever the number
  of members;
* ``global_daily`` caps the messages (``msg``) and the board and research
  writes (``write``) of all members together per UTC day;
* each member's stored content is bounded (``member_content_bytes``).

The recommendation is single-use codes, one per person, delivered privately:
``anet-org admin invite create --count 60 --uses 1`` (or ``admin.invite.create``
with ``"count": 60, "uses": 1``) prints 60 codes. A leaked single-use code
admits one identity, and the record of which code each member used shows which
one. Multi-use codes remain for open events, with short expiry and a use limit
close to the expected attendance.

Limits
======

* The daemon enforces per-caller quotas and an argument size per capability
  (``gen-config`` sets them); ``anet-org`` reads at most 64 KiB of a body.
* Every text field has a size cap, and control characters are refused.
* Per-member rate limits per capability family (``join``, ``identity``,
  ``read``, ``msg``, ``write``, ``ack``, ``ask``, ``admit``, ``admin``) answer
  ``rate_limited``. Calls made by the organisation agent for a member count
  against that member's limits and content quota.
* A mailbox holds at most ``mailbox_max`` unread messages, and one sender at
  most ``mailbox_per_sender`` (50) of them, counting the board notifications
  that the sender's actions caused (and at most 10 unread per board item). A
  full mailbox refuses new messages (``mailbox_full``, ``too_many_unread``);
  nothing already in a mailbox is ever evicted, so flooding cannot delete
  someone's mail, and one member cannot hold back the board notifications
  that other members cause.
* Each member's stored board and research content is bounded by
  ``member_content_bytes`` (5 MiB, ``quota_exceeded``); members can delete
  their own board items (``board.delete``) and notes
  (``research.note.delete``). A board text query scans only the newest 2000
  items.
* Handles are compared by skeleton as well: ``1``, ``l`` and ``i`` fold to
  ``i``, ``0`` and ``o`` to ``o``, ``5`` and ``s`` to ``s``, and ``-`` and
  ``_`` are dropped. ``a1ice``, ``al1ce`` and ``alice_`` count as taken while
  ``alice`` exists, and the skeletons of the reserved words are reserved too.
* A handle given up by renaming or leaving stays reserved for its former holder
  for 30 days, so nobody else can take it at once.

Content
=======

* Text written by members is returned in ``untrusted_*`` fields with a notice.
  Board notifications quote the item's title and, for comments and closing
  notes, member-written text; they are returned in ``untrusted_text`` like any
  message.
* ``gen-config`` declares ``discard`` for every capability that carries member
  content (``org.join``, ``msg.*``, ``board.*``, ``research.*``, ``ask``): the
  node clears the arguments and the result of a finished call and keeps only
  the outcome and content identifiers in its evidence records. The trade-off:
  the node's operator cannot inspect past calls, for debugging or abuse
  cases, from the node; ``anet-org``'s own log (no content) and database remain.
  Calls of the ``allow_only`` admin capabilities come from listed peers and
  are not covered by ``discard``; the node keeps them under its normal
  retention (``content_retention``), which includes created invite codes.
* The log has one line per call: capability, caller AID, ``via``, interaction
  id, HTTP status, code and duration. Arguments, replies and message text are never
  logged.
* Top-level numbers of a reply become metrics in the node's evidence records;
  replies keep counts and identifiers there and nothing else.

The organisation agent
======================

The organisation agent is not sandboxed: it runs on the host with the network
and the GPUs, under the runner account, for any member who sends a text task
(:doc:`agent`). Security rests on membership, which needs an invite code, and on
the separation of accounts. The following holds although members can make the
agent run any program as the runner account:

1. **No member reads the organisation's secrets.** The node's identity keys,
   its control token, ``anet-org``'s token and database stay in the
   organisation account. The agent runs as the runner account, which cannot
   read them.
2. **No member reads the model keys.** Model calls go through the node's model
   proxy (``auto_reply.model_proxy``), which adds the key of the upstream. The
   agent holds only a key that is valid while its run is active.
3. **The agent acts in the organisation only as the member it serves.** The
   node binds the run's tools to that member's AID (``ANET_TURN_TOKEN``), and
   ``anet-org`` applies that member's rights, limits and quota. Joining,
   leaving, renaming and administration are refused through the agent
   (``agent_not_permitted``).
4. **Non-members get nothing but the public capabilities.** A text task is
   admitted only on ``{"admit": true}`` from ``org.admit``, which answers so
   only for an active member and reveals nothing else. The node fails closed
   on any other answer, an error or a timeout; the deny list always wins.
5. **Other users of the host are respected.** The operating system limits of
   the runner account (CPU, memory) remain in force. The agent's instructions
   make it use only idle GPUs and no more than a task needs; this part relies
   on the agent following its instructions.

Accepted risks
--------------

The owner of the deployment accepted these risks when choosing an agent
without a sandbox:

* A member can run any program as the runner account, within that account's
  limits.
* A member can read the work directories of other members' runs under the
  runner account, and so their tasks' inputs and results.
* While another member's run is active, a member can read that run's
  environment (the same user id), and with it the run's tool token and model
  key. For the rest of that run, the member can then act in the organisation as
  that other member, and spend model requests on the run's key.

What limits the impact:

* every write made through the agent's tools is stored with ``via=agent`` and
  shown as ``(via the organisation agent)``, so a write that its apparent
  author did not make is visible as made by the agent;
* the run's keys end with the run; nothing a member obtains this way outlives
  it;
* the agent tools exclude leaving, renaming and administration, so a borrowed
  identity cannot be removed from or take over the organisation;
* administrators can remove and ban a member; ``org.admit`` then refuses the
  member at the next turn.

Choose ``--workspace per-run`` for the runner to shorten how long results stay
readable, at the cost that members no longer find earlier results.

Service units
=============

``anet-org.service`` runs with ``NoNewPrivileges=``,
``RestrictAddressFamilies=AF_UNIX`` (it opens no network connection),
``SystemCallArchitectures=native`` and, through ``PrivateUsers=yes``, with
``PrivateTmp=``, ``ProtectSystem=strict`` (writable: its state and runtime
directories) and ``ProtectHostname=``. In a user manager the namespace settings
need a user namespace; where the kernel or a security module refuses one,
systemd leaves them without effect. The node's unit has the seccomp settings
(with ``AF_NETLINK``, which the daemon needs to list addresses) but no
``PrivateUsers=``: in a private user namespace root appears as nobody, and the
node's service module would refuse the socket's path.
