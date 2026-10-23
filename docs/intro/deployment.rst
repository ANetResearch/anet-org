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
     - Model API keys for the node's model proxy, mode 0600, read by the node
       only (:doc:`agent-deployment`). Never in a repository.
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
   ~/.config/anet-org/anet-org.json`` and merge ``inbound``,
   ``modules.service`` and, with an agent, ``auto_reply`` into
   ``$ANET_DATA_DIR/config.json``. Without an organisation agent, add
   ``--no-agent``; :doc:`agent-deployment` describes the agent's part. Keep
   ``inbound.policy`` at ``closed``. ``deploy/node-config.example.json`` shows
   the output for the example configuration. The capabilities that carry member
   content are declared ``discard``, so the node keeps no copy of their
   arguments and results (:doc:`../topics/security`).
5. Put each administrator's AID on the node's allow list with ``anet peers
   allow <AID>``; the ``admin.*`` capabilities are ``allow_only``.
6. Install the units and start them::

      cp deploy/systemd/* ~/.config/systemd/user/
      systemctl --user daemon-reload
      systemctl --user enable --now anet-org.service anet-org-node.service anet-org-backup.timer

   As root, run ``loginctl enable-linger <account>`` once. The slice
   ``anet-org.slice`` limits both units together to 400 % CPU and 4 GiB of
   memory; the memory limit needs the memory controller delegated to the user
   manager, and the CPU quota the cpu controller (on systemd 249 the default
   delegation is memory and pids only, so ``CPUQuota=`` then has no effect).
   The units' sandboxing is described in :doc:`../topics/security`.
7. Register at the hub: ``anet hub-register <hub URL> --name <name>``. A hub
   that admits by AID prints a line for its operator.
8. Create invite codes with ``anet-org admin … invite create --count N --uses
   1`` (one single-use code per person) and give them out together with the
   node's AID.

Backups
=======

``anet-org-backup.timer`` writes a consistent copy of the database every day
with ``VACUUM INTO``, one file per weekday under
``~/.local/state/anet-org/backups/``. Back up the node's data directory as well:
it holds the organisation's identity.

Upgrades
========

Stop ``anet-org.service``, replace the binary, start it again. The database is
migrated when it is opened; take a backup first. After a change of the
capability catalog, run ``gen-config`` again, update the node's
``config.json``, restart the node and register again so that the hub's entry
lists the new capabilities.

Upgrading to 0.3.9
------------------

* The built-in assistant is removed. A configuration that still has the
  ``assistant`` block is refused at start with a message saying so: delete the
  block. Its model key files are now read by the node's model proxy, named in
  ``gen-config``'s ``--model-plan-key-file`` and ``--model-api-key-file``.
* The database moves to schema version 2: ``via`` columns are added to
  messages, board items (with ``claim_via`` and ``close_via``), comments,
  projects and notes, filled with the empty value for existing rows;
  ``messages.item_id`` is added for board notifications; the assistant's
  ``usage`` table is dropped. A backup made before the upgrade is the way back.
* ``ask`` refuses every call with ``ask_moved``. Members send requests as text
  tasks instead.
* Regenerate the node's blocks with ``gen-config``: the capabilities gain
  ``agent_tool`` and ``input_schema``, ``org.admit`` is declared
  ``internal``, and ``inbound.admit`` and ``auto_reply`` are new
  (:doc:`agent-deployment`).
