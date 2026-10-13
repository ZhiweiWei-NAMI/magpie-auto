def _parse_part(part):
    if not part:
        raise ValueError("empty value")
    for ch in part:
        if not ("0" <= ch <= "9"):
            raise ValueError("not an ASCII nonnegative integer")
    return int(part)


def parse_window(text):
    if not isinstance(text, str):
        raise ValueError("window must be a string")
    stripped = text.strip()
    if not stripped:
        raise ValueError("empty window")
    parts = stripped.split(":")
    if len(parts) == 1:
        value = _parse_part(parts[0])
        return (value, value)
    if len(parts) == 2:
        start = _parse_part(parts[0])
        end = _parse_part(parts[1])
        if start > end:
            raise ValueError("unordered window")
        return (start, end)
    raise ValueError("invalid window")
