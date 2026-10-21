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
   not passed on); only a full server or a full assistant queue answers 503
   ``busy``.

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

The assistant acts only within the caller's grant
   Write tools are offered only for what ``act`` grants: explicit recipients
   and posting, or with ``"act": true`` the ``@handles`` and the post request
   in the caller's own text. Copying member-written tool output into a write is
   refused. The answer is returned as ``untrusted_answer``.

Budgets count total tokens and are reserved first
   The budget is charged with ``usage.total_tokens`` (prompt and completion,
   including reasoning), the closest figure to what the upstream bills. Each
   model call first reserves half a token per prompt character plus
   ``max_output_tokens``, and is then settled to the reported usage; a call
   that may have been served without a usage report (a timeout, a broken
   reply) keeps its reservation. The global budget is the primary cap.

Upstream reset times
   A quota error that states a reset time without a zone is read as UTC+8.

Rate limits in memory
   Per-member rate counters, the organisation-wide daily caps on messages and
   writes, and the failed-join counters are in memory and restart with the
   program. The daemon's own per-caller quotas persist across restarts of
   ``anet-org``, and the assistant's token usage is in the database.

``/healthz`` needs the token
   Without the token the server tells nothing, not even its version.

One database connection
   SQLite is opened with WAL and one connection. Statements are serialised,
   which removes lock contention inside the program; the load of one
   organisation is small. Backups use ``VACUUM INTO`` from a second process.

Logs without content
   One JSON line per call with capability, caller AID, interaction id, status,
   code and duration. Upstream logs name the upstream, status and token counts.
