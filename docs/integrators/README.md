<!-- ABOUTME: Index for the integrators tier — docs for building software on top of -->
<!-- ABOUTME: yoloAI as a library/daemon (public API, embedding). Stub; to be populated. -->

# Integrators

Documentation for **building on yoloAI** — embedding the Go library, driving it from a
daemon, or calling its public API. This is the audience that wants the public surface and
the layering *contract*, but not the plans, research, or decision log under
[`../contributors/`](../contributors/).

> **Stub.** This tier is intentionally empty for now. The public-API reference, an embedding
> guide, and the consumer-facing layering overview (derived from
> [`../contributors/architecture/overview.md`](../contributors/architecture/overview.md))
> will be populated as a follow-up to the documentation reorg.

## Backend connection environment

yoloAI never reads the embedding process's live environment (§12 "no ambient
configuration"). Everything the selected backend needs to reach its daemon is read from the
environment snapshot you pass as `Options.Env` / `SystemOptions.Env` — a `map[string]string`.
The CLI fills this from its one licensed `os.Environ()` read; an embedder passes each
principal's own environment (never the daemon's process env, so credentials stay
principal-scoped).

Include whichever variables apply to your `Backend`:

| Backend    | Variables                                                              | If absent / blank                                              |
| ---------- | --------------------------------------------------------------------- | ------------------------------------------------------------- |
| `docker`   | `DOCKER_HOST`, `DOCKER_CERT_PATH`, `DOCKER_TLS_VERIFY`, `DOCKER_API_VERSION` | Default local socket, no TLS, version negotiation — identical to the `docker` CLI's own defaults. |
| `podman`   | `CONTAINER_HOST`, `DOCKER_HOST`, `XDG_RUNTIME_DIR`                     | Falls back to the well-known podman socket paths.             |
| `seatbelt` | `PATH`, `HOME`, `TERM`, `LANG`, `LC_*` (locale)                        | The on-host agent runs with an empty locale env.             |

All of these are optional: a blank value is the same code path as an absent one (for docker
this means "no TLS, default socket"). For interactive exec, set `IOStreams.Term` to the
principal's terminal type (e.g. `xterm-256color`) — the library never reads the process's
own `$TERM`.
