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

With ``"act": true`` in the arguments, two more tools are offered:
``send_message`` (to a handle or the group ``staff``) and ``post_board_item``,
both in the caller's name, at most three actions per call. Without ``act`` they
are not offered at all, so text in a mailbox cannot make the assistant send
anything. The reply lists every action taken under ``actions``.

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
when no total is given. Each member has a daily budget
(``member_daily_tokens``, default 2,000,000) and the organisation a global one
(``global_daily_tokens``, default 200,000,000); 0 means unlimited. Days are UTC.
A budget is checked before every model call: a call that starts over budget is
refused with ``budget_exhausted``; a call that crosses the budget midway ends
with the answer so far and a sentence that the budget was reached.

Without upstreams
=================

When ``assistant.upstreams`` is empty, ``ask`` answers with
``"configured": false`` and a sentence that the assistant is not configured.
