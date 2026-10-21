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
   ``modules.service.token_file``. At least 16 bytes, not accessible to other
   users.
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
   Default 500, at most 100000.
``joins_per_minute_per_code``
   Default 30.
``max_concurrent``
   Calls handled at once; default 64. Beyond it, calls are refused with
   ``busy``.
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
        - ``msg.ack``, ``board.*`` writes, ``research.*`` writes
      * - ``ask``
        - 6/min, 100/day
        - ``ask``
      * - ``admin``
        - 60/min, 2000/day
        - ``admin.*``

   The counts are kept in memory and start again after a restart.

``assistant``
=============

``upstreams``
   A list of ``{"name", "base_url", "model", "api_key_file",
   "max_concurrency"}``, in order of preference. ``base_url`` is https (or http
   on a loopback address) and ends before ``/chat/completions``. ``model``
   defaults to ``deepseek-v4-1-flash-261017``; ``max_concurrency`` to 8. An
   empty list leaves the assistant unconfigured.
``member_daily_tokens``, ``global_daily_tokens``
   Defaults 2,000,000 and 200,000,000; 0 is unlimited.
``max_steps``
   Model calls per ``ask``, default 6, at most 12.
``max_output_tokens``
   ``max_tokens`` of each model call, default 4096.
``timeout_seconds``
   One upstream request, default 90.
``queue_size``, ``queue_wait_seconds``
   Defaults 64 and 30.
``quota_cooldown_seconds``
   Default 3600.
``backoff_base_seconds``, ``backoff_max_seconds``
   Defaults 30 and 900.
