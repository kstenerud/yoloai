> **ABOUTME:** Index of raw runs for the proxy-chokepoint round, including the invalidated ones,
> each carrying the class of defect and whether it agreed with the hypothesis in play.

# Proxy chokepoint — raw runs

Every run lands here as it happens, **including the ones thrown away** ([D136](../../../../decisions/working-notes.md) §4).
What a bad control looked like is worth as much as the result that replaced it, and this
directory is the fact store the round's synthesis pass reads.

Invalidated runs carry two fields written **at the moment they are invalidated**, never swept
retroactively (D136 §6). `Class:` from `verification-method.md` § *The classes §11 does not
reach*, plus `free-negative` for the §11 class itself. `Direction:` is whether the bad run
agreed with the hypothesis in play — the corpus's asymmetry is that agreeing runs survive,
because a contradicting one makes you look.

Aggregates are a `grep -c`, computed on demand. Nothing here stores a total.

**Harness provenance: every run here is `research_harness_v2`**, and from the stamp onward each
report says so in its own header. What that guarantees, and what it does not, is
[`procedures/verification-rounds.md`](../../../../procedures/verification-rounds.md) § *Which
harness produced a result* — check it before relying on any of these, because v2 guarantees the
probe could report failure and guarantees nothing about whether the arms were separable. Two runs
in this very directory were lost to exactly that gap (P8 runs 1 and 2).

| Run | Verdict | File |
| --- | --- | --- |
| P1 — agent traffic census | **RECORD** (run 3) | [`p1-agent-traffic-census.txt`](p1-agent-traffic-census.txt) |
| P2 — chokepoint viability | **RECORD** (run 3) | [`p2-chokepoint-viability.txt`](p2-chokepoint-viability.txt) |
| P3+P4 — toolchain under a chokepoint | **RECORD** (run 2) | [`p3p4-toolchain-under-chokepoint.txt`](p3p4-toolchain-under-chokepoint.txt) |
| P7 — port publishing | **RECORD** (run 2) | [`p7-port-publishing.txt`](p7-port-publishing.txt) |
| P8 — agent proxy routing | **RECORD** (run 3) | [`p8-agent-proxy-routing.txt`](p8-agent-proxy-routing.txt) |
| P9 — is `--network-none` usable? (follow-up) | **RECORD** (run 2) | [`p9-network-none-utility.txt`](p9-network-none-utility.txt) |

## Invalidated

### P1 run 1 — the census was of a docker image build

- **Class:** `frame-capture` · **Direction:** `confirmed`
- The harness chose a workdir under `/tmp/claude-1000/…`. yoloAI re-creates a workdir's absolute
  path inside the container, docker creates the intermediate directories **root-owned**, and
  Claude Code then refuses `/tmp/claude-1000` as its temp directory — *"owned by uid 0, expected
  1000"*. The agent died 2 seconds after launch. Meanwhile the capture had been running across a
  **7.9 GB image build**, whose build container held the same bridge address the sandbox later
  got, so 764,551 frames were attributed to a sandbox that had done nothing.
- **It read 100.0% proxyable-http with a zero remainder** — a cleaner result than the real one,
  in the direction the round was hoping for, produced entirely by `apt` inside a docker build.
- Caught by the harness's own canary probe, which failed. Without that probe this would have
  been recorded as the census.
- Fixed by moving the workdir off `/tmp/claude-<uid>`, discarding frames that pre-date the
  sandbox, and controlling that the agent actually did the task (the diff must mention
  `package.json`) rather than only that the command returned 0.

### P1 run 2 — the classifier could not see UDP

- **Class:** `predicate-bug` · **Direction:** `confirmed`
- Protocol was inferred from whether the token `UDP` appeared in tcpdump's decoded text. tcpdump
  *decodes* DNS instead of labelling it, so all 24 DNS frames were classified `tcp/53`. Harmless
  by itself — both spellings land in the DNS bucket — but the same predicate would have labelled
  a QUIC flow `tcp/443` and counted it as proxyable. **The one observation that could falsify the
  direction was the one the predicate was structurally unable to express.**
- The run's headline (99.4% HTTP+DNS) was almost certainly right; it is voided anyway, because
  the demonstration that no QUIC was present was performed by hand, after seeing the result, by
  the person who wanted it to be true. That is the retroactive-interpretation shape D136 §6
  exists to refuse.
- Fixed by classifying with **BPF filters against a `.pcap`** — the kernel's own parser — and by
  asserting the buckets sum to the attributable total, so a gap or an overlap is visible rather
  than absorbed.
- Run 2 also produced the first sighting of SSH to GitHub (140.82.121.3:22), which run 3
  reproduced. That observation survives its run's invalidation because it is a raw destination
  in the capture, not a product of the broken predicate.

### P2 runs 1 and 2 — both harness, neither the world

- **Run 1 — `Class:` `predicate-bug` · `Direction:` `confirmed`.** The harness passed
  `--env NO_PROXY=localhost,127.0.0.1`, and `--env` on `new`/`run` comma-splits its value, so the
  launch failed with an error naming a fragment nobody wrote. Every downstream control failed
  honestly and the harness refused a verdict. Filed as [DF195](../../../findings-unresolved.md) rather
  than fixed mid-round — the same flag is `StringArray` on `start`/`restart`/`reset`, so three
  sibling paths already do it the other way.
- **Run 2 — `Class:` `instrument-in-region` · `Direction:` `contradicted`.** A second `probe()` was
  given `baseline(want=False)` on the belief that it would *reuse* the first probe's
  mechanism-absent state. `baseline()` executes the callable, so it ran the with-proxy check and
  got `True`, and v2 refused the run. The fix is the shape the harness is designed for: **one
  probe, one callable, a state flag** — so the baseline and the sample come from the same code
  path rather than from two functions that could quietly differ. Worth recording because the
  failure was a misreading of the harness, not of the subject, and the corrected shape is the one
  future harnesses should copy.

### P3+P4 run 1 — every tool was tested without the mechanism under test

- **Class:** `free-negative` · **Direction:** `contradicted`
- The rig set the proxy with `--env` at launch and then ran each tool through `yoloai exec`,
  assuming the variables would be there. They are not: `--env` reaches the **agent's process
  tree only**. So npm, pip, go, git and even `curl` were run with no proxy and no resolver, and
  every `NO` was free — they were not failing to honour a proxy, they had none.
- The tell was `curl` returning `000` when P2 had already shown curl working through the same
  proxy, and the diagnosis was checked against `/proc/<pid>/environ` rather than inferred: PID 62
  and PID 77 carry the variables, PID 1 and every `exec` shell do not.
- **This is the one invalidated run in this round whose direction is `contradicted`** — it
  disagreed with the hypothesis, which is exactly why it got chased within minutes. The two
  `confirmed` ones in P1 each survived until a control caught them. Same asymmetry D136 counted,
  visible inside a single round.
- Fixed by exporting the proxy explicitly in each command, via a `sh_proxied` helper that says in
  its docstring why it exists. The environment-delivery fact it exposed is not a rig detail — it
  is a design constraint on whatever gets built, and it is recorded in the run's reading notes.

### P7 run 1 — a TCP connect is not a round trip

- **Class:** `free-negative` (inverted: a free *positive*) · **Direction:** `confirmed`
- The probe called `connect()` on the published host port. docker binds that port at **create**
  time, so the connection succeeds whether or not anything is listening inside — the proxy
  accepts and then fails to forward. The baseline, taken with nothing serving, therefore read
  `True` where it had to read `False`, and v2 refused the run.
- Fixed by requiring an HTTP round trip. Kept because the same mistake would make any future
  port-publishing test pass vacuously, on any backend.

### P8 runs 1 and 2 — the control could not see a repeat, and then attribution leaked

- **Run 1 — `Class:` `predicate-bug` · `Direction:` `contradicted`.** The positive control asked
  whether anything *new* had reached the proxy, comparing **sets of destination names**. The host
  it fetched had already been contacted by the sandbox's own resident agent, so the set delta was
  empty and the control failed although the request had plainly happened. Fixed by counting
  request *lines*.
- **Run 2 — `Class:` `confounded-arms` · `Direction:` `confirmed`, and the sharpest specimen in
  this round.** It reported **4 of 4 agents took the proxy** — the answer the round wanted. It was
  wrong: gemini was scored on `api.openai.com` and `chatgpt.com`, which are codex's, arriving
  asynchronously in gemini's window, while gemini's own output showed it had refused to run at
  all. Every control held. Nothing in the harness was broken. The arms were simply not separable,
  because a live Claude agent talks continuously in the same sandbox and the previous agent's
  connections trail into the next window.
- Fixed by requiring a new target to match **the agent's own provider**, excluding the resident
  agent's hosts by name, and settling between agents. The corrected run reads **3 of 4**, with
  gemini recorded as *unmeasured* rather than as either answer.
- Worth keeping as the specimen it is: a bad run that agreed with the hypothesis, passed every
  control, and was caught only by reading a row against the tool's own output.

### P9 run 1 — two rig errors, both producing false negatives

- **Class:** `free-negative` · **Direction:** `confirmed`
- Asked whether any counterexample to "`--network-none` is useless" exists, and found none —
  because it passed `"ignored"` as the `test` agent's prompt (that agent's `HeadlessCmd` is
  `sh -c "PROMPT"`, so the prompt must *be* a shell command; `ignored` is not one, exit 1), and
  because it probed for local work against a container a `--wait` run had already stopped.
- **Both counterexamples are real** and the corrected run finds them: the `test` agent exits 0,
  and `--agent idle --network-none` gives a working offline sandbox with `lo` only.
- The invalidated run's answer — "no counterexamples" — is exactly what the deprecation proposal
  it was checking would have wanted, arrived at by measuring nothing. Recorded because the
  headline expectation *passed in both runs*: only the counterexample arm was wrong, which is the
  half a reader skims.

## Abandoned rather than half-run

- **A scouting run to separate task-driven from setup-driven SSH.** Intended to answer whether
  the GitHub SSH attempt happens in a session that uses no tools. Two attempts failed on shell
  backgrounding rather than on anything about the subject, and the second left a 24-byte capture
  and no sandbox. Abandoned deliberately rather than reported from a partial run; the question is
  P3's and will be asked there with controls. Recorded because an abandoned probe that goes
  unmentioned is indistinguishable from one that was never thought of.
