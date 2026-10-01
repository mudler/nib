# Consistent footer job counts

## Problem

The activity strip reports shell jobs and sub-agents in different ways. The shell chip includes the number of running jobs. Running sub-agents only get individual chips. The aggregate agent chip can say `agents 1 done` while other agents are still running.

This makes the aggregate chips ambiguous. A user cannot compare shell and agent state at a glance.

## Design

The activity strip keeps the aggregate shell and agent chips. It also keeps one detailed chip for each running sub-agent.

The aggregate chips use the same count order:

```text
shell 1 running · 3 done
agents 2 running · 1 done
explore: inspect lifecycle · bash · 3.1k
plan: design footer fix · thinking · 1.2k
```

The count rules are:

- Count all running sub-agents. Include foreground and background agents.
- Show the running count first when it is greater than zero.
- Append the completed count when it is greater than zero.
- Show only the completed count when no job is running.
- Show the idle marker when neither count is greater than zero.
- Keep failure alerts on the aggregate chip.
- Do not include failed jobs in the completed count.

The aggregate agent chip stays visible while agents run. Each running agent also keeps its detailed chip. The aggregate chip opens the agent log list. A detailed chip opens that agent's log.

The pinned `background: ...` summary does not change. It counts only background work and has a separate purpose.

## Implementation

Add one formatter for the shell and agent aggregate text. The formatter takes a label, running count, and completed count. It returns the text forms described above.

Update `activityItems` to:

1. Count shell states.
2. Add the aggregate shell chip.
3. Count agent states before it adds agent chips.
4. Add the aggregate agent chip.
5. Add one detailed chip for each running agent.

Keep the existing alert and focus behavior. The aggregate chips remain the targets for history and failure alerts. Detailed agent chips remain active navigation targets.

## Tests

Extend the activity-strip tests to cover:

- idle shell and agent chips;
- running-only counts;
- completed-only counts;
- mixed running and completed counts;
- all running agents, including foreground and background agents;
- the aggregate agent chip beside detailed running-agent chips;
- failure alerts on the aggregate agent chip;
- focus and log navigation after the extra aggregate chip appears;
- footer fitting at narrow terminal widths.

Run `go test ./tui` and `go test ./...`.

## Documentation

Update the footer section in `README.md`. State that the strip shows aggregate running and completed counts. State that it also shows one detailed chip for each running sub-agent.

Run `make sync-readme` so `selfdoc/README.md` matches the user documentation.
