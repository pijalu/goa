# Cache-miss export review

Status: closed for initial automation; follow-up may improve cause confidence.

Added `tooling/cache_miss_review.py` with `validate`, `extract`, and `report` commands. The supplied export contains three valid reports:

- Report 1: full `162560 -> 0`, lost 162560, cause `param_change`, gap 5299ms, sequences 110/111.
- Report 2: partial `62464 -> 1792`, lost 60672, cause `param_change`, gap 2726ms, sequences 150/151.
- Report 3: partial `10752 -> 1792`, lost 8960, cause `param_change`, gap 111879ms, sequences 163/164.

No affinity hint was present. The tool validates archive presence, JSON shape, non-empty ordered request sequences, and returns non-zero for invalid input.
