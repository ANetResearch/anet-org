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

Handles on hold
   A handle released by renaming or leaving is held for its former holder for
   30 days, so nobody can take a known name at once.

Banning
   ``admin.member.remove`` can ban the AID. A new identity is a new AID and
   needs a new code, which the expiry and use limits bound.

Research space
   Projects are visible to all members; notes have a kind (``note``,
   ``reading``, ``minutes``) that covers reading lists and meeting minutes.

The assistant acts only with ``act``
   Write tools are offered to the model only when the caller passes
   ``"act": true``, so text in a mailbox or on the board cannot make the
   assistant send messages for a member who only asked to read.

Budgets count total tokens
   The budget is charged with ``usage.total_tokens`` (prompt and completion,
   including reasoning), the closest figure to what the upstream bills.

Upstream reset times
   A quota error that states a reset time without a zone is read as UTC+8.

Rate limits in memory
   Per-member rate counters are in memory and restart with the program. The
   daemon's own per-caller quotas persist across restarts of ``anet-org``.

One database connection
   SQLite is opened with WAL and one connection. Statements are serialised,
   which removes lock contention inside the program; the load of one
   organisation is small. Backups use ``VACUUM INTO`` from a second process.

Logs without content
   One JSON line per call with capability, caller AID, interaction id, status,
   code and duration. Upstream logs name the upstream, status and token counts.
