========
Overview
========

An organisation on anet is an ordinary anet user. Its node offers a set of
capabilities, and a program behind the node, ``anet-org``, holds the
organisation's state and answers the calls. Nothing in anet or in the hub knows
what an organisation is: the hub relays encrypted calls and lists the node in its
directory, and the node's ``service`` module forwards each call to ``anet-org``.

Components
==========

``anet`` node
   The organisation's identity on the network. Its ``config.json`` declares the
   capabilities in ``modules.service`` and lists who may call them in
   ``inbound.public_capabilities``. The daemon authenticates every caller and
   enforces per-caller quotas before a call reaches ``anet-org``.

``anet-org serve``
   An HTTP server on a Unix socket. It checks the daemon's bearer token, reads
   the caller from ``X-ANet-Caller``, applies its own limits and answers with a
   JSON object.

SQLite database
   Invites (hashes only), members and handles, mailboxes, the task board,
   research projects and notes.

The organisation agent
   An agent program (Hermes at present) that the node runs through ``anet
   runner`` under a second account, for each text task of a member. It reaches
   its model through the node's model proxy and the organisation through the
   node's turn tools, as the member who sent the task (:doc:`../topics/agent`).

The request path
================

1. A member's coding agent calls ``anet delegate <org-aid> --capability
   msg.send --args '{...}'`` (or the equivalent MCP tool).
2. The member's node seals the call and sends it through the hub.
3. The organisation's node verifies the sender, applies the inbound policy and
   the quotas, and POSTs the arguments to
   ``unix:///…/backend.sock:/v1/msg.send`` with ``Authorization: Bearer
   <token>`` and ``X-ANet-Caller: <member AID>``.
4. ``anet-org`` answers. A JSON object is the result; a 4xx with
   ``{"error": "<code>"}`` is a refusal that the member's task reports with that
   code.
5. The organisation's node signs a receipt and returns the result to the member.

A request in natural language takes another path: the member's node sends it
as a text task, the organisation's node asks ``anet-org`` with ``org.admit``
whether the sender is a member, and only then runs the organisation agent,
whose calls reach ``anet-org`` as the same member with ``X-ANet-Via: agent``.

Members never open their nodes to inbound calls: every exchange is a task the
member starts.
