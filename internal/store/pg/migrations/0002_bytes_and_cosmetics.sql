-- Bytes: the currency finished attempts pay out, and the cosmetics it buys.

-- Earnings ride in the run's own row rather than in a ledger of their own.
-- The write that records a race is the write that records what it paid, so a
-- run and its bytes cannot disagree and a finished race still costs the typing
-- path exactly one queued statement.
alter table runs add column earned integer not null default 0;

-- One cosmetic a typist has bought.
--
-- The price is recorded as paid rather than looked up, because a balance is
-- derived from these rows: repricing an item in the catalogue would otherwise
-- reach backwards and rewrite everyone's balance.
--
-- There is deliberately no balance column anywhere. A balance is everything
-- earned less everything bought — see store.Balance, which this schema is
-- shaped to let the SQL recompute.
create table purchases (
    id          bigserial primary key,
    user_id     text not null references users (id) on delete cascade,
    cosmetic_id text not null,
    slot        text not null,
    price       integer not null,
    equipped    boolean not null default false,
    created_at  timestamptz not null default now(),
    unique (user_id, cosmetic_id)
);

-- The shop's query: everything one typist owns.
create index purchases_user_idx on purchases (user_id);

-- At most one cosmetic worn per slot. Enforced here rather than only in Go
-- because two sessions of one account can equip at the same instant.
create unique index purchases_user_slot_equipped_idx
    on purchases (user_id, slot) where equipped;
