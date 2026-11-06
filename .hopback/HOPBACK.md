<!-- hopback-template v2 -->
# Hopback

This project uses **Hopback** to keep track of where the work stands across agent sessions. The files in `.hopback/` are the project's memory: the backlog, decisions, the latest status, and a handoff from every parked session. The developer reads a brief built from these files when they return to the project, so keep them accurate.

You, the agent, do the thinking: you write the status, the handoffs and the recommendations. Hopback only stores and shows them.

## Files

| File | Contents |
|---|---|
| `project.json` | Project identity |
| `state.json` | Latest status: mode, bottom line, current feature, blockers, recommended next |
| `backlog.json` | Backlog items (`T-n`), ordered by `rank` |
| `decisions.json` | Decisions (`D-n`): open questions for the developer, and decisions made |
| `events.jsonl` | Append-only history of every change |
| `sessions/<id>.json` | One record per agent session |
| `sessions/<id>.md` | Handoff written when a build session parks |

Read these files directly whenever you like. **Change them only through the `hopback` command.** It locks, validates and records history. Never edit the JSON by hand.

## Starting a session

1. Run `hopback context`. It prints one JSON document with the project, state, open tasks, items needing review, open decisions, the latest handoff and your session.
2. Tell the developer briefly where things stand: the bottom line, what needs their decision, and what you'll do next.
3. Get to work.

## Recording changes as you work

| Situation | What to do |
|---|---|
| The developer explicitly asks ("add this to the backlog", "let's go with Postgres") | Record it directly, with `--origin user`. |
| You come up with an idea, feature, refactor or decision yourself | Ask the developer first. Record it only if they agree, with `--origin agent`. |
| You start, finish or get blocked on a task | Update its status directly. |
| A change may affect other backlog items | `hopback architecture change --summary "..." --affects T-3,T-7`. Never change or cancel those tasks yourself; they are flagged for the developer to review. |
| You need the developer to make a call you can't | Record an open decision: `hopback decision add --title "..." --option "name: tradeoffs"` |

Record project-level changes, not every edit.

## Switching git branches

The files in `.hopback/` describe the whole project, not one branch. When you switch branches, take Hopback's uncommitted changes with you:

```
git stash push --include-untracked -- .hopback
git switch <branch>
git stash pop
```

If `git stash push` says `No local changes to save`, it created no stash: skip `git stash pop`, or you would pop an unrelated older stash. If `git stash pop` reports a conflict in `.hopback/`, stop and tell the developer. Don't resolve the JSON by hand.

## Commands

```
hopback context
hopback task add --title T [--description D] [--type feature|idea|bug|improvement|tech-debt|research|experiment]
                 [--priority high|medium|low] [--status idea|planned|in_progress|blocked] [--depends-on T-1,T-2]
                 --origin user|agent
hopback task update T-n [--status S] [--priority P] [--title T] [--description D] [--type Y] [--depends-on T-1,T-2]
hopback task list [--all] [--json]
hopback task reorder T-5 T-2              # listed tasks move to the top, in that order
hopback decision add --title T [--context C] [--option "name: tradeoffs"]... [--decision D --rationale R] [--supersedes D-n]
hopback decision resolve D-n --decision D [--rationale R]
hopback architecture change --summary S --affects T-1,T-2
hopback review clear T-n
hopback state get
hopback state set --expect-revision N --file state.json      # --file - reads stdin
hopback next set T-n --reason R [--alt "T-m: reason"]...
hopback batch --file ops.json                                # many changes in one call, all or nothing
hopback session park --handoff-file handoff.md               # --handoff-file - reads stdin
```

`batch` takes a JSON array of operations. Each one looks like `{"op": "<name>", ...fields}`. The op names are `task_add`, `task_update`, `task_reorder`, `decision_add`, `decision_resolve`, `architecture_change`, `review_clear`, `state_set` and `next_set`. Each takes the same fields as its command, in snake_case: `depends_on`, `task_id`, `ids`, `expect_revision`, and `state` for `state_set`.

`state set` takes:

```json
{
  "mode": "mid_feature | between_features",
  "bottom_line": "One or two sentences a busy reader needs first.",
  "current_feature": {"task_id": "T-n", "why": "...", "stopped_at": "...", "next_step": "..."},
  "blockers": ["..."],
  "recommended_next": {"task_id": "T-n", "reason": "..."},
  "alternatives": [{"task_id": "T-m", "reason": "..."}],
  "project_status": "active | paused | blocked | completed | archived"
}
```

`current_feature` is required when `mode` is `mid_feature`. Pass the `revision` from `hopback state get` as `--expect-revision`. If someone else has changed the state since you read it, the write is refused: read it again and retry.

## Parking (when the developer types /park or asks to park)

1. Update the status of the tasks you worked on.
2. Record any decisions not yet recorded.
3. Run `hopback state set` with the new status. Write it like a status email to a busy executive: bottom line first.
4. Write the handoff for the next agent. It must contain these sections (any heading level; a section may just say "none"):
   - `## Objective`
   - `## Done`
   - `## Stopped at`
   - `## Unfinished`
   - `## Gotchas`
   - `## Next step`
5. Run `hopback session park --handoff-file <file>`.

If `session park` refuses, fix what it lists and run it again. If you can't complete it, tell the developer plainly that parking failed. Never say the session is parked unless that command succeeded.

## The concierge

A concierge session is for planning and doesn't change project code. It reviews the backlog with the developer, reorders and updates it, records decisions, and records what to build next with `hopback next set`. Use `hopback batch` for bulk changes. Every concierge starts fresh, so write down anything worth keeping as it happens.
