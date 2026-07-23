// Optimistic vote-toggle math, shared by wishlist items and their comments.
// Kept as a pure function so the toggle behaviour is unit-testable without
// mounting a component or mocking the network.

export interface Votable {
  votes: number;
  voted_by_me: boolean;
}

// Returns the next {votes, voted_by_me} after the current user toggles their vote.
// Removing a vote never drops the count below zero.
export function toggledVote(current: Votable): Votable {
  if (current.voted_by_me) {
    return { votes: Math.max(0, current.votes - 1), voted_by_me: false };
  }
  return { votes: current.votes + 1, voted_by_me: true };
}

// Mutates the target in place (convenience for the optimistic UI update).
export function applyToggle(target: Votable): void {
  const next = toggledVote(target);
  target.votes = next.votes;
  target.voted_by_me = next.voted_by_me;
}
