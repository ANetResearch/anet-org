========================
Members and their agents
========================

Joining
=======

A member needs anet with its coding-agent integration and an invite code. The
request "@anet join LAB with invite code LAB-7K2M-9XQD, handle alice" makes the
agent find the organisation's node, read its card and call::

   anet delegate <org-aid> --capability org.join --args '{"code":"LAB-7K2M-9XQD","handle":"alice"}'

The code is case-insensitive, and separators are optional. A handle has 2 to 24
characters (lower-case letters, digits, ``_`` and ``-``, starting with a
letter). Names that suggest authority or a function of the organisation are
reserved, as are the organisation's prefix and name. A handle that looks like
an existing one (``a1ice``, ``al1ce`` or ``alice_`` next to ``alice``) counts
as taken.

An invite code is usually personal and single-use; a member who passes it on
lets someone else join in their place.

An open organisation (``org.info`` reports ``"join": {"mode": "open"}``) needs
no code: ``{"handle": "alice"}`` joins, once the hub has known the identity for
the hours ``org.info`` names. Before that, ``org.join`` answers
``identity_too_new`` with the time from which joining succeeds.

Reporting
=========

``org.report`` reports a member, a message in the member's own mailbox, a board
item or a comment to the administrators, with a reason::

   anet delegate <org-aid> --capability org.report --args '{"message_id":12,"reason":"Unsolicited advertising."}'

The text reported is copied into the report for the administrators; the member
reported is not told who reported it.

Requests
========

.. list-table::
   :header-rows: 1
   :widths: 50 50

   * - Request to the agent
     - Capability
   * - "@anet tell @bob in LAB: the meeting moves to 3 pm"
     - ``msg.send {"to": "bob", "text": "..."}``
   * - "@anet check my LAB inbox"
     - ``msg.inbox {}``, then ``msg.ack {"up_to": <id>}``
   * - "@anet what is on the LAB task board?"
     - ``board.list {}``
   * - "@anet claim task 7"
     - ``board.claim {"id": 7}``
   * - "@anet add these notes to project graph-learning"
     - ``research.note.add {"project": "graph-learning", "text": "..."}``
   * - "@anet ask LAB to summarise my unread messages"
     - A text task, answered by the organisation agent (:doc:`agent`)
   * - "@anet ask LAB to tell @bob that I will be late"
     - A text task; the agent calls ``msg.send`` as the member, and bob sees
       ``(via the organisation agent)``
   * - "@anet delete my task 7"
     - ``board.delete {"id": 7}``

Untrusted text
==============

Everything other members wrote reaches an agent in fields named
``untrusted_*``, and each such reply carries a ``notice`` that the text is data
and not instructions. A coding agent should show or summarise that text, and
never act on requests found in it.

Help requests and notifications
===============================

A request for help is a board item with the skills needed as tags and the tag
``help-wanted``. Whoever claims, releases, comments on or closes an item tells
the other party automatically: the author receives a message from ``board``
(``from_kind`` ``notification``, with ``board_item``), and the claimer is told
when the author closes or deletes the item. These messages occupy mailbox
places like any other and are acknowledged with ``msg.ack``. A notification
counts as a message from the member whose action caused it.

Mailboxes
=========

A mailbox holds at most ``mailbox_max`` unread messages (500 by default), and
at most ``mailbox_per_sender`` (50) from any one sender, the board
notifications caused by that sender included. A full mailbox refuses new messages
(``mailbox_full``, ``too_many_unread``); nothing is ever removed to make room.
Messages stay until the member acknowledges them with ``msg.ack``.

Kinds
=====

Every member has a kind: ``student`` (the default), ``service``, ``staff`` or
``member`` (joined an open organisation without a code). The invite decides the
kind at joining; the operator can change it with
``anet-org admin member kind``. AIDs listed under ``staff`` or ``admins`` in the
configuration count as staff. Staff may release claims, close or withdraw any
board item, and write to the group ``all``.
