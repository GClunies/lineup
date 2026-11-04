# Tasks live in a local store, not in the tracker

A Task is a local record that links to zero or one tracker Issue. The tracker stays the source of truth for planned work. The local store holds only execution state. We chose this because Tasks without an Issue must exist, and because the Board must never wait on a tracker round trip.

## Considered Options

- **The tracker is the database** (lerp: a Linear status is the queue, and no local store exists). Rejected: it works only for Linear, it cannot hold a Task with no Issue, and every Board change waits on the network.
