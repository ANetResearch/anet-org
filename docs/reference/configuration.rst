=============
Configuration
=============

``anet-org`` reads one JSON file, named with ``--config`` or ``ANET_ORG_CONFIG``.
Unknown keys are refused. Paths may start with ``$VAR`` or ``${VAR}``; a
variable that is not set is an error. ``deploy/anet-org.example.json`` is a
complete example.

Top-level keys
==============

``org.name`` (required)
   The organisation's name, at most 64 bytes.
``org.prefix`` (required)
   The invite prefix: 2 to 10 letters and digits, starting with a letter,
   upper-cased. The lower-case prefix and ``prefix-…`` are reserved handles.
``org.description``, ``org.join_instructions``, ``org.contact``
   Shown by ``org.info``.
``socket`` (required)
   The Unix socket path. Its directory is created with mode 0700 if missing and
   must not be writable by every user.
``socket_group``
   Give the socket to this group with mode 0660 (when the node runs as another
   account). Default: none, mode 0600.
``peer_users``
   Further accounts (names or uids) whose processes may connect. This account is
   always allowed.
``token_file`` (required)
   The bearer token the daemon presents; the same file as the node's
   ``modules.service.token_file``. At least 16 bytes, mode 0600 or stricter
   (any group or other permission bit is refused).
``database`` (required)
   The SQLite file; created with mode 0600.
``admins``
   AIDs that may call ``admin.*``; they count as staff.
``staff``
   AIDs that count as staff whatever their stored kind.
``allow_local_admin``
   Default ``false``. Treat calls without a caller as the operator for
   ``admin.*`` and ``org.info``. For tests.
``mailbox_max``
   Unread messages a mailbox holds; default 500, at most 100000. A full
   mailbox refuses new messages.
``mailbox_per_sender``
   Unread messages one sender may have in one mailbox, counting the board
   notifications caused by that sender; default 50.
``failed_joins_per_caller``, ``failed_joins_global``
   Failed ``org.join`` attempts in a ten-minute window, per caller (default 10)
   and for everyone (default 300).
``global_daily``
   Organisation-wide calls per UTC day: ``{"msg": 20000, "write": 20000}`` by
   default; ``msg`` is ``msg.send``, ``write`` the board and research writes.
``member_content_bytes``
   Bytes of board items, comments, projects and notes one member may store;
   default 5 MiB (5242880).
``joins_per_minute_per_code``
   Default 30.
``max_concurrent``
   Calls handled at once; default 64. Beyond it, calls are refused with
   ``busy``.
``agent_profile_file``
   Optional absolute path. The server writes the agent settings
   (``admin.settings.set``) there as JSON (``organisation``, ``brief``,
   ``model``, ``updated_at``), mode 0640, at start and after every change, for
   the account that runs the agent to read before each run. Its directory must
   be writable by the server; give it the group of the agent's account.
``rates``
   Per-member limits by family, each ``{"per_minute": N, "per_day": N}``.

   .. list-table::
      :header-rows: 1
      :widths: 15 25 60

      * - Family
        - Default
        - Capabilities
      * - ``join``
        - 10/min, 50/day
        - ``org.join`` (attempts per caller)
      * - ``identity``
        - 10/min, 50/day
        - ``org.rename``, ``org.leave``
      * - ``read``
        - 120/min, 5000/day
        - ``org.info``, ``org.me``, ``org.members``, ``msg.inbox``,
          ``board.list``, ``research.*.list``
      * - ``msg``
        - 20/min, 300/day
        - ``msg.send``
      * - ``write``
        - 30/min, 1000/day
        - ``board.*`` writes, ``research.*`` writes
      * - ``ack``
        - 60/min, 5000/day
        - ``msg.ack``
      * - ``ask``
        - 6/min, 100/day
        - ``ask`` (retired; answers ``ask_moved``)
      * - ``admit``
        - 600/min, 50000/day
        - ``org.admit`` (called by the node, per sender)
      * - ``admin``
        - 60/min, 2000/day
        - ``admin.*``

   The counts are kept in memory and start again after a restart.

Removed keys
============

``assistant``
   Removed in 0.3.9 together with the built-in assistant. A file that still
   has it is refused at start, with a message that says so. The organisation
   agent's model and keys are configured on the node (``auto_reply`` and its
   ``model_proxy``), which ``anet-org gen-config`` prints; see
   :doc:`../intro/agent-deployment`.
