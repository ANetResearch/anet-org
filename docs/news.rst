.. _news:

=============
Release notes
=============

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
