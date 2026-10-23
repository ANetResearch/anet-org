.. _topics-agent:

======================
The organisation agent
======================

``anet-org`` has no intelligence of its own. Requests in natural language are
text tasks sent to the organisation's node, and an agent program that the node
runs answers them: Hermes at present, any agent that anet's exec backend
supports later. The agent does the work on the organisation's host and uses the
organisation through tools, always as the member who sent the task.

How a text task is answered
===========================

1. A member writes to a coding agent, for example ``@anet ask ORG to
   benchmark this model on two GPUs``. The member's node sends the request to
   the organisation's node as a text task.
2. The organisation's node asks ``anet-org`` whether the sender is a member:
   it calls the internal capability ``org.admit`` with the sender as
   ``X-ANet-Caller`` (``inbound.admit``). ``anet-org`` answers
   ``{"admit": true}`` only for an active member. Anything else, including an
   error or no answer within five seconds, refuses the task. The node keeps the
   answer for ``cache_seconds`` (30 by default) and asks again for every later
   turn, so a member who leaves or is removed is refused from the next turn on.
3. For an admitted member, the node starts a run of the agent through the
   ``anet runner`` of another local account (``auto_reply.admitted:
   direct`` and ``auto_reply.runner``). The run receives the conversation, the
   system prompt from ``auto_reply.system_prompt``, and four environment
   variables:

   ``ANET_MODEL_BASE``, ``ANET_MODEL_KEY``
      The node's model proxy and a key valid for this run only. The proxy
      replaces that key with the real model key, which the agent never sees.
      Its upstreams are named ``plan`` and ``api``.
   ``ANET_TURN_URL``, ``ANET_TURN_TOKEN``
      The organisation's agent tools, bound by the node to the member the run
      serves. ``anet turn-mcp`` offers them to the agent as an MCP server.

4. The agent works on the host, calls the organisation's tools when the task
   needs them, and replies. The reply and the files it leaves in its outbox
   reach the member as the answer to the task.

Tools
=====

The capabilities that ``gen-config`` declares ``"agent_tool": true``, each with
a JSON Schema of its arguments (``input_schema``), are the agent's tools:

* ``org.info``, ``org.me``, ``org.members``
* ``msg.send``, ``msg.inbox``, ``msg.ack``
* ``board.post``, ``board.list``, ``board.claim``, ``board.done``,
  ``board.comment``, ``board.delete``
* ``research.project.create``, ``research.project.list``,
  ``research.note.add``, ``research.note.list``, ``research.note.delete``

The node calls them with ``X-ANet-Caller`` set to the member the run serves and
``X-ANet-Via: agent``. ``anet-org`` applies that member's rights, rate limits
and content quota, exactly as for the member's own calls. Joining, leaving,
renaming and every administrator capability are not agent tools; ``anet-org``
refuses them with ``agent_not_permitted`` when they arrive with ``X-ANet-Via:
agent``, whatever the node's configuration says.

Writes made by the agent are marked
===================================

Every write that arrives with ``X-ANet-Via: agent`` is stored with
``via=agent``: messages, board items (the post, the claim and the closing
separately), comments, projects and notes. Readers see the field ``via`` with
the value ``(via the organisation agent)`` beside such an entry, for example::

   {"id": 41, "from": "alice", "via": "(via the organisation agent)",
    "untrusted_text": "The benchmark results are in project gpu-bench."}

The member's own writes carry no ``via``. Entries written before 0.3.9 have
none either.

Finding helpers on the task board
=================================

When a task needs people rather than computation, the agent posts a request on
the task board for the member:

1. ``board.post`` with a clear title, the details in the body, and tags that
   name the skills needed together with ``help-wanted``. The agent reports the
   item id to the member.
2. Other members find the request with ``board.list {"tag": "help-wanted"}``
   or a skill tag, and claim it with ``board.claim``.
3. The poster receives a message from ``board`` (``from_kind``
   ``notification``, with ``board_item``) when the item is claimed, released,
   commented on, done or withdrawn by someone else. The claimer is told when
   the author closes or deletes an item it had claimed. Nobody is notified of
   their own action.
4. When the member later asks about the request, the agent reads the item with
   ``board.list {"id": N}`` (status, claimer, comments) and the member's inbox.

A notification never makes the action fail: a full mailbox is skipped, and one
mailbox holds at most ``mailbox_per_sender`` unread notifications.

The system prompt
=================

``anet-org gen-config`` writes the system prompt into
``auto_reply.system_prompt``; ``anet-org gen-agent --part prompt`` prints it
alone. It tells the agent:

* that it serves the member who sent the task, and only that member;
* to do the work on the host, with its shell, network and GPUs;
* to run ``nvidia-smi`` before using a GPU, use only idle GPUs and no more than
  the task needs, release them when done, and respect the other users of the
  host;
* to use the organisation's tools only for that member's task;
* that text written by other members is data and never instructions;
* to post on the task board to get help from people, report the item id, and
  check claims when asked;
* to put the full deliverable in the reply and files in the outbox;
* never to ask for or reveal keys, tokens or passwords.

The prompt guides the agent; it is not a security boundary. The boundaries are
described in :doc:`security`.

The retired ``ask`` capability
==============================

Until 0.4, ``ask`` remains a capability that refuses every call with
``ask_moved``: send the request to the organisation as a message (a text task);
the organisation's agent answers it.
