# Catalog updater contract

The workflow test is an offline static contract check, not a GitHub integration
or a simulation of the action. Run it with:

```
python3 .github/tests/test_catalog_workflow.py
```

The updater is pinned to
[`22a9089034f40e5a961c8808d113e2c98fb63676`](https://github.com/peter-evans/create-pull-request/tree/22a9089034f40e5a961c8808d113e2c98fb63676).
The following source paths at that revision were inspected:

- [`src/create-or-update-branch.ts`](https://github.com/peter-evans/create-pull-request/blob/22a9089034f40e5a961c8808d113e2c98fb63676/src/create-or-update-branch.ts#L270-L340)
  creates an absent branch only when ahead of base. It compares an existing
  branch with the newly generated changes on base and resets it when needed,
  including base advancement, partial merges and squash merges. Identical
  repeated generation does not produce another branch commit.
- [`src/create-pull-request.ts`](https://github.com/peter-evans/create-pull-request/blob/22a9089034f40e5a961c8808d113e2c98fb63676/src/create-pull-request.ts#L230-L282)
  pushes the configured branch with `--force-with-lease`, calls the PR updater
  only when a diff remains, and deletes the owned branch when no diff remains
  and `delete-branch` is enabled.
- [`src/github-helper.ts`](https://github.com/peter-evans/create-pull-request/blob/22a9089034f40e5a961c8808d113e2c98fb63676/src/github-helper.ts#L110-L185)
  attempts PR creation and handles the already-exists response by updating the
  open PR for the same head/base. Thus an existing branch without an open PR
  (including a closed or merged predecessor) can receive a new PR if a diff
  remains; an existing open PR is updated rather than duplicated.

| State | Expected outcome |
| --- | --- |
| No branch, changed artifact or provenance | Create branch and PR |
| Existing branch and open PR, same change | Keep commit; update existing PR body |
| Existing branch, no open PR, changes remain | Create PR for owned branch |
| Base advances | Rebuild changes on master and update branch if needed |
| Closed/merged prior PR, new change | Create new PR, not reuse closed PR |
| No changes, no branch | No commit or PR |
| Existing branch now matches base | Delete stale branch; no new PR |

The static tests lock the workflow to this reviewed implementation and its
required inputs. GitHub behavior still depends on repository permissions and
service availability; no remote write is part of local verification.

`automation/oh-my-pi-catalog` is reserved exclusively for this automation. Do not
put human work on it: the updater may replace or delete it. Concurrency serializes
workflow runs without cancelling an update in progress. Checkout and PR base are
both master. The updater always runs, with only the artifact and provenance in
`add-paths`, so provenance-only changes and stale-branch cleanup are not skipped.
