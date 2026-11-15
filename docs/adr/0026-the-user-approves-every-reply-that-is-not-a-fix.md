# The user approves every review reply that is not a fix

In a Responding round, a Worker posts a reply in a person's review thread only when the reply reports a fix. A fix is the change that the thread asks for, in a commit that the Worker pushed. For every other reply, the Worker writes a Draft reply and leaves the thread open. Other replies include a deferral to a follow-up ticket, a rebuttal, a disagreement, an answer to a question, and any reply that the Worker cannot sort. We chose this because a Worker posts as the user, so each reply carries the user's name to the people the user works with. A wrong pushback costs the user's reputation, and the user cannot take back a reply that a reviewer already read.

The rule rests on three choices:

1. The reply decides who posts it, not the comment. A Worker can sort a comment into the wrong class. The test for a reply is plain: it names the commit that made the requested change.
2. The verdict of the review does not decide who posts. An approval, a comment, and a request for changes follow the same rule. The drafts for a request for changes come first, because each one keeps the PR from landing.
3. A follow-up is a draft ticket in the Task's Tracker with no Project. The user picks the Project when the user approves the draft.

## Considered Options

- The Worker posts every reply. This was the rule before this ADR. Rejected: a Worker pushed back in the user's name, and the user did not see the words first.
- The Worker posts a rebuttal that a command proves, unless the reviewer requested changes. Rejected: a correct rebuttal can still read badly to a colleague. The risk to the user does not depend on the verdict.
- The Worker runs `pr-respond`, which asks the user to approve each batch, fixes included. Rejected: most replies report a fix, so the user approves each round of routine work.
- The Worker leaves a comment that it does not fix with no reply. Rejected: the thread stays open, so the PR never meets its Landing bar, and the reviewer gets no answer.

## Consequences

- This ADR replaces the consequence in [ADR 0015](0015-a-landing-bar-decides-ready-to-land.md) that a Worker can answer "won't fix" and resolve the thread. Only the user can.
- A Responding round that leaves a Draft reply ends with the Needs you flag. The flag gives the count of drafts.
- A thread that holds a Draft reply does not start a new Responding round. The Task stays in Needs you until the user decides each draft.
- The user decides each draft in the Worker's tab: post it as written, post it with the user's edit, or make the fix instead. Then the Worker posts each reply and resolves the thread.
- No draft ends with no reply. A thread that has no reply stays open, so the next poll would start a new Responding round for it. If the user leaves a draft undecided, the Worker sets the flag again with the count that is left.
- The rule covers the threads that people opened. Threads that a review bot opened keep the old rule. A reply to a bot speaks to no colleague, and a draft for each bot comment stops each review cycle.
- Comments in the body of a review, outside a thread, are not part of this decision. The poll does not read them.
