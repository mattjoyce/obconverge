# obconverge

A vault reconciliation CLI for Obsidian.

Converge a drifting vault back to a coherent state: dedupe, reconcile near-duplicates, merge frontmatter, relocate orphans — without breaking the link graph and without leaking secrets.

See [SPEC.md](SPEC.md) for the design. Code lands once the spec is reviewed.

## Stance

Simple, not easy. Values, not places. Decisions as data. Effects at the edges. Policy, not mechanism. Borrowed from Hickey.

## Pipeline

```
scan → classify → plan → apply
```

Three pure phases, one effectful one. The plan is a markdown note the operator reviews *in Obsidian*. Nothing mutates the vault until a human ticks a checkbox.

## Sibling tools

- [obsave](https://github.com/mattjoyce/obsave) — write notes into a vault. obconverge copies its config pattern.

## Status

Spec. No binary yet.
