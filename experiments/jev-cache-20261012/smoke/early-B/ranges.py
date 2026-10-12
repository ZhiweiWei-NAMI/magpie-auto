import re

_UINT = r'\d+'


def parse_range(value):
    text = str(value).strip()
    parts = text.split('-')
    if len(parts) == 1:
        if not re.fullmatch(_UINT, parts[0]):
            raise ValueError(f'malformed range: {value!r}')
        n = int(parts[0])
        return (n, n)
    if len(parts) == 2:
        if not all(re.fullmatch(_UINT, p) for p in parts):
            raise ValueError(f'malformed range: {value!r}')
        a, b = int(parts[0]), int(parts[1])
        if a > b:
            raise ValueError(f'reversed range: {value!r}')
        return (a, b)
    raise ValueError(f'malformed range: {value!r}')
