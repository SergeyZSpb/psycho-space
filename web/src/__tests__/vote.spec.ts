import { describe, expect, it } from 'vitest';
import { applyToggle, toggledVote } from '../lib/vote';

describe('vote toggle (shared by items and comments)', () => {
  it('casts a vote: not-voted -> voted, count +1', () => {
    expect(toggledVote({ votes: 3, voted_by_me: false })).toEqual({
      votes: 4,
      voted_by_me: true,
    });
  });

  it('retracts a vote: voted -> not-voted, count -1', () => {
    expect(toggledVote({ votes: 3, voted_by_me: true })).toEqual({
      votes: 2,
      voted_by_me: false,
    });
  });

  it('never drops the count below zero when retracting', () => {
    expect(toggledVote({ votes: 0, voted_by_me: true })).toEqual({
      votes: 0,
      voted_by_me: false,
    });
  });

  it('applyToggle mutates the target in place', () => {
    const comment = { id: 'c1', votes: 5, voted_by_me: false };
    applyToggle(comment);
    expect(comment.votes).toBe(6);
    expect(comment.voted_by_me).toBe(true);
    // and back
    applyToggle(comment);
    expect(comment.votes).toBe(5);
    expect(comment.voted_by_me).toBe(false);
  });
});
