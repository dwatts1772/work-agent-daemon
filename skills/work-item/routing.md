# Default Routing (mattpocock-skills)

The built-in layer of Routing. An override file uses the same table: a row with the same Wake Reason and Situation replaces the row here, a new Situation adds a row.

| Wake Reason | Situation | Route |
| --- | --- | --- |
| `issue` | `unclear` | `/grill-with-docs` until the requirements are clear, then re-route |
| `issue` | `large` | `/grill-with-docs` → `/to-spec` → `/to-tickets` |
| `issue` | `ready` | `/implement`, then open a draft PR for the item's own branch |
| `feedback` | `any` | address every unresolved review thread, verify, push (non-force); replies to reviewers held for Operator approval |
| `ci-failure` | `any` | `/diagnosing-bugs` on the failing checks of the head commit, fix, verify, push (non-force) |
| `review` | `first` | `/code-review PR#<n>`; findings held for Operator approval |
| `review` | `new-head` | `/code-review PR#<n>` against the latest head, scoped to the new diff since the last reviewed head plus unresolved prior findings; findings held for Operator approval |

## Situations

- `unclear` — the issue leaves a behaviour or acceptance criterion open to more than one reading.
- `large` — the issue spans more than one independently shippable change.
- `ready` — the issue's acceptance criteria are concrete enough to test against.
- `first` — no review of this PR by the Operator exists, held or submitted.
- `new-head` — the Operator reviewed an earlier head (held findings or a submitted review), and the PR head has moved since.
- `any` — every Wake with that Wake Reason.
