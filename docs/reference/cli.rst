============
Command line
============

``anet-org serve --config FILE``
   Run the server. Logs are JSON lines on standard error.

``anet-org gen-config --config FILE [--token-file PATH] [--public-admin]``
   Print the ``inbound`` and ``modules.service`` blocks for the node's
   ``config.json``. ``--token-file`` names the token file as the daemon should
   read it (for example ``${CREDENTIALS_DIRECTORY}/token``); by default it is the
   configuration's ``token_file``. ``--public-admin`` lists ``admin.*`` as
   ordinary public capabilities instead of ``allow_only``.

``anet-org gen-token PATH``
   Write a new random token, mode 0600. The file must not exist.

``anet-org admin --config FILE invite create [--expires-days N] [--uses N] [--kind K] [--note TEXT] [--json]``
   Create an invite; the code is printed once.

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
