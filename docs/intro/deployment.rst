==========
Deployment
==========

This page runs the organisation's node and ``anet-org`` as systemd user units
of one dedicated account. The files are in ``deploy/`` of the repository.

Layout
======

.. list-table::
   :header-rows: 1
   :widths: 45 55

   * - Path
     - Content
   * - ``~/.local/bin/anet``, ``~/.local/bin/anet-org``
     - The binaries.
   * - ``~/.config/anet-org/anet-org.json``
     - The configuration (:doc:`../reference/configuration`).
   * - ``~/.config/anet-org/token``
     - The bearer token, mode 0600, read by both units.
   * - ``~/.config/anet-org/*.key``
     - Assistant API keys, mode 0600. Never in a repository.
   * - ``~/.local/state/anet-org/org.db``
     - The database.
   * - ``~/.local/state/anet-org-node/``
     - The node's data directory (``ANET_DATA_DIR``).
   * - ``$XDG_RUNTIME_DIR/anet-org/backend.sock``
     - The socket, in a runtime directory of mode 0700.

Steps
=====

1. Install anet with the official installer, and install ``anet-org``.
2. Create the node's identity::

      export ANET_DATA_DIR=~/.local/state/anet-org-node
      anet init
      anet profile set

3. Copy ``deploy/anet-org.example.json`` to ``~/.config/anet-org/anet-org.json``
   and set ``org.name``, ``org.prefix`` and ``admins``. Create the token with
   ``anet-org gen-token ~/.config/anet-org/token``.
4. Print the node's blocks with ``anet-org gen-config --config
   ~/.config/anet-org/anet-org.json`` and merge ``inbound`` and
   ``modules.service`` into ``$ANET_DATA_DIR/config.json``. Keep
   ``inbound.policy`` at ``closed``. ``deploy/node-config.example.json`` shows
   the output for the example configuration.
5. Put each administrator's AID on the node's allow list with ``anet peers
   allow <AID>``; the ``admin.*`` capabilities are ``allow_only``.
6. Install the units and start them::

      cp deploy/systemd/* ~/.config/systemd/user/
      systemctl --user daemon-reload
      systemctl --user enable --now anet-org.service anet-org-node.service anet-org-backup.timer

   As root, run ``loginctl enable-linger <account>`` once. The slice
   ``anet-org.slice`` limits both units together to 400 % CPU and 4 GiB of
   memory; the user manager must have the CPU and memory controllers delegated.
7. Register at the hub: ``anet hub-register <hub URL> --name <name>``. A hub
   that admits by AID prints a line for its operator.
8. Create invite codes with ``anet-org admin … invite create`` and announce
   them together with the node's AID.

Backups
=======

``anet-org-backup.timer`` writes a consistent copy of the database every day
with ``VACUUM INTO``, one file per weekday under
``~/.local/state/anet-org/backups/``. Back up the node's data directory as well:
it holds the organisation's identity.

Upgrades
========

Stop ``anet-org.service``, replace the binary, start it again. After a change of
the capability catalog, run ``gen-config`` again, update the node's
``config.json``, restart the node and register again so that the hub's entry
lists the new capabilities.
