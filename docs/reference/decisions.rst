=========
Decisions
=========

The design leaves some points open. This page records how ``anet-org`` settles
them.

Errors carry codes, not sentences
   The anet service module passes a 4xx refusal's ``error`` code to the caller
   and keeps the body's other text in the node's log. Codes are therefore
   specific (``unknown_field``, ``too_long``, ``handle_taken``) so that an agent
   can act on the code alone. 429 and 503 become ``busy`` in the daemon and lose
   the code, so ``rate_limited`` is sent as 403 (its ``Retry-After`` header is
   not passed on); only a full server answers 503 ``busy``.

Admin capabilities are ``allow_only``
   ``admin.*`` stay off the public card. Administrators are put on the node's
   allow list, and ``anet-org`` still checks their AIDs against ``admins``. The
   operator on the host uses the ``admin`` command line, which needs no network.

Groups for messages
   ``msg.send`` accepts a handle or the groups ``staff`` (anyone may write to
   staff) and ``all`` (staff only). Both names are reserved handles.

Acknowledging deletes
   ``msg.ack`` removes messages. A mailbox holds what has not been read, and a
   deleted message cannot leak later.

Full mailboxes refuse, never evict
   A full mailbox refuses new messages, and one sender can fill at most 50
   places in another member's mailbox. Evicting the oldest message would let a
   flood delete someone's mail.

Handles on hold
   A handle released by renaming or leaving is held for its former holder for
   30 days, so nobody can take a known name at once. An administrator's removal
   releases it at once: a removed member has no claim to the name.

Handle skeletons
   Look-alike handles are refused by comparing skeletons (``1``/``l``/``i``,
   ``0``/``o``, ``5``/``s`` folded; ``-`` and ``_`` dropped). This also refuses
   some unrelated pairs (``ilan`` and ``lian``); the member chooses another
   handle.

No identity verification
   The owner chose not to verify identities. Multi-use codes stay available;
   bulk single-use codes are the recommendation, and the global daily caps
   bound what many identities can do together (:doc:`../topics/security`).

Failed joins do not charge the code
   The per-code limit counts successful joins only, and failed joins are
   limited per caller and globally, so knowing a code is not enough to lock it.

Content discarded on the node
   ``gen-config`` declares ``discard`` for the capabilities that carry member
   content; ``anet-org``'s database is the only copy.

Banning
   ``admin.member.remove`` can ban the AID. A new identity is a new AID and
   needs a new code, which the expiry and use limits bound.

Research space
   Projects are visible to all members; notes have a kind (``note``,
   ``reading``, ``minutes``) that covers reading lists and meeting minutes.

No intelligence in ``anet-org``
   Requests in natural language are text tasks answered by the organisation
   agent that the node runs. ``anet-org`` keeps the organisation's state and
   rules; it calls no model. ``ask`` stays until 0.4 only to answer
   ``ask_moved``.

Admission is a yes or no
   ``org.admit`` answers ``{"admit": true}`` or ``{"admit": false}`` and
   ignores its arguments, so that the node's admission check reveals nothing
   that ``org.info`` does not already tell the caller about itself. It has its
   own rate family, ``admit``, so that admission checks never use up a
   member's ``read`` allowance.

The agent gate is enforced twice
   The node calls only the capabilities declared ``agent_tool``, and
   ``anet-org`` refuses every other capability with ``X-ANet-Via: agent``
   (``agent_not_permitted``), so that a mistaken node configuration cannot let
   the agent leave, rename or administer in a member's name.

Marks instead of separate identities
   The agent writes as the member it serves, with the member's limits and
   quota, and every such write is stored with ``via=agent``. Readers can tell
   what the agent wrote; no second identity per member is needed.

Notifications come from ``board``
   Board notifications are messages from the reserved handle ``board``, kept in
   the mailbox until acknowledged like any other. They do not count against
   the actor's message limits, and one mailbox holds at most
   ``mailbox_per_sender`` of them unread. An action never fails because a
   notification could not be delivered.

Rate limits in memory
   Per-member rate counters, the organisation-wide daily caps on messages and
   writes, and the failed-join counters are in memory and restart with the
   program. The daemon's own per-caller quotas persist across restarts of
   ``anet-org``.

``/healthz`` needs the token
   Without the token the server tells nothing, not even its version.

One database connection
   SQLite is opened with WAL and one connection. Statements are serialised,
   which removes lock contention inside the program; the load of one
   organisation is small. Backups use ``VACUUM INTO`` from a second process.

Logs without content
   One JSON line per call with capability, caller AID, ``via``, interaction
   id, status, code and duration.
