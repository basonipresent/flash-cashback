-- Fixed, well-known demo user ids. This service doesn't own user creation
-- (decisions.md "User identity") - users are populated directly by SQL,
-- not through an API. Safe to re-run.
INSERT INTO users (id, name) VALUES
    ('11111111-1111-1111-1111-111111111111', 'alice'),
    ('22222222-2222-2222-2222-222222222222', 'bob')
ON CONFLICT (id) DO NOTHING;
