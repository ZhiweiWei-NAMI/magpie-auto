def _parse_ascii_uint(chunk):
    """Parse a non-empty run of ASCII digits into an int."""
    if not chunk or not chunk.isascii() or not chunk.isdigit():
        raise ValueError("window value must be an ASCII nonnegative integer")
    return int(chunk)


def parse_window(text):
    """Parse ``n`` or ordered ``a:b`` windows, trimming outer whitespace.

    Returns a ``(start, end)`` tuple. Raises ``ValueError`` for anything
    that is not a string, is empty, uses non-ASCII digits, or has an
    unordered/invalid range.
    """
    if not isinstance(text, str):
        raise ValueError("window must be a string")
    stripped = text.strip()
    if not stripped:
        raise ValueError("window must not be empty")
    if ":" in stripped:
        parts = stripped.split(":")
        if len(parts) != 2:
            raise ValueError("window must contain at most one ':'")
        start = _parse_ascii_uint(parts[0])
        end = _parse_ascii_uint(parts[1])
        if start > end:
            raise ValueError("window start must not exceed end")
        return (start, end)
    value = _parse_ascii_uint(stripped)
    return (value, value)
