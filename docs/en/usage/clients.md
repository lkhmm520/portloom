# Clients and enrollment

Create an enrollment token in the console. Tokens expire, are single-use, have a maximum lifetime of 30 days, and are shown in plaintext only once. The Server stores only a verifier.

On first start, the Agent generates a request ID and long-lived Agent token locally, atomically persists them in `/data/agent.json.pending`, and submits their values with `TM_CLIENT_NAME` and `TM_ENROLLMENT_TOKEN`. The Server stores only the Agent-token verifier and returns the Client ID. After `/data/agent.json` is durable, the pending claim is removed. If a response is lost, the same request ID can safely recover the same Client without consuming another token.

After successful enrollment:

1. confirm heartbeat and online state;
2. back up the Agent state directory;
3. remove the one-time enrollment token from the environment;
4. recreate the Agent and confirm it still connects.

For separate Web and media SSH master connections, create two Clients with separate state directories and enrollment tokens. They may share the same SSH key read-only, but must never share Agent state.
