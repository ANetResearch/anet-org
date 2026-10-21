=============
The assistant
=============

The capability ``ask`` hands a request in natural language to a language model
whose tools are operations of ``anet-org``, scoped to the caller. The model
chooses arguments; it never chooses whose data it touches.

Tools
=====

Every call offers four read-only tools:

``list_members``
   The member directory (handles and kinds).
``read_inbox``
   The caller's own mailbox. Reading does not acknowledge.
``list_board``
   The task board, without withdrawn items.
``list_my_notes``
   The caller's own research notes.

Two write tools exist only when the caller grants them with ``act``:

``"act": {"to": ["bob", "staff"], "post": true}``
   ``send_message`` to exactly those handles (or the group ``staff``), and
   ``post_board_item``.
``"act": true``
   ``send_message`` to the ``@handles`` written in the caller's ``text``, and
   ``post_board_item`` when the text asks for a post (words such as post,
   board, task, 任务板, 发布).

The grant is enforced by the tools themselves, not by the prompt: text the
model reads in a mailbox or on the board cannot add a recipient. A write that
copies 41 or more bytes of member-written text the tools returned in the same
request is refused with ``forward_refused``, unless the caller wrote that text
in the request. At most three writes per call. ``actions`` lists every
attempted write, done or refused, with its code.

The answer is returned as ``untrusted_answer`` with a ``notice``: it derives
from other members' text and is data for the caller's agent, not
instructions.

The assistant has no shell, no file access and no network access other than
the configured upstream URLs.

Upstreams and load
==================

``assistant.upstreams`` lists OpenAI-compatible chat-completions endpoints in
order of preference. Each has a ``name``, ``base_url``, ``model``,
``api_key_file`` and ``max_concurrency`` (default 8). Only ``message.content``
of a reply is used; ``reasoning_content`` is ignored.

* An upstream is tried while it has a free slot and is not cooling down.
* A 429, a 5xx, a timeout or a broken reply fails the request over to the next
  upstream and puts the failing one on a cool-down. A 429 whose error names an
  exhausted quota (for example ``AccountQuotaExceeded``) cools down for
  ``quota_cooldown_seconds`` (default one hour), or until the reset time the
  error message states. A time without a zone is read as UTC+8, the zone of
  the provider in the example configuration. Any other failure backs off
  exponentially from ``backoff_base_seconds`` (30) to ``backoff_max_seconds``
  (900); a success resets it.
* A 401 or 403 (a key the upstream refuses) cools the upstream down like an
  exhausted quota.
* Another 4xx is a request the upstream rejects, which the next one would
  reject too: the call fails without failover.
* A global admission queue holds the requests in flight plus ``queue_size``
  waiting ones (default 64); a waiting request gives up after
  ``queue_wait_seconds`` (30). A request that cannot be admitted, or waits too
  long, is refused with ``busy`` (HTTP 503 with ``Retry-After``), which the
  daemon reports to the caller as ``busy`` with a retry time.
* When every upstream is cooling down or failed, ``ask`` is refused with
  ``assistant_unavailable``.

The log records the upstream, the HTTP status, the error code of the upstream,
the cool-down and the token count; never keys or message text.

Budgets
=======

Tokens are counted from the ``usage`` the API returns: ``total_tokens``, which
includes the completion with its reasoning, or prompt plus completion tokens
when no total is given. The organisation has a global daily budget
(``global_daily_tokens``, default 200,000,000), the primary cap, and each
member a share of it (``member_daily_tokens``, default 2,000,000); 0 means
unlimited. Days are UTC.

Before every model call an estimate (half a token per character of the
conversation plus ``max_output_tokens``) is reserved against both budgets in
one transaction; the call is refused with ``budget_exhausted`` when the
reservation does not fit. After the call the reservation is replaced by the
reported usage. The request to the model runs under its own deadline, detached
from the caller: a caller who cancels does not stop it, and its usage is still
charged. A call that may have been served without a usage report (a timeout or
a broken reply) keeps its reservation. A request that runs out of budget
midway ends with the answer so far and a sentence that the budget was
reached.

Without upstreams
=================

When ``assistant.upstreams`` is empty, ``ask`` answers with
``"configured": false`` and a ``message`` that the assistant is not
configured.
