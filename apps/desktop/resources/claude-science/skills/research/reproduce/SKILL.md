---
name: reproduce
description: "Reproduce a published result or a prior experiment. Freeze the target before running, prefer the canonical artifact, measure before judging, and report divergence honestly."
category: research
allowed-tools: [Read, Write, Edit, Bash]
adapted-from: "synthetic-sciences/openscience backend/cli/skills/core/reproduce (Apache-2.0); concept port for Multica claude_science_lab (0.5.114)"
---

# Reproduce

## Overview

A reproduction is a measurement of a claim. These rules exist because the
most common reproduction failure is not technical — it is silently changing
what you are reproducing until the numbers agree.

## Non-negotiables

1. **Freeze the target first.** Before running anything, post the admission
   record as an issue comment: the exact claim under test, the inputs, the
   method, and the budget. A reproduction that adjusts its target after
   seeing the result is not a reproduction.
2. **Canonical path first.** Run the authors' released code/config/data
   before writing your own. Any substitution must be recorded in the
   admission record, never decided mid-run.
3. **Measure before judging.** Report the metric exactly as the claim
   defines it. If you cannot compute that metric, that is a finding, not a
   reason to substitute a different one.
4. **Report divergence honestly.** Agreement and disagreement are both
   results. State what reproduced, what differed, and by how much, with the
   numbers inline.
5. **Keep the artifacts.** Every figure/table you produce must be traceable
   to the code and inputs that generated it (the lab artifact pipeline does
   this — let it).
6. **One claim at a time.** Split multi-claim reproductions into separate
   runs so a partial failure is attributable.

## Workflow

- [ ] Post the admission record (claim / inputs / method / budget).
- [ ] Obtain the canonical artifact; note any substitution + why.
- [ ] Run; capture outputs as artifacts.
- [ ] Compare measured vs claimed, same metric, same scope.
- [ ] Post the reproduction report: reproduced / diverged / unverifiable,
      with numbers and artifact references.
- [ ] Before handing over: would a stranger know exactly what was tested
      and what was not?
