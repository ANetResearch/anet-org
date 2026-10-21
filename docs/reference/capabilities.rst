============
Capabilities
============

Every capability takes a JSON object of arguments and answers with a JSON
object. The daemon POSTs the arguments to ``/v1/<capability>`` on the socket.
Unknown arguments are refused with ``unknown_field``; arguments of the wrong
type with ``bad_args``.

Text written by members is returned in fields named ``untrusted_*``; replies
that contain such fields carry a ``notice`` saying that the text is data and
not instructions. Times are RFC 3339 in UTC.

Organisation
============

``org.info`` (anyone)
   Arguments: ``{}``. Returns ``name``, ``prefix``, ``description``,
   ``contact``, ``how_to_join``, ``invite_format``, ``stats.members``,
   ``caller`` (``member``, ``handle``, ``kind``, ``admin``) and
   ``capabilities``.

``org.join`` (anyone)
   Arguments: ``{"code": "LAB-7K2M-9XQD", "handle": "alice"}``. Returns
   ``joined``, ``org``, ``handle``, ``kind`` and ``next_steps``. Errors:
   ``invite_unknown``, ``invite_expired``, ``invite_revoked``,
   ``invite_used_up``, ``invalid_handle``, ``handle_reserved``,
   ``handle_taken``, ``already_member``, ``join_refused``, ``rate_limited``.

``org.me`` (member)
   Arguments: ``{}``. Returns ``handle``, ``kind``, ``aid``, ``joined_at``,
   ``renamed_at``, ``next_rename_at``, ``admin``, ``staff`` and ``mailbox``
   (``messages``, ``limit``).

``org.leave`` (member)
   Arguments: ``{"confirm": true}``. Deletes the membership and the mailbox,
   reopens items the member had claimed and holds the handle for 30 days.
   Errors: ``confirm_required``.

``org.rename`` (member)
   Arguments: ``{"handle": "alice-w"}``. At most once every 7 days. Returns
   ``handle``, ``previous`` and ``next_rename_at``. Errors: ``invalid_handle``,
   ``handle_reserved``, ``handle_taken``, ``rename_too_soon``.

``org.members`` (member)
   Arguments, all optional: ``query`` (part of a handle), ``kind``,
   ``include_aid``, ``limit`` (default 100, at most 500), ``offset``. Returns
   ``members`` (``handle``, ``kind``, ``joined_at`` and, with ``include_aid``,
   ``aid``) and ``more``.

Messages
========

``msg.send`` (member)
   Arguments: ``{"to": "bob", "text": "..."}``. ``to`` is a handle, with or
   without ``@``, or the group ``staff``; the group ``all`` is open to staff
   only. ``text`` is at most 4000 bytes. Returns ``sent``, ``to``,
   ``recipients`` and, for one recipient, ``message_id``. Errors:
   ``recipient_unknown``, ``not_permitted``, ``too_long``, ``rate_limited``.

``msg.inbox`` (member)
   Arguments, all optional: ``since`` (a message id, or an RFC 3339 time),
   ``limit`` (default 50, at most 200). Returns ``messages`` oldest first, each
   with ``id``, ``from``, ``from_kind``, ``via_group``, ``sent_at`` and
   ``untrusted_text``; ``more``; ``mailbox``; ``notice``; and ``dropped``
   (``count``, ``notice``) when messages were dropped since the last read.

``msg.ack`` (member)
   Arguments: ``{"ids": [12, 13]}`` (at most 500) or ``{"up_to": 13}``.
   Deletes those messages of the caller's mailbox. Returns ``acknowledged``.

Task board
==========

Items are ``open``, ``claimed``, ``done`` or ``withdrawn``. Only the author or
the claimer may change an item; staff may moderate any item.

``board.post`` (member)
   Arguments: ``title`` (at most 200 bytes), ``body`` (4000), ``tags`` (at most
   8, each 1 to 32 characters of ``a-z0-9_.-``). Returns ``item``.

``board.list`` (member)
   Arguments, all optional: ``status`` (``active``, the default, which is open
   and claimed; ``open``; ``claimed``; ``done``; ``withdrawn``; ``all``),
   ``tag``, ``mine``, ``query``, ``limit`` (default 30, at most 100),
   ``before`` (an item id, for paging); or ``id`` alone for one item with its
   comments. Items have ``id``, ``status``, ``author``, ``claimer``, ``tags``,
   ``created_at``, ``updated_at``, ``untrusted_title``, ``untrusted_body``,
   ``untrusted_note``, ``comment_count`` and ``comments``.

``board.claim`` (member)
   Arguments: ``{"id": 7}`` to claim an open item, ``{"id": 7, "release":
   true}`` to release a claim (claimer or staff). Errors: ``item_unknown``,
   ``already_claimed``, ``item_closed``, ``not_permitted``.

``board.done`` (member)
   Arguments: ``{"id": 7, "note": "..."}`` marks the item done (author, claimer
   or staff); ``{"id": 7, "withdraw": true}`` withdraws it (author or staff).
   Errors: ``item_unknown``, ``item_closed``, ``not_permitted``.

``board.comment`` (member)
   Arguments: ``{"id": 7, "text": "..."}`` (at most 2000 bytes; 500 comments
   per item). Returns ``commented``, ``item_id`` and ``comment_id``.

Research
========

``research.project.create`` (member)
   Arguments: ``name`` (2 to 40 characters of ``a-z0-9-``), ``title``,
   ``description``. Errors: ``project_exists``, ``limit_reached``.

``research.project.list`` (member)
   Arguments, all optional: ``query``, ``limit``. Returns ``projects`` with
   ``name``, ``owner``, ``created_at``, ``note_count``, ``untrusted_title`` and
   ``untrusted_description``.

``research.note.add`` (member)
   Arguments: ``project``, ``text`` (at most 12000 bytes), ``kind`` (``note``,
   ``reading`` or ``minutes``), ``title``, ``url`` (http or https). Errors:
   ``project_unknown``, ``too_long``, ``invalid_value``.

``research.note.list`` (member)
   Arguments: ``project``, or ``mine: true`` for the caller's notes in every
   project; ``kind``, ``mine``, ``limit``, ``before``. Returns ``notes`` with
   ``id``, ``project``, ``kind``, ``author``, ``created_at``,
   ``untrusted_title``, ``untrusted_text`` and ``untrusted_url``.

Assistant
=========

``ask`` (member)
   Arguments: ``{"text": "...", "act": false}``; ``text`` is at most 4000
   bytes. Returns ``configured``, ``answer``, ``generated``, ``notice``,
   ``actions``, ``stopped`` and ``usage`` (``tokens``, ``member_used_today``,
   ``member_budget``). Errors: ``budget_exhausted``, ``busy``,
   ``assistant_unavailable``. See :doc:`../topics/assistant`.

Administration
==============

These are ``allow_only`` on the node (off the public card), and ``anet-org``
accepts them only from AIDs in ``admins``.

``admin.invite.create``
   Arguments, all optional: ``expires_days`` (1 to 90, default 14), ``uses``
   (1 to 10000, default 200), ``kind`` (``student``, ``service``, ``staff``),
   ``note``. Returns ``code`` (shown once) and ``invite``.

``admin.invite.revoke``
   Arguments: ``{"id": 3, "remove_joined_after": "2026-10-21T08:00:00Z"}``.
   Returns ``revoked``, ``id`` and ``removed_members``.

``admin.invite.list``
   Arguments: ``{"all": true}`` to include inactive invites. Returns
   ``invites`` (``id``, ``state``, ``kind``, ``note``, ``created_at``,
   ``created_by``, ``expires_at``, ``max_uses``, ``uses``, ``revoked_at``).

``admin.member.remove``
   Arguments: ``{"handle": "mallory", "ban": true}``. Returns ``removed`` and
   ``banned``.

Error codes
===========

A refusal is HTTP 4xx with ``{"error": "<code>", "message": "<sentence>"}``.
The daemon passes the code to the caller word for word; the sentence stays in
the organisation node's log. ``busy`` is HTTP 503 with ``Retry-After``, which
the daemon reports as ``busy`` with a retry time.

.. list-table::
   :header-rows: 1
   :widths: 30 70

   * - Code
     - Meaning
   * - ``unauthorized``
     - The daemon's token is missing or wrong.
   * - ``caller_required``
     - The call has no authenticated caller.
   * - ``bad_caller``, ``capability_mismatch``
     - Malformed headers from the daemon.
   * - ``unknown_capability``
     - No such capability.
   * - ``bad_json``, ``bad_args``, ``unknown_field``, ``missing_field``,
       ``invalid_value``, ``too_long``, ``files_not_accepted``
     - The arguments are not acceptable.
   * - ``not_a_member``, ``not_admin``, ``not_permitted``
     - The caller may not do this.
   * - ``invite_unknown``, ``invite_expired``, ``invite_revoked``,
       ``invite_used_up``, ``join_refused``, ``already_member``
     - Joining failed.
   * - ``invalid_handle``, ``handle_reserved``, ``handle_taken``,
       ``rename_too_soon``, ``confirm_required``
     - Handle and membership rules.
   * - ``recipient_unknown``, ``member_unknown``, ``item_unknown``,
       ``project_unknown``
     - The named object does not exist.
   * - ``item_closed``, ``already_claimed``, ``project_exists``,
       ``limit_reached``
     - The object's state does not allow the change.
   * - ``rate_limited``
     - A per-member or per-code rate limit was reached.
   * - ``budget_exhausted``, ``assistant_unavailable``, ``busy``
     - The assistant cannot answer now.
   * - ``internal_error``
     - An unexpected failure; the daemon reports ``backend_failed``.
