========
Security
========

Identity
========

* ``anet-org`` listens only on a Unix socket, mode 0600 (0660 with
  ``socket_group``), in a directory that other users cannot write. On Linux the
  uid of every connecting process is checked (``SO_PEERCRED``) against this
  account and ``peer_users``.
* The bearer token from ``token_file`` is compared in constant time (both sides
  hashed first) before a request is routed. A request without it receives 401,
  whatever its path, and learns nothing about the capabilities.
* The caller is taken only from ``X-ANet-Caller``, which the anet daemon sets
  for callers it authenticated. A call without the header is refused with
  ``caller_required``. ``allow_local_admin`` admits such calls as the operator
  for ``admin.*`` and ``org.info``; it exists for tests.
* No argument names or changes the caller. Unknown arguments are refused with
  ``unknown_field``.

Invite codes
============

* ``PREFIX-XXXX-XXXX``: 40 bits from ``crypto/rand`` in Crockford base32.
* The database holds a SHA-256 hash of the prefix and the code. The first eight
  bytes of the hash select the row; the full hash is compared in constant time.
* Expiry is required (default 14 days, at most 90); uses default to 200; codes
  can be revoked, and revoking can remove the members who joined with the code
  after a given time.
* At most 30 joins per minute per code, and at most 10 join attempts per minute
  per caller, which bounds guessing.
* A removed member can be banned from joining again.

Limits
======

* The daemon enforces per-caller quotas and an argument size per capability
  (``gen-config`` sets them); ``anet-org`` reads at most 64 KiB of a body.
* Every text field has a size cap, and control characters are refused.
* Per-member rate limits per capability family (``join``, ``identity``,
  ``read``, ``msg``, ``write``, ``ask``, ``admin``) answer ``rate_limited``.
* Mailboxes keep at most ``mailbox_max`` messages, dropping the oldest with a
  notice.
* A handle given up by renaming or leaving stays reserved for its former holder
  for 30 days, so nobody else can take it at once.

Content
=======

* Text written by members is returned in ``untrusted_*`` fields with a notice.
* The log has one line per call: capability, caller AID, interaction id, HTTP
  status, code and duration. Arguments, replies and message text are never
  logged.
* Top-level numbers of a reply become metrics in the node's evidence records;
  replies keep counts and identifiers there and nothing else.
