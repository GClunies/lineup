# A Responding round takes the Feedback before it

This ADR replaces the last consequence of [ADR 0026](0026-the-user-approves-every-reply-that-is-not-a-fix.md).

The poll reads Feedback: each comment that a person other than the user wrote outside a Review thread. Feedback is a comment in the PR conversation, or a review with a body or a request for changes. New Feedback is a gap of the Landing bar, and it starts a Responding round. Each Responding round for people takes the Feedback that the first poll after its start sees. Only Feedback after that counts as new. We chose this because a comment outside a thread has no resolve. A reviewer's request in a review body or in the PR conversation started no round before, and the user had to find it by hand.

## Considered Options

- **Feedback is answered when the user wrote a later comment.** Rejected. A thanks or a plain approval needs no reply, so it would keep the PR from landing. A Worker posts only a reply that reports a fix, by ADR 0026. Also, a reply after a new comment would hide that comment from the poll.
- **Only a review edge starts the round, as before.** Rejected. An edge in CI running starts nothing, so a review that came while CI ran was lost.
- **Read every comment's body, and start a round only for a request.** Rejected. lineup cannot tell a request from a thanks. The Worker reads the comment and decides. A thanks costs one round with no push.

## Consequences

- A round takes the Feedback that its poll saw, so the Worker must read the newest comments on the PR. A comment that comes while the Worker works starts the next round.
- A review cycle takes no Feedback, because its prompt names only the bot's review. People's Feedback starts its own round after the cycle.
- With `threads = "ignored"`, Feedback does not gate, as threads do not. The zero Landing bar ignores threads, so Tasks stored before the Landing bar do not change.
- A Task stored before this ADR takes the Feedback of its first poll, so old comments on its PR are not new.
- The ADR 0026 rule covers Feedback. A Worker posts a reply in the PR conversation only when the reply reports a fix. Every other reply is a Draft reply.
- With the Responding budget spent, new Feedback keeps the `budget spent` flag. A comment has no resolve, so the user runs the Stage again, hides the comment, or closes the Task.
- A hidden comment is not Feedback. The user can hide a comment on GitHub to drop it before a round takes it.
