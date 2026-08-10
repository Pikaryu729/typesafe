-- Accounts, the keys that reach them, and the runs they produced.

create table users (
    id           text primary key,
    display_name text not null,
    created_at   timestamptz not null default now(),
    last_seen_at timestamptz not null default now()
);

-- An account is reached by one or more public keys. The fingerprint is the
-- identity; the display name is decoration.
create table user_keys (
    fingerprint text primary key,
    user_id     text not null references users (id) on delete cascade,
    added_at    timestamptz not null default now()
);

create index user_keys_user_id_idx on user_keys (user_id);

-- A run stores the passage's seed and length rather than its text: the passage
-- is reproducible from those two numbers through words.Passage.
create table runs (
    id          bigserial primary key,
    user_id     text not null references users (id) on delete cascade,
    mode        text not null check (mode in ('practice', 'race')),
    seed        bigint not null,
    word_count  integer not null,
    wpm         double precision not null,
    raw_wpm     double precision not null,
    accuracy    double precision not null,
    duration_ms bigint not null,
    keystrokes  integer not null,
    correct     integer not null,
    incorrect   integer not null,
    race_code   text,
    place       integer,
    created_at  timestamptz not null default now()
);

-- The profile screen's query: one user's history, newest first.
create index runs_user_created_idx on runs (user_id, created_at desc);

-- Leaderboards are not built yet, but they are the obvious next query and the
-- index is cheap at this size.
create index runs_created_idx on runs (created_at desc);

-- Short-lived, single-use codes that attach another key to an account.
create table link_codes (
    code       text primary key,
    user_id    text not null references users (id) on delete cascade,
    expires_at timestamptz not null
);
