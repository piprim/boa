# TODO

## 1. ~~Real placeholders.~~ Done, see `docs/superpowers/specs/2026-09-27-bound-parameters-design.md`. Values are bound as `$1..$n`; arrays, ranges and multiranges are requested in text so the existing parsers keep working. The aliasing constraint recorded here turned out not to apply: every pgx codec copies before handing bytes to a `sql.Scanner`, and the raw-buffer path is only reached for unregistered types requested in binary, which pgcrud never does.
2. Removing feature-flag branches that only mattered for other databases.

## 3. `SendBatch`

`SendBatch` is part of `DBExecutor` for interface parity with the application's unit of work but is not used by the library in this version.

## 4. Binary decoding

Binary decoding of arrays, ranges and multiranges in the library's scanners. They stay text via the result-format map.
