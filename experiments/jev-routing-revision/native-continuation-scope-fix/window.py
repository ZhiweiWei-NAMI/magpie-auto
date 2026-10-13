"""Parsing helpers for window specifications."""

import re

# Only ASCII decimal digits are accepted; no sign characters, no Unicode digits.
_NUMBER = r"[0-9]+"
_WINDOW_RE = re.compile(r"^(?P<a>" + _NUMBER + r")(?:\s*:\s*(?P<b>" + _NUMBER + r"))?$")


def parse_window(text):
    """Parse a window specification into a ``(start, end)`` tuple.

    Accepts a nonnegative ASCII integer ``n`` (returning ``(n, n)``) or an
    ``a:b`` pair with ``a <= b`` (returning ``(a, b)``).  Outer whitespace is
    ignored, as is whitespace surrounding the colon.  Any invalid input raises
    ``ValueError``.
    """
    if not isinstance(text, str):
        raise ValueError("window specification must be a string")

    stripped = text.strip()
    if not stripped:
        raise ValueError("empty window specification")

    match = _WINDOW_RE.match(stripped)
    if match is None:
        raise ValueError("invalid window specification: %r" % (text,))

    a = int(match.group("a"))
    b_group = match.group("b")
    b = a if b_group is None else int(b_group)

    if a > b:
        raise ValueError("window start must not exceed end: %r" % (text,))

    return (a, b)
