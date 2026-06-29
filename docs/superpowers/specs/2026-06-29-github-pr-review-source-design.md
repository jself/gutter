# Design: GitHub PR review source for gutter

Date: 2026-06-29

## Goal

Let `gutter` review a GitHub pull request — load the PR's diff into the existing
review UI, and emit a `review.md` that gives an AI agent (Claude) everything it
needs to understand the PR's changes and post the human's comments back to the
PR. Structured so a Bitbucket source can be added later without rework.

## Non-goals

- gutter does **not** post comments to the PR itself. It enriches `review.md`
  so the agent posts them via `gh`. (Posting logic, error handling, and auth
  stay out of gutter.)
- No Bitbucket implementation in this cut — only the seam that makes it
  droppable later.
- No checkout/fetch of the PR branch. gutter reads the PR diff over the network
  and leaves the local working tree untouched.

## Architecture — a "diff source" seam

Today every code path funnels through `getDiff(vcs, rev) -> (unified git diff,
untracked map)`. The parser, intra-line annotator, UI, and markdown layers are
all VCS-agnostic and consume that diff string. A PR source only needs to
produce the same string, so the change is isolated to the diff-fetch layer.

- New config field `PR string`:
  - Flag: `-pr <number|url>`
  - Env: `GUTTER_PR`
  - JSON: `pr`
  - Follows existing precedence (CLI > env > `./.gutter.json` > user config >
    defaults), per CLAUDE.md.
- `-pr` accepts a bare PR **number** (`123`) or a GitHub PR **URL**. A URL
  overrides the repo; a bare number uses the repo `gh` infers from cwd.
- When `PR` is set, `computeData` fetches from the GitHub source instead of
  calling `getDiff(vcs, rev)`. Local VCS detection (`detectVCS`) still runs —
  it's needed for the repo root and the editor "open file" links.
- The GitHub fetch lives behind a tiny interface (e.g. a `prSource` with a
  method returning the diff plus PR metadata). Bitbucket becomes a sibling
  implementation; no other code changes when it's added.

## GitHub source — commands

All via the `gh` CLI, run inside the repo. Purely read; no checkout, no fetch.

- **Diff:** `gh pr diff <n>` (or `gh pr diff <url>`) — emits the `--git` unified
  format the existing parser already handles.
- **Metadata:** `gh pr view <n> --json number,headRefOid,baseRefOid,headRepository`
  (and repo owner/name) — yields:
  - repo `owner/name`
  - PR number
  - head SHA (`headRefOid`)
  - base SHA (`baseRefOid`)

`/diff` continues to recompute on every request (now hitting the network each
reload). This is consistent with the existing "no caching, reload-to-refresh"
rule in CLAUDE.md.

### Caveat surfaced at startup

The diff is the PR's, but the editor "open file" links point at the **local**
working tree, which may not be on the PR's branch. gutter prints a one-line note
about this at startup rather than auto-checking-out. The `review.md` output
makes the same point to the agent (see below).

## Output — `review.md` + PR metadata

When reviewing a PR, the title line becomes:

```
# Review of PR #123 (github)
```

A new machine-readable `## PR` block is written immediately after the title and
**before** `## General feedback`:

```markdown
## PR

- repo: owner/name
- number: 123
- head: <head-sha>
- base: <base-sha>

NOTE: This is a GitHub PR review. The local working tree is NOT the PR's code —
do not read local files to understand the changes. Use `gh pr diff 123` (or
`gh pr view 123`) to see the actual changes these comments refer to.
To post a comment: `gh` review-comment API — use `head` as the commit id,
`path`/`line` from each comment, side RIGHT for added/context, LEFT for removed.
```

Rationale for the NOTE: `review.md` only carries the human's comments (anchors,
an optional fenced snippet of the commented lines, and a body). Since nothing is
checked out, the agent must pull the PR diff for context, and must not be misled
into reviewing the local working tree. The block gives the agent a complete,
self-contained path: read comments -> `gh pr diff` for context -> post back.

### Per-comment side anchors

GitHub review comments need a side (RIGHT/LEFT). `Comment.Side` already exists
(`"new"`/`"old"`). The inline header is extended **only when side is old**, to
preserve the load-bearing round-trip:

- `### path:line`          -> RIGHT (default, unchanged — backward compatible)
- `### path:line (LEFT)`   -> old side
- range form likewise: `### path:line-end` / `### path:line-end (LEFT)`

Changes required, kept conservative per CLAUDE.md's warning about
`renderMarkdown` / `loadPrior` compatibility:

- `renderMarkdown`: append ` (LEFT)` to the `###` location when
  `c.Side == "old"`.
- `inlineHeaderRe`: extend to capture an optional ` (LEFT)` suffix; absence
  means RIGHT/new (so existing `review.md` files parse unchanged).

The `## PR` block is **not** round-tripped by `loadPrior`. It's regenerated from
the live PR source on every save. `loadPrior` already ignores `## `-prefixed
headers other than `## General feedback` / `## Inline comments`, so the block is
skipped on read with no change to the parser's section state machine.

## Data flow

```
-pr set?
  no  -> getDiff(vcs, rev)            (unchanged local path)
  yes -> githubSource.fetch()
           -> gh pr diff <n>          -> diff string  -> parseDiff (unchanged)
           -> gh pr view <n> --json   -> PR metadata  -> DiffData.PR
DiffData -> UI (header shows "PR #123")    -> /save -> renderMarkdown
                                                        (## PR block + (LEFT) anchors)
```

`DiffData` gains an optional PR metadata field; `renderMarkdown` gains the PR
metadata as input so it can emit the `## PR` block and the title variant.

## Error handling

- `gh` missing on PATH, or `gh pr diff`/`gh pr view` failing (not authed, PR not
  found, no network): fail fast at startup with the `gh` stderr surfaced, same
  pattern as the existing `getDiff` error path.
- Invalid `-pr` value (not a number or recognizable URL): clear error before
  starting the server.
- Empty PR diff: same "No changes found yet — reload to check again" behavior as
  the local path.

## Testing

Consistent with CLAUDE.md (no unit-test suite; prefer end-to-end against the
binary). Manual verification path:

1. `gutter -pr <n>` inside a clone with an open PR — UI loads the PR diff,
   header shows `PR #123`, startup prints the local-tree caveat.
2. Add an inline comment on an added line and one on a removed line; save.
3. Inspect `review.md`: `## PR` block present with correct repo/number/head/base;
   added-line comment has no suffix, removed-line comment has ` (LEFT)`.
4. Re-run `gutter -pr <n>`: prior comments reattach (round-trip intact),
   including the `(LEFT)` one.
5. URL form `gutter -pr <url>` resolves the same PR.

## Future: Bitbucket

Add a `bitbucketSource` implementing the same `prSource` interface using the
Bitbucket CLI, plus URL recognition for `bitbucket.org` PR links. The `## PR`
block's forge label and command hints become source-specific. No changes to the
parser, UI, or `loadPrior`.
