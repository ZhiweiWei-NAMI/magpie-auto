import re

_ASCII_NUMBER = re.compile(r"[0-9]+")


def _as_number(part):
    if not _ASCII_NUMBER.fullmatch(part):
        raise ValueError("invalid window value: %r" % (part,))
    return int(part)


def parse_window(text):
    """Parse an ASCII nonnegative window.

    Accepts either a single value ``n`` (returning ``(n, n)``) or an ordered
    range ``a:b`` with ``a <= b``.  Outer whitespace is trimmed; anything else
    -- including non-string input -- raises ``ValueError``.
    """
    if not isinstance(text, str):
        raise ValueError("window must be a string, got %r" % (text,))
    text = text.strip()
    if ":" in text:
        parts = text.split(":")
        if len(parts) != 2:
            raise ValueError("invalid window: %r" % (text,))
        start, end = (_as_number(part) for part in parts)
        if start > end:
            raise ValueError("unordered window: %r" % (text,))
        return (start, end)
    value = _as_number(text)
    return (value, value)
