# TODO

## 1. ~~Real placeholders.~~ Done. Values are bound as `$1..$n`; arrays, ranges and multiranges are requested in text so the existing parsers keep working. The aliasing constraint recorded here turned out not to apply: every pgx codec copies before handing bytes to a `sql.Scanner`, and the raw-buffer path is only reached for unregistered types requested in binary, which boa never does.

## 2. ~~Removing feature-flag branches that only mattered for other databases.~~ Done. The `feature` package, `dialect.Name`, the `schema.Dialect` interface, MySQL index hints, `OUTPUT`, `OFFSET ... FETCH`, `REPLACE`, `ON DUPLICATE KEY UPDATE` and `ORDER BY`/`LIMIT` on `UPDATE`/`DELETE` are gone.

## 3. ~~`SendBatch`~~ Removed from `DBExecutor`; nothing in the library used it.

## 4. Binary decoding

Binary decoding of arrays, ranges and multiranges in the library's scanners. They stay text via the result-format map.
