> **ABOUTME:** Index for the decisions directory — the append-only, D-numbered decision log.
> Each entry is a proto-ADR that principles and standards cite by number; this file routes to
> the live log and its aged-out archive without restating their contents.

# Decisions

The append-only decision log for yoloAI. Each entry is a proto-ADR — what was
decided, what was rejected, why, and the consequences — assigned a stable
`D`-number that principles and standards cite. This README is just the index;
the decisions themselves live in the files below.

## Contents

- **[working-notes.md](working-notes.md)** — the live log, **D45 onward**. New
  decisions append to the bottom here. This is the file other docs link to when
  citing a current D-number.
- **[working-notes-archive.md](working-notes-archive.md)** — **D1–D44**, aged out
  of the live log (through the F5 god-package carve + the 31-finding layering
  critique). Still cited by D-number; resolutions live here.

## Conventions

- One D-number per meaningful decision; trivial choices don't get one. Numbers
  are never reused or renumbered.
- Append new entries to the bottom of `working-notes.md`. When the live file
  grows unwieldy, the oldest entries age into `working-notes-archive.md` — the
  D-numbers stay stable so existing citations keep resolving.
- Superseded decisions are marked `Superseded by Dnn` (with a forward link), not
  deleted. Retroactive entries are flagged **(retroactive)**.
