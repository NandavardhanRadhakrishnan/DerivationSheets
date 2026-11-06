---
name: park
description: Park this Hopback session - update the project state and write the handoff so the developer can pick up later.
---
<!-- hopback-template v2 -->
Park this session following the "Parking" section of `.hopback/HOPBACK.md`:

1. Update the tasks you worked on (`hopback task update`).
2. Record decisions not yet recorded.
3. Run `hopback state get`, then `hopback state set --expect-revision <revision>` with the new status.
4. Write the handoff with the sections Objective, Done, Stopped at, Unfinished, Gotchas and Next step.
5. Run `hopback session park --handoff-file <file>`.

Report the result honestly. If `hopback session park` refuses, fix what it lists and retry. If you can't, say that parking failed.
