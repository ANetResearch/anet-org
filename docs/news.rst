.. _news:

=============
Release notes
=============

anet-org 0.3.0
==============

Released 2026-10-26. Requires anet 0.3.9 or later on the organisation's host.

* Open organisations. With ``open_join`` (:doc:`reference/configuration`),
  ``org.join`` admits an identity without an invite code once the hub it names
  has known that identity for ``min_hub_age_hours`` (default 72), counted from
  the end of the UTC day of its first registration. The server asks the hub
  for the identity's registry entry; ``hub_address`` (``host:port`` or
  ``unix:<path>``) lets it reach the hub through a local proxy and keep no
  network of its own. A too recent identity is answered with ``joined: false``
  and ``join_from``. ``daily_joins`` and ``hourly_joins`` cap such joins
  (successful ones count); ``admin.settings.set`` with ``open_join_paused``
  pauses them. New error codes: ``not_on_hub``, ``join_paused``,
  ``hub_unavailable``. Members who join so have the new kind ``member``.
  ``org.info`` reports the ``join`` mode.
* Refused joins without a code count against their caller only; the
  organisation-wide failed-join limit counts joins with a code, which it guards
  against guessing.
* ``org.report`` lets members report a member, a message, a board item or a
  comment; ``admin.report.list`` and ``admin.report.close`` serve the
  administrators. The rate family ``report`` (3/min, 20/day) limits it. Closed
  reports are deleted 90 days after closing.
* The database schema is version 3 (table ``reports``); an earlier database is
  migrated at start.

anet-org 0.2.1
==============

Released 2026-10-25. Requires anet 0.3.9 or later on the organisation's host.

* ``org.members`` returns handles only. ``include_aid`` is refused with
  ``not_permitted`` unless the caller is an administrator: members reach each
  other by handle within the organisation, not through each other's nodes.
* ``admin.member.list`` lists the members with their AIDs for the
  administrators.
* ``admin.settings.get`` and ``admin.settings.set`` manage the settings of the
  organisation's agent: the brief it follows in every run and its model tier
  (``plan`` or ``api``). The agent cannot change them.
* The configuration option ``agent_profile_file`` names where the server writes
  these settings for the account that runs the agent (JSON, mode 0640), at
  start and after every change.

anet-org 0.2.0
==============

Released 2026-10-23. Requires anet 0.3.9 or later on the organisation's host.

The organisation runs inside an agent
-------------------------------------

* The built-in assistant is removed, together with its model configuration,
  key files and model budgets. Requests in natural language are text tasks to
  the organisation's node, answered by the organisation agent that the node
  runs (see :doc:`intro/agent-deployment`). A configuration that still holds an
  ``assistant`` block is refused at start.
* The capability ``ask`` answers ``ask_moved`` until anet-org 0.3.
* The internal capability ``org.admit`` tells the node whether a caller is an
  active member (see :doc:`topics/agent`). It is not advertised and cannot be
  called from the network.
* The capabilities that the agent may use are marked ``agent_tool`` in the
  output of ``gen-config``, with a description and an input schema. A call
  made through the agent carries ``X-ANet-Via: agent``; only these
  capabilities accept it, and every write made through it is marked as written
  by the organisation agent.
* ``gen-config`` writes the node blocks for the agent: ``inbound.admit`` and
  ``auto_reply`` with the runner and the model proxy. ``gen-agent`` prints the
  Hermes configuration and the instructions of the organisation agent.

Task board
----------

* A claim, a release, a comment, completion, withdrawal or deletion of a board
  item notifies the other party by a message from ``board``.
* Notifications count against the mailbox places of the member whose action
  caused them, and one member's actions leave at most 10 unread notifications
  about one item in a mailbox.

Upgrading from 0.1.0
--------------------

#. Back up the database (``anet-org backup``).
#. Remove the ``assistant`` block from the configuration.
#. Start the new program; it migrates the database to schema version 2.
#. Generate the node blocks with ``gen-config`` and merge them into the node's
   configuration, then set up the agent as described in
   :doc:`intro/agent-deployment`.

anet-org 0.1.0
==============

Released 2026-10-21. First release: membership with invite codes, mailboxes,
the task board, research projects and notes, and a built-in assistant.
