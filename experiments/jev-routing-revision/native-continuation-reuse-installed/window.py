def _parse_nonnegative_ascii_int(part):
    """Parse one trimmed part as an ASCII nonnegative integer.

    Only plain runs of ASCII digits are accepted; signs, decimals, spaces and
    non-ASCII digits all raise ValueError.
    """
    if not part:
        raise ValueError("empty numeric part")
    for char in part:
        if char < "0" or char > "9":
            raise ValueError("not an ASCII digit: %r" % (part,))
    return int(part)


def parse_window(text):
    """Parse a window specification into a ``(start, end)`` int tuple.

    Accepts a single nonnegative integer ``n`` (meaning ``(n, n)``) or an
    inclusive range ``a:b`` with ``a <= b``. Outer whitespace on the whole
    string and on each part is trimmed. Any other input raises ValueError.
    """
    if isinstance(text, bool):
        raise ValueError("window must not be a boolean, got %r" % (text,))
    if not isinstance(text, str):
        raise ValueError("window must be a string, got %r" % (type(text).__name__,))
    stripped = text.strip()
    if not stripped:
        raise ValueError("empty window specification")
    parts = stripped.split(":")
    if len(parts) == 1:
        value = _parse_nonnegative_ascii_int(parts[0].strip())
        return (value, value)
    if len(parts) == 2:
        start = _parse_nonnegative_ascii_int(parts[0].strip())
        end = _parse_nonnegative_ascii_int(parts[1].strip())
        if start > end:
            raise ValueError("window start must not exceed end")
        return (start, end)
    raise ValueError("window specification must be 'n' or 'a:b'")
