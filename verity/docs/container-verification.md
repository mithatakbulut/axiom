# Rootless container verification

Container verification is opt-in through the trusted
`.verity/verification.yaml` manifest:

```yaml
version: 1
trust_level: untrusted
isolation: rootless_container_no_network
container_image: registry.example/checks@sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef
# Optional; exact DNS hostname and TCP port pairs only.
network_allowlist:
  - proxy.golang.org:443
commands:
  test:
    command: [go, test, ./...]
    required: true
    timeout_seconds: 300
profiles:
  standard: [test]
```

Set `trust_level: untrusted` to require container isolation; the manifest is a
trusted control file and cannot be supplied by the candidate tree. The image
must be pinned by SHA-256 digest. The plan and approval include the trust
level, image, isolation policy, and sorted network allowlist, and
verification/proof records retain them. The runner currently supports Docker on Linux and requires `docker info` to report
rootless or user-namespace mode. Missing Docker, an unsupported host, root
execution, or an unhealthy/non-rootless runtime yields `BLOCKED`; there is no
fallback to local execution.

Each check gets a fresh candidate snapshot in a container with no direct network, a
read-only root filesystem, no added capabilities, `no-new-privileges`, a
non-root UID, a PID limit, CPU and memory limits, and bounded tmpfs storage
for `/workspace` and `/tmp`. The candidate is serialized into an archive
capped at 512 MiB and copied over stdin. The container receives only
`HOME`, `TMPDIR`, and `CI` (plus an explicitly
configured Playwright base URL). Without an allowlist, no host path is
mounted. With an allowlist, Verity mounts only its read-only executable and a
private Unix socket directory used by a loopback HTTP CONNECT relay; it does
not mount the host home directory or forward runtime credentials. Container
HTTP proxy variables point only at the local relay. Direct network remains
disabled. The host broker accepts CONNECT only for exact configured
`hostname:port` entries, resolves the hostname itself for each connection,
rejects non-public/reserved IP ranges, dials the validated IP directly, and
requires the TLS ClientHello SNI to match the CONNECT hostname. This pins the
network route and TLS virtual host to the approved destination; the broker does
not decrypt TLS or inspect encrypted HTTP `Host`/`:authority` headers, so the
destination service must enforce its own application-level routing and auth.
Allowlist entries must be lowercase DNS names and numeric ports; wildcards,
IP literals, localhost, and local/internal suffixes are rejected. Containers
are removed after each check, including timeout paths. Images must already exist locally;
the runner does not pull them and requires `/bin/sh` and `tar` in the image to
unpack the candidate.

Playwright base URLs must not contain URL user information or query parameters
named like credentials (`token`, `secret`, `password`, `api_key`, and related
names). A browser scenario may opt into an explicit short-lived test
credential binding:

```yaml
browser:
  adapter: playwright
  base_url: https://test-api.example.test
  scenarios:
    - id: authenticated-smoke
      description: authenticated smoke
      required: true
      spec: playwright/authenticated.spec.js
      expose_to_test_code: true
      test_credentials:
        - name: VERITY_TEST_TOKEN
          source_env: VERITY_TEST_API_TOKEN
          expires_at_env: VERITY_TEST_API_TOKEN_EXPIRES
          ttl_seconds: 300
```

This is allowed only for an untrusted rootless Playwright check over HTTPS
whose base host and port are in the network allowlist. `source_env` and
`expires_at_env` must name trusted host variables prefixed `VERITY_TEST_`; the
expiry value must be RFC3339, in the future, and no farther away than
`ttl_seconds` (maximum 600 seconds). Verity makes the secret available under
`name` only to that Playwright process, caps the check deadline at the expiry,
and narrows that check's egress policy to the base URL host. Ordinary command
checks do not receive these values. The secret value and expiry are not placed
in the approval plan, run, or proof; binding names, lifetime, and check-scoped
destination are recorded, and exact secret values are redacted from captured
output.

`expose_to_test_code: true` is an explicit trust decision: scenario test code
can read the named environment variable. The runner does not mint tokens,
verify server-side token scopes, or decrypt TLS to police encrypted HTTP
paths. Use a disposable least-privilege test credential and a test service
that enforces its own authorization. Do not use provider, GitHub, cloud, or
production credentials. No credential is supplied when a source is missing,
empty, expired, or outside its declared lifetime; that check is `BLOCKED`.

Checks needing external network access are blocked unless the trusted manifest
provides an exact network allowlist. The container's private loopback interface remains available for local-only test
services. The Linux Docker broker path still needs runtime integration
coverage on an available rootless Docker host; the current development host
cannot provide that evidence. On
Unix, a per-user lock serializes Verity snapshot/archive lifetimes across
processes sharing the same temporary directory. Each snapshot/archive is
capped at 512 MiB, so this limits aggregate Verity host staging to one active
pair; crashed runs release the lock automatically and the next lease removes
only owner-marked Verity snapshots and owned candidate archives. Run untrusted
code only on a dedicated Linux host with a rootless runtime and appropriate
host-level resource limits until real malicious-container integration
coverage is complete.
macOS development continues to use the documented local-process mode; it is
not OS isolation.
