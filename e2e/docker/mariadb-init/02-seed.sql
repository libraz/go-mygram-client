-- Deterministic seed for the closed e2e. Content is fixed (not the random
-- benchmark generator) so the e2e can assert exact result sets.
--
-- Only `enabled = 1` rows are visible to MygramDB (the bundled config marks
-- `enabled = 1` as a required filter), so id 6 below is intentionally hidden.
--
-- Known facts asserted by tests/e2e.test.ts (seeded block):
--   search "python"            -> {3}            (count 1)
--   search "machine learning"  -> {3}            (count 1; id 6 hidden)
--   searchRaw "ruby OR python" -> {2, 3}         (count 2)
--   search "機械学習"           -> {1, 5}         (count 2)
--   count  "golang"            -> 1
--   facet  category            -> tech=3, science=2

-- The init files are UTF-8; tell the server so, otherwise the bytes are
-- interpreted as latin1 and Japanese content is stored as mojibake.
SET NAMES utf8mb4;

USE testdb;

INSERT INTO articles (id, title, content, status, category, enabled) VALUES
  (1, 'ML intro',      '機械学習のチュートリアル入門',        1, 'tech',    1),
  (2, 'Rails guide',   'ruby on rails programming guide',     1, 'tech',    1),
  (3, 'Python ML',     'python machine learning basics',      2, 'science', 1),
  (4, 'Go tutorial',   'golang tutorial for beginners',       1, 'tech',    1),
  (5, 'Deep learning', '機械学習と深層学習の研究',            3, 'science', 1),
  (6, 'Hidden',        'machine learning hidden disabled',    1, 'tech',    0);

INSERT INTO products (id, name, description, status, category, enabled) VALUES
  (1, 'Widget', 'high quality widget product', 1, 'hardware',    1),
  (2, 'Gadget', 'smart gadget device',         1, 'electronics', 1);
