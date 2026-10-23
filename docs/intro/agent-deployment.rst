.. _intro-agent-deployment:

=======================
Deploying with an agent
=======================

This page adds the organisation agent to a deployment made as described in
:doc:`deployment`. It needs anet 0.3.9 or later on the host. The agent runs
under a second account, so that a member who controls the agent's work never
reaches the files of the organisation's account.

Accounts
========

.. list-table::
   :header-rows: 1
   :widths: 25 75

   * - Account
     - Holds
   * - Organisation account (the one that runs ``anet-org``)
     - The node's identity and control token, the organisation's database, the
       ``anet-org`` token and the model key files. Members never get access to
       it.
   * - Runner account, ``anet-runner`` in this guide
     - ``anet runner serve`` and the agent program with its configuration. No
       keys of any kind. Every member's task runs here, with the network and
       the host's GPUs.

Use operating system limits for the runner account (for example a systemd
slice with ``CPUQuota=`` and ``MemoryMax=`` for its user manager), so that the
agent cannot take the whole host.

Steps
=====

1. **Create the runner account** and give it the agent program. For Hermes,
   install Hermes Agent as that account. Do not put any model key in its
   ``.env`` or configuration.

2. **Configure the agent.** As the organisation account, print the fragment
   and the prompt::

      anet-org gen-agent --config ~/.config/anet-org/anet-org.json --agent hermes

   Merge the YAML part into ``config.yaml`` of the runner account (by default
   ``~/.hermes/config.yaml``). It defines the providers ``anet-plan`` and
   ``anet-api`` on ``${ANET_MODEL_BASE}/plan`` and ``${ANET_MODEL_BASE}/api``
   with ``key_env: ANET_MODEL_KEY`` (``plan`` first, ``api`` as the fallback),
   the model (``--model``, default ``deepseek-v4-1-flash-261017``), and the MCP
   server ``org``, which runs ``anet turn-mcp`` with ``ANET_TURN_URL`` and
   ``ANET_TURN_TOKEN``. ``--anet-command`` names the anet binary when it is not
   on the runner account's ``PATH``.

3. **Prepare the runner's socket directory**, writable only by the runner
   account and reachable by the organisation account, for example with
   systemd-tmpfiles (as root)::

      d /run/anet-runner 0750 anet-runner <organisation account's group> -

4. **Start the runner** as a systemd user unit of the runner account, allowing
   only the organisation account to connect::

      anet runner serve --socket /run/anet-runner/runner.sock \
        --allow-user <organisation account> --agent hermes=<path to hermes> \
        --workspace per-peer --keep-days 14

   With ``per-peer``, each member has a work directory of its own that is kept
   for ``--keep-days`` after its last use, so later tasks find earlier results.
   ``per-run`` removes the directory at the end of each task.

5. **Put the model keys in place.** Each upstream key goes in its own file,
   mode 0600, in the organisation account, outside any repository (by default
   ``~/.config/anet-org/ark-plan.key`` and ``~/.config/anet-org/ark-api.key``).

6. **Generate the node's blocks** and merge them into the node's
   ``config.json``::

      anet-org gen-config --config ~/.config/anet-org/anet-org.json \
        --runner-user anet-runner --runner-socket /run/anet-runner/runner.sock

   Besides ``modules.service`` and ``inbound.public_capabilities``, the
   output has:

   ``inbound.admit``
      ``{"capability": "org.admit", "cache_seconds": 30}``: a text task from a
      peer on no list is accepted only when ``org.admit`` answers
      ``{"admit": true}``.
   ``auto_reply``
      ``backend`` ``exec``, ``agent`` ``hermes``, ``untrusted`` ``off``,
      ``admitted`` ``direct``, ``runner`` (``socket``, ``expected_user``),
      ``model_proxy`` (``listen`` on loopback, ``upstreams`` ``plan`` and
      ``api`` with ``api_base`` and ``api_key_file``) and
      ``system_prompt``.

   The flags ``--agent``, ``--admitted``, ``--admit-cache-seconds``,
   ``--model-proxy-listen``, ``--model-plan-base``,
   ``--model-plan-key-file``, ``--model-api-base`` and
   ``--model-api-key-file`` change the defaults (:doc:`../reference/cli`).
   Consider ``auto_reply.admitted_budget`` (runs per day, runs per member per
   day, model requests per run) to bound what members together can spend.
   ``anet autoreply set --admitted direct`` sets the mode with a confirmation
   on the terminal.

7. **Restart the node** and check ``anet status`` and ``anet inbound``: they
   show the admission capability and the admitted mode. Register at the hub
   again so that its entry reflects the capabilities.

8. **Test** with a member's identity: a text task such as ``@anet ask ORG to
   report the GPUs of the host`` is answered, and one from an identity that is
   not a member is refused.

Operation
=========

* ``anet-org``'s log has one line per call, with ``via`` set to ``agent`` for
  the agent's calls.
* Removing a member (``anet-org admin member remove``) stops the agent from
  serving that member at the next turn, at most ``cache_seconds`` later.
* Rotating a model key means replacing its file and restarting the node; the
  runner account is not involved.
* The runner account's work directories hold members' results. Their retention
  is ``--keep-days``; include them in backups only if members rely on them.
