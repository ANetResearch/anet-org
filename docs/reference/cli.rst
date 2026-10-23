============
Command line
============

``anet-org serve --config FILE``
   Run the server. Logs are JSON lines on standard error.

``anet-org gen-config --config FILE [--token-file PATH] [--public-admin] [--no-agent] [agent flags]``
   Print the blocks for the node's ``config.json``: ``modules.service`` (every
   capability, with ``agent_tool`` and ``input_schema`` for the agent tools and
   ``internal`` for ``org.admit``), ``inbound`` (``policy``,
   ``public_capabilities`` and ``admit``) and ``auto_reply``.
   ``--token-file`` names the token file as the daemon should read it (for
   example ``${CREDENTIALS_DIRECTORY}/token``); by default it is the
   configuration's ``token_file``. ``--public-admin`` lists ``admin.*`` as
   ordinary public capabilities instead of ``allow_only``. ``--no-agent``
   leaves out ``inbound.admit`` and ``auto_reply``.

   The agent flags, with their defaults:

   .. list-table::
      :header-rows: 1
      :widths: 35 30 35

      * - Flag
        - Default
        - Block
      * - ``--agent``
        - ``hermes``
        - ``auto_reply.agent``
      * - ``--admitted``
        - ``direct``
        - ``auto_reply.admitted`` (``direct`` or ``sandbox``)
      * - ``--admit-cache-seconds``
        - 30 (1 to 300)
        - ``inbound.admit.cache_seconds``
      * - ``--runner-socket``
        - ``/run/anet-runner/runner.sock``
        - ``auto_reply.runner.socket``
      * - ``--runner-user``
        - ``anet-runner``
        - ``auto_reply.runner.expected_user``
      * - ``--model-proxy-listen``
        - ``127.0.0.1:47811`` (loopback only)
        - ``auto_reply.model_proxy.listen``
      * - ``--model-plan-base``, ``--model-api-base``
        - the plan and API endpoints of the default model service (https only)
        - ``auto_reply.model_proxy.upstreams[].api_base``
      * - ``--model-plan-key-file``, ``--model-api-key-file``
        - ``${HOME}/.config/anet-org/ark-plan.key``,
          ``${HOME}/.config/anet-org/ark-api.key``
        - ``auto_reply.model_proxy.upstreams[].api_key_file``

   Paths are written as the current environment resolves them. The output
   also carries the organisation agent's prompt in
   ``auto_reply.system_prompt``.

``anet-org gen-agent --config FILE [--agent hermes] [--model NAME] [--anet-command PATH] [--part all|config|prompt]``
   Print the agent program's configuration fragment and the organisation
   agent's system prompt. For Hermes, the fragment is YAML for
   ``config.yaml``: ``model`` and ``providers`` (``anet-plan`` and
   ``anet-api`` on ``${ANET_MODEL_BASE}/plan`` and ``${ANET_MODEL_BASE}/api``,
   ``key_env: ANET_MODEL_KEY``), ``fallback_providers`` (``anet-api``) and
   ``mcp_servers.org`` (``anet turn-mcp`` with ``ANET_TURN_URL`` and
   ``ANET_TURN_TOKEN``). ``--model`` defaults to
   ``deepseek-v4-1-flash-261017``; ``--anet-command`` to ``anet``. ``--part
   config`` prints only the fragment, ``--part prompt`` only the prompt; the
   default prints both, the prompt as YAML comments.

``anet-org gen-token PATH``
   Write a new random token, mode 0600. The file must not exist.

``anet-org admin --config FILE invite create [--count N] [--expires-days N] [--uses N] [--kind K] [--note TEXT] [--json]``
   Create invites; each code is printed once. ``--count 60 --uses 1`` prints
   sixty single-use codes, one per line (at most 500).

``anet-org admin --config FILE invite list [--all] [--json]``
   List invites (active only unless ``--all``).

``anet-org admin --config FILE invite revoke ID [--remove-joined-after RFC3339]``
   Revoke an invite, optionally removing the members who joined with it at or
   after that time.

``anet-org admin --config FILE member list [--query Q] [--json]``
   List members with their AIDs.

``anet-org admin --config FILE member remove HANDLE [--ban]``
   Remove a member; ``--ban`` refuses the identity if it tries to join again.

``anet-org admin --config FILE member kind HANDLE student|service|staff``
   Change a member's kind.

``anet-org backup --config FILE [--replace] PATH``
   Write a consistent copy of the database (``VACUUM INTO``), safe while the
   server runs. The copy is written beside ``PATH`` and renamed into place.

``anet-org version``
   Print the version.

The ``admin`` and ``backup`` commands open the database directly; run them as
the account that owns it.
