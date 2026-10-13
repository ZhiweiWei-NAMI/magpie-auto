def _parse_uint(part):
    if not part or not all(ch in "0123456789" for ch in part):
        raise ValueError("expected an ASCII nonnegative integer: %r" % (part,))
    return int(part)


def parse_window(text):
    if not isinstance(text, str):
        raise ValueError("window must be a string, got %r" % (type(text).__name__,))
    stripped = text.strip()
    if not stripped:
        raise ValueError("window must not be empty")
    parts = stripped.split(":")
    if len(parts) == 1:
        start = end = _parse_uint(parts[0])
    elif len(parts) == 2:
        start = _parse_uint(parts[0])
        end = _parse_uint(parts[1])
    else:
        raise ValueError("window must be 'n' or 'a:b': %r" % (text,))
    if start > end:
        raise ValueError("window start must not exceed end: %r" % (text,))
    return (start, end)
