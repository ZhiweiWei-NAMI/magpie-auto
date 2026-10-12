def parse_range(value):
    text = value.strip()
    if '-' not in text:
        n = int(text)
        if n < 0:
            raise ValueError(f'malformed range: {value!r}')
        return (n, n)
    parts = text.split('-')
    if len(parts) != 2:
        raise ValueError(f'malformed range: {value!r}')
    a, b = (int(p) for p in parts)
    if a < 0 or b < 0 or a > b:
        raise ValueError(f'malformed range: {value!r}')
    return (a, b)
