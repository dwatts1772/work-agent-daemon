# Default Routing (mattpocock-skills)

The built-in layer of Routing, and the table format every override layer uses.

| Wake Reason | Situation | Route |
| --- | --- | --- |
| `issue` | `unclear` | `/grill-with-docs` until the requirements are clear, then re-route |
| `issue` | `large` | `/grill-with-docs` → `/to-spec` → `/to-tickets` |
| `issue` | `implementable` | `/implement` |
| `feedback` | `any` | address every unresolved review thread, verify, push (non-force) |
| `ci-failure` | `any` | `/diagnosing-bugs` on the failing checks of the head commit, fix, verify, push (non-force) |
| `review` | `first` | `/code-review PR#<n>`; findings held for Operator approval |
| `review` | `new-head` | `/code-review PR#<n>` against the latest head, scoped to the new diff plus unresolved prior findings; findings held for Operator approval |

## Situations

- `unclear` — the issue leaves a behaviour or acceptance criterion open to more than one reading.
- `large` — the issue spans more than one independently shippable change.
- `implementable` — the issue's acceptance criteria are concrete enough to test against.
- `first` — the Operator has not reviewed any head of this PR, held or submitted.
- `new-head` — the Operator reviewed an earlier head, and the PR head has moved since.
- `any` — every Wake with that Wake Reason that no more specific Situation matches.
