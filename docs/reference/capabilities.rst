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

Capabilities marked *agent tool* below may also be called by the organisation
agent for a member, with ``X-ANet-Via: agent`` (:doc:`../topics/agent`). Every
write made so carries ``via``, ``(via the organisation agent)``, wherever it is
shown: in messages, board items (``via`` for the post, ``claim_via`` and
``close_via`` for the claim and the closing), comments, projects and notes.
Other capabilities refuse such calls with ``agent_not_permitted``.

Organisation
============

``org.info`` (anyone; agent tool)
   Arguments: ``{}``. Returns ``name``, ``prefix``, ``description``,
   ``contact``, ``how_to_join``, ``invite_format``, ``stats.members``,
   ``caller`` (``member``, ``handle``, ``kind``, ``admin``) and
   ``capabilities`` (all but the internal ``org.admit``).

``org.admit`` (internal)
   Called by the organisation's node for ``inbound.admit``, never from the
   network: ``gen-config`` declares it ``"internal": true`` and lists it in no
   public capability. Arguments are ignored. Returns ``{"admit": true}`` when
   the caller is an active member and ``{"admit": false}`` otherwise; nothing
   else.

``org.join`` (anyone)
   Arguments: ``{"code": "LAB-7K2M-9XQD", "handle": "alice"}``. Returns
   ``joined``, ``org``, ``handle``, ``kind`` and ``next_steps``. Errors:
   ``invite_unknown``, ``invite_expired``, ``invite_revoked``,
   ``invite_used_up``, ``invalid_handle``, ``handle_reserved``,
   ``handle_taken``, ``already_member``, ``join_refused``, ``rate_limited``.

``org.me`` (member; agent tool)
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

``org.members`` (member; agent tool)
   Arguments, all optional: ``query`` (part of a handle), ``kind``,
   ``limit`` (default 100, at most 500), ``offset``. Returns ``members``
   (``handle``, ``kind``, ``joined_at``) and ``more``. Members reach each other
   by handle within the organisation; ``include_aid``, which adds ``aid``, is
   refused with ``not_permitted`` unless the caller is an administrator.

Messages
========

``msg.send`` (member; agent tool)
   Arguments: ``{"to": "bob", "text": "..."}``. ``to`` is a handle, with or
   without ``@``, or the group ``staff``; the group ``all`` is open to staff
   only. ``text`` is at most 4000 bytes. A full mailbox (``mailbox_max``
   unread messages) refuses the message with ``mailbox_full``; a sender with
   ``mailbox_per_sender`` (50) unread messages in it is refused with
   ``too_many_unread``. Nothing is ever evicted. Returns ``sent``, ``to``,
   ``recipients``, for one recipient ``message_id``, and for a group
   ``skipped_full_mailboxes``. Errors: ``recipient_unknown``,
   ``not_permitted``, ``mailbox_full``, ``too_many_unread``, ``too_long``,
   ``rate_limited``.

``msg.inbox`` (member; agent tool)
   Arguments, all optional: ``since`` (a message id, or an RFC 3339 time),
   ``limit`` (default 50, at most 200). Returns ``messages`` oldest first, each
   with ``id``, ``from``, ``from_kind``, ``via_group``, ``via``, ``sent_at``
   and ``untrusted_text``; ``more``; ``mailbox``; and ``notice``. A
   notification of the task board has ``from`` ``board``, ``from_kind``
   ``notification`` and ``board_item`` (the item's id).

``msg.ack`` (member; agent tool)
   Arguments: ``{"ids": [12, 13]}`` (at most 500) or ``{"up_to": 13}``.
   Deletes those messages of the caller's mailbox. Returns ``acknowledged``.

Task board
==========

Items are ``open``, ``claimed``, ``done`` or ``withdrawn``. Only the author or
the claimer may change an item; staff may moderate any item.

The board notifies by message. A claim, a release, a comment, a closing (done
or withdrawn) or a deletion sends a message from ``board`` to the author and,
where it applies, to the claimer, never to the member who acted. A
notification counts as a message from the member who acted: it is skipped
when the recipient's mailbox is full, when that member already holds
``mailbox_per_sender`` places in it, or when it already holds 10 unread
notifications from that member about the item. The replies of these
capabilities list the handles notified in ``notified``.

``board.post`` (member; agent tool)
   Arguments: ``title`` (at most 200 bytes), ``body`` (4000), ``tags`` (at most
   8, each 1 to 32 characters of ``a-z0-9_.-``). A request for help names the
   skills needed as tags, together with ``help-wanted``. Returns ``item``.

``board.list`` (member; agent tool)
   Arguments, all optional: ``status`` (``active``, the default, which is open
   and claimed; ``open``; ``claimed``; ``done``; ``withdrawn``; ``all``),
   ``tag``, ``mine``, ``query``, ``limit`` (default 30, at most 100),
   ``before`` (an item id, for paging); or ``id`` alone for one item with its
   comments. Items have ``id``, ``status``, ``author``, ``claimer``, ``tags``,
   ``created_at``, ``updated_at``, ``untrusted_title``, ``untrusted_body``,
   ``untrusted_note``, ``comment_count``, ``comments`` and the marks ``via``,
   ``claim_via`` and ``close_via``.

``board.claim`` (member; agent tool)
   Arguments: ``{"id": 7}`` to claim an open item, ``{"id": 7, "release":
   true}`` to release a claim (claimer or staff). Errors: ``item_unknown``,
   ``already_claimed``, ``item_closed``, ``not_permitted``.

``board.done`` (member; agent tool)
   Arguments: ``{"id": 7, "note": "..."}`` marks the item done (author, claimer
   or staff); ``{"id": 7, "withdraw": true}`` withdraws it (author or staff).
   Errors: ``item_unknown``, ``item_closed``, ``not_permitted``.

``board.comment`` (member; agent tool)
   Arguments: ``{"id": 7, "text": "..."}`` (at most 2000 bytes; 500 comments
   per item, all returned by ``board.list`` with ``id``). Returns
   ``commented``, ``item_id``, ``comment_id`` and ``notified``.

``board.delete`` (member; agent tool)
   Arguments: ``{"id": 7}``. Only the author may. The item's text, tags and
   comments are erased and it is no longer listed. Returns ``deleted``,
   ``id`` and ``notified``. Errors: ``item_unknown``, ``not_permitted``.

Board items, comments, projects and notes count against the author's
``member_content_bytes`` (``quota_exceeded``). A text ``query`` searches the
newest 2000 items.

Research
========

``research.project.create`` (member; agent tool)
   Arguments: ``name`` (2 to 40 characters of ``a-z0-9-``), ``title``,
   ``description``. Errors: ``project_exists``, ``limit_reached``.

``research.project.list`` (member; agent tool)
   Arguments, all optional: ``query``, ``limit``. Returns ``projects`` with
   ``name``, ``owner``, ``created_at``, ``note_count``, ``untrusted_title`` and
   ``untrusted_description``.

``research.note.add`` (member; agent tool)
   Arguments: ``project``, ``text`` (at most 12000 bytes), ``kind`` (``note``,
   ``reading`` or ``minutes``), ``title``, ``url`` (http or https). Errors:
   ``project_unknown``, ``too_long``, ``invalid_value``.

``research.note.list`` (member; agent tool)
   Arguments: ``project``, or ``mine: true`` for the caller's notes in every
   project; ``kind``, ``mine``, ``limit``, ``before``. Returns ``notes`` with
   ``id``, ``project``, ``kind``, ``author``, ``created_at``,
   ``untrusted_title``, ``untrusted_text`` and ``untrusted_url``.

``research.note.delete`` (member; agent tool)
   Arguments: ``{"id": 12}``. Only the author may. Errors: ``note_unknown``,
   ``not_permitted``.

Retired
=======

``ask`` (member)
   Retired; kept until 0.4. Every call is refused with ``ask_moved``,
   whatever its arguments. Send the request to the organisation as a message
   (a text task); the organisation agent answers it.

Administration
==============

These are ``allow_only`` on the node (off the public card), and ``anet-org``
accepts them only from AIDs in ``admins``.

``admin.invite.create``
   Arguments, all optional: ``count`` (1 to 500, default 1),
   ``expires_days`` (1 to 90, default 14), ``uses`` (1 to 10000, default 200),
   ``kind`` (``student``, ``service``, ``staff``), ``note``. Returns ``codes``
   and ``invites`` (each code shown once), and ``code`` and ``invite`` when one
   was created. ``"count": 60, "uses": 1`` makes sixty single-use codes, the
   recommended form.

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

``admin.member.list``
   Arguments: ``{"query": "part of a handle"}``, optional. Returns ``members``
   (``handle``, ``kind``, ``aid``, ``joined_at``, ``invite_id``).

``admin.settings.get``
   Arguments: ``{}``. Returns the settings of the organisation's agent:
   ``brief`` (the text the agent follows in every run: its role, scope and
   rules), ``model`` (``plan`` or ``api``, the upstream of the model proxy),
   ``updated_at`` and ``updated_by``.

``admin.settings.set``
   Arguments: ``brief`` (at most 8000 bytes) and/or ``model`` (``plan`` or
   ``api``). Stores them and rewrites ``agent_profile_file``; the next run of
   the agent uses them. The agent itself cannot call it.

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
       ``limit_reached``, ``note_unknown``
     - The object's state does not allow the change.
   * - ``mailbox_full``, ``too_many_unread``, ``quota_exceeded``
     - A mailbox or the member's storage is full.
   * - ``agent_not_permitted``
     - The organisation agent called a capability that is not an agent tool.
   * - ``bad_via``
     - ``X-ANet-Via`` has a value other than ``agent``.
   * - ``ask_moved``
     - ``ask`` is retired; send the request as a text task.
   * - ``rate_limited``
     - A per-member, per-code, failed-join or organisation-wide daily limit
       was reached.
   * - ``busy``
     - The server handles its maximum of calls at once.
   * - ``internal_error``
     - An unexpected failure; the daemon reports ``backend_failed``.
